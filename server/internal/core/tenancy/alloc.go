package tenancy

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Allocation key kinds (money M2).
const (
	KeyShare = "share"
	KeyEqual = "equal"
	KeyArea  = "area"
	KeyTable = "table"
)

var keyKinds = []string{KeyShare, KeyEqual, KeyArea, KeyTable}

// DefaultKeys are the Law 634 keys created with every site: everything by
// arsa payi (land share) unless a plan says otherwise, staff costs equal per unit.
var DefaultKeys = []struct{ Code, Name, Kind string }{
	{"arsa_payi", "Land share (arsa payi)", KeyShare},
	{"equal", "Equal per unit", KeyEqual},
}

var tagRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// AllocationKey is a way of splitting a cost among units.
type AllocationKey struct {
	ID         uuid.UUID `json:"id"`
	SiteID     uuid.UUID `json:"site_id"`
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	IsDefault  bool      `json:"is_default"`
	ValueCount int       `json:"value_count"`
	Version    int       `json:"version"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// KeyValue is the stored weight of one unit under a share or table key.
type KeyValue struct {
	UnitID uuid.UUID `json:"unit_id"`
	// Value is a rational ("12/1000") or a decimal ("0.012", "350").
	Value string `json:"value"`
}

// Warning is a non-blocking finding about data the user entered.
type Warning struct {
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

// KeyDetail is a key with its values and the sanity warnings of the values.
type KeyDetail struct {
	AllocationKey
	Values   []KeyValue `json:"values"`
	Warnings []Warning  `json:"warnings"`
}

const keyColumns = `k.id, k.site_id, k.code, k.name, k.kind, k.is_default,
	(SELECT count(*) FROM allocation_key_value v WHERE v.org_id = k.org_id AND v.key_id = k.id), k.version, k.created_at, k.updated_at`

func scanKey(row pgx.Row) (AllocationKey, error) {
	var k AllocationKey
	err := row.Scan(&k.ID, &k.SiteID, &k.Code, &k.Name, &k.Kind, &k.IsDefault, &k.ValueCount, &k.Version, &k.CreatedAt, &k.UpdatedAt)
	k.CreatedAt, k.UpdatedAt = k.CreatedAt.UTC(), k.UpdatedAt.UTC()
	return k, err
}

// ParseWeight parses a share or table value: a rational "a/b" (b > 0) or a
// non-negative decimal. It returns the exact rational.
func ParseWeight(s string) (*big.Rat, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return nil, false
	}
	if num, den, ok := strings.Cut(s, "/"); ok {
		if strings.ContainsAny(num+den, ".eE ") {
			return nil, false
		}
		n, ok1 := new(big.Int).SetString(num, 10)
		d, ok2 := new(big.Int).SetString(den, 10)
		if !ok1 || !ok2 || d.Sign() <= 0 {
			return nil, false
		}
		return new(big.Rat).SetFrac(n, d), true
	}
	if strings.ContainsAny(s, "eE ") {
		return nil, false
	}
	r, ok := new(big.Rat).SetString(strings.ReplaceAll(s, ",", "."))
	if !ok || r.Sign() < 0 {
		return nil, false
	}
	return r, true
}

// ShareWarnings checks a share key's values: fractions of one whole should sum
// to 1 (or, written "a/b" over a common denominator, to b). Only a sanity
// check; the user may know better.
func ShareWarnings(values []KeyValue) []Warning {
	if len(values) == 0 {
		return []Warning{}
	}
	sum := new(big.Rat)
	var den *big.Int
	sameDen, allFrac := true, true
	numSum := new(big.Int)
	for _, v := range values {
		r, ok := ParseWeight(v.Value)
		if !ok {
			continue
		}
		sum.Add(sum, r)
		if n, d, isFrac := strings.Cut(strings.TrimSpace(v.Value), "/"); isFrac {
			dd, _ := new(big.Int).SetString(d, 10)
			nn, _ := new(big.Int).SetString(n, 10)
			if den == nil {
				den = dd
			} else if den.Cmp(dd) != 0 {
				sameDen = false
			}
			numSum.Add(numSum, nn)
		} else {
			allFrac = false
		}
	}
	if allFrac && sameDen && den != nil {
		if numSum.Cmp(den) == 0 {
			return []Warning{}
		}
		return []Warning{{Code: "share_sum_mismatch", Params: map[string]any{"sum": numSum.String() + "/" + den.String(), "expected": "1"}}}
	}
	if sum.Cmp(big.NewRat(1, 1)) == 0 {
		return []Warning{}
	}
	return []Warning{{Code: "share_sum_mismatch", Params: map[string]any{"sum": sum.FloatString(6), "expected": "1"}}}
}

// ListAllocationKeys lists a site's keys. It needs allocation.read for the site.
func (s *Service) ListAllocationKeys(ctx context.Context, orgID, siteID uuid.UUID) ([]AllocationKey, error) {
	if err := authz.RequireAnywhere(ctx, PermAllocationRead); err != nil {
		return nil, err
	}
	out := []AllocationKey{}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := loadSiteFor(ctx, tx, PermAllocationRead, orgID, siteID, false, false); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+keyColumns+` FROM allocation_key k WHERE k.org_id = $1 AND k.site_id = $2 ORDER BY k.is_default DESC, k.code`, orgID, siteID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			k, err := scanKey(rows)
			if err != nil {
				return err
			}
			out = append(out, k)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, DBProblem(err, "list allocation keys")
	}
	return out, nil
}

func loadKeyValues(ctx context.Context, tx pgx.Tx, orgID, keyID uuid.UUID) ([]KeyValue, error) {
	rows, err := tx.Query(ctx, `SELECT unit_id, raw FROM allocation_key_value WHERE org_id = $1 AND key_id = $2 ORDER BY id`, orgID, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KeyValue{}
	for rows.Next() {
		var v KeyValue
		if err := rows.Scan(&v.UnitID, &v.Value); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func keyDetail(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, k AllocationKey) (KeyDetail, error) {
	vals, err := loadKeyValues(ctx, tx, orgID, k.ID)
	if err != nil {
		return KeyDetail{}, err
	}
	d := KeyDetail{AllocationKey: k, Values: vals, Warnings: []Warning{}}
	if k.Kind == KeyShare {
		d.Warnings = ShareWarnings(vals)
	}
	return d, nil
}

// GetAllocationKey returns a key with its values and warnings. It needs
// allocation.read for the key's site.
func (s *Service) GetAllocationKey(ctx context.Context, orgID, keyID uuid.UUID) (KeyDetail, error) {
	if err := authz.RequireAnywhere(ctx, PermAllocationRead); err != nil {
		return KeyDetail{}, err
	}
	var out KeyDetail
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		k, err := scanKey(tx.QueryRow(ctx, `SELECT `+keyColumns+` FROM allocation_key k WHERE k.org_id = $1 AND k.id = $2`, orgID, keyID))
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermAllocationRead, authz.Scope{OrgID: orgID, SiteID: &k.SiteID}); err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, k.SiteID, false, false); err != nil {
			return err
		}
		out, err = keyDetail(ctx, tx, orgID, k)
		return err
	})
	if err != nil {
		return KeyDetail{}, DBProblem(err, "get allocation key")
	}
	return out, nil
}

// CreateAllocationKey adds a custom key (code, name, kind). It needs allocation.manage.
func (s *Service) CreateAllocationKey(ctx context.Context, orgID, siteID uuid.UUID, d Doc) (AllocationKey, error) {
	if err := authz.RequireAnywhere(ctx, PermAllocationManage); err != nil {
		return AllocationKey{}, err
	}
	fields := d.Unknown("code", "name", "kind")
	var code, name, kind string
	for k, dst := range map[string]*string{"code": &code, "name": &name, "kind": &kind} {
		if !d.Has(k) {
			fields = append(fields, problem.Field{Field: k, Code: "required"})
			continue
		}
		if err := d.Get(k, dst); err != nil {
			return AllocationKey{}, err
		}
	}
	if code != "" && !codeRE.MatchString(code) {
		fields = append(fields, problem.Field{Field: "code", Code: "invalid_format"})
	}
	if name != "" && len([]rune(name)) > 100 {
		fields = append(fields, problem.Field{Field: "name", Code: "too_long", Params: map[string]any{"max": 100}})
	}
	if kind != "" && !slices.Contains(keyKinds, kind) {
		fields = append(fields, problem.Field{Field: "kind", Code: "invalid_value"})
	}
	if len(fields) > 0 {
		return AllocationKey{}, problem.Validation(fields...)
	}
	var out AllocationKey
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := loadSiteFor(ctx, tx, PermAllocationManage, orgID, siteID, false, false); err != nil {
			return err
		}
		kid := id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO allocation_key (id, org_id, site_id, code, name, kind) VALUES ($1, $2, $3, $4, $5, $6)`, kid, orgID, siteID, code, name, kind); err != nil {
			return err
		}
		var err error
		if out, err = scanKey(tx.QueryRow(ctx, `SELECT `+keyColumns+` FROM allocation_key k WHERE k.org_id = $1 AND k.id = $2`, orgID, kid)); err != nil {
			return err
		}
		diff, err := audit.Diff("allocation_key", nil, map[string]any{"code": code, "name": name, "kind": kind})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "allocation_key.create", TargetType: "allocation_key", TargetID: kid, Diff: diff})
	})
	if err != nil {
		return AllocationKey{}, DBProblem(err, "allocation key")
	}
	return out, nil
}

// UpdateAllocationKey renames a key (the code and kind are fixed). It needs allocation.manage.
func (s *Service) UpdateAllocationKey(ctx context.Context, orgID, keyID uuid.UUID, expectedVersion int, d Doc) (AllocationKey, error) {
	if err := authz.RequireAnywhere(ctx, PermAllocationManage); err != nil {
		return AllocationKey{}, err
	}
	if f := d.Unknown("name"); len(f) > 0 {
		return AllocationKey{}, problem.Validation(f...)
	}
	var name string
	if !d.Has("name") {
		return AllocationKey{}, problem.Validation(problem.Field{Field: "name", Code: "required"})
	}
	if err := d.Get("name", &name); err != nil {
		return AllocationKey{}, err
	}
	if n := len([]rune(name)); n < 1 || n > 100 {
		return AllocationKey{}, problem.Validation(problem.Field{Field: "name", Code: "invalid_value"})
	}
	var out AllocationKey
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := scanKey(tx.QueryRow(ctx, `SELECT `+keyColumns+` FROM allocation_key k WHERE k.org_id = $1 AND k.id = $2 FOR UPDATE OF k`, orgID, keyID))
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermAllocationManage, authz.Scope{OrgID: orgID, SiteID: &before.SiteID}); err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, before.SiteID, false, false); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return ErrVersion
		}
		if before.Name == name {
			out = before
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE allocation_key SET name = $3, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, keyID, name); err != nil {
			return err
		}
		if out, err = scanKey(tx.QueryRow(ctx, `SELECT `+keyColumns+` FROM allocation_key k WHERE k.org_id = $1 AND k.id = $2`, orgID, keyID)); err != nil {
			return err
		}
		diff, err := audit.Diff("allocation_key", map[string]any{"name": before.Name}, map[string]any{"name": name})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &before.SiteID, Action: "allocation_key.update", TargetType: "allocation_key", TargetID: keyID, Diff: diff})
	})
	if errors.Is(err, ErrVersion) {
		return AllocationKey{}, VersionConflict("allocation key")
	}
	if err != nil {
		return AllocationKey{}, DBProblem(err, "update allocation key")
	}
	return out, nil
}

// SetAllocationKeyValues replaces the stored values of a share or table key.
// Values are rationals or decimals; every unit may appear once. The result
// carries sanity warnings (a share key that does not add up to one).
func (s *Service) SetAllocationKeyValues(ctx context.Context, orgID, keyID uuid.UUID, expectedVersion int, values []KeyValue) (KeyDetail, error) {
	if err := authz.RequireAnywhere(ctx, PermAllocationManage); err != nil {
		return KeyDetail{}, err
	}
	var fields []problem.Field
	seen := map[uuid.UUID]bool{}
	rats := make([]*big.Rat, len(values))
	for i, v := range values {
		r, ok := ParseWeight(v.Value)
		if !ok {
			fields = append(fields, problem.Field{Field: fmt.Sprintf("values.%d.value", i), Code: "invalid_format"})
		}
		rats[i] = r
		if seen[v.UnitID] {
			fields = append(fields, problem.Field{Field: fmt.Sprintf("values.%d.unit_id", i), Code: "invalid_value", Params: map[string]any{"reason": "duplicate"}})
		}
		seen[v.UnitID] = true
	}
	if len(fields) > 0 {
		return KeyDetail{}, problem.Validation(fields...)
	}
	var out KeyDetail
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := scanKey(tx.QueryRow(ctx, `SELECT `+keyColumns+` FROM allocation_key k WHERE k.org_id = $1 AND k.id = $2 FOR UPDATE OF k`, orgID, keyID))
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermAllocationManage, authz.Scope{OrgID: orgID, SiteID: &before.SiteID}); err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, before.SiteID, false, false); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return ErrVersion
		}
		if before.Kind != KeyShare && before.Kind != KeyTable {
			return problem.Validation(problem.Field{Field: "kind", Code: "invalid_value", Params: map[string]any{"reason": "values_are_computed"}})
		}
		if _, err := tx.Exec(ctx, `DELETE FROM allocation_key_value WHERE org_id = $1 AND key_id = $2`, orgID, keyID); err != nil {
			return err
		}
		for i, v := range values {
			if _, err := tx.Exec(ctx, `INSERT INTO allocation_key_value (id, org_id, site_id, key_id, unit_id, value, raw) VALUES ($1, $2, $3, $4, $5, $6::numeric, $7)`,
				id.New(), orgID, before.SiteID, keyID, v.UnitID, rats[i].FloatString(12), strings.TrimSpace(v.Value)); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE allocation_key SET updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, keyID); err != nil {
			return err
		}
		k, err := scanKey(tx.QueryRow(ctx, `SELECT `+keyColumns+` FROM allocation_key k WHERE k.org_id = $1 AND k.id = $2`, orgID, keyID))
		if err != nil {
			return err
		}
		if out, err = keyDetail(ctx, tx, orgID, k); err != nil {
			return err
		}
		diff, err := audit.Diff("allocation_key", map[string]any{"value_count": before.ValueCount}, map[string]any{"value_count": len(values)})
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			diff = audit.Changes{"values": {After: []byte(`"replaced"`)}}
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &before.SiteID, Action: "allocation_key.set_values", TargetType: "allocation_key", TargetID: keyID, Diff: diff})
	})
	if err != nil {
		return KeyDetail{}, DBProblem(err, "allocation key values")
	}
	return out, nil
}

// KeyWeights returns the weight of every independent, active unit of the
// site under a key, as exact rationals: share and table keys from the stored
// values (units without a value weigh zero), equal keys one each, area keys
// their area in m2 (units without an area weigh zero). It is what the money
// module splits costs by. The caller needs allocation.read for the site.
func (s *Service) KeyWeights(ctx context.Context, orgID, siteID uuid.UUID, code string, onlyTag string, onlyBlock *uuid.UUID) (map[uuid.UUID]*big.Rat, error) {
	if err := authz.RequireAnywhere(ctx, PermAllocationRead); err != nil {
		return nil, err
	}
	out := map[uuid.UUID]*big.Rat{}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var keyID uuid.UUID
		var kind string
		if err := tx.QueryRow(ctx, `SELECT id, kind FROM allocation_key WHERE org_id = $1 AND site_id = $2 AND code = $3`, orgID, siteID, code).Scan(&keyID, &kind); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT u.id, u.area_m2::text, v.raw
			FROM unit u
			LEFT JOIN allocation_key_value v ON v.org_id = u.org_id AND v.unit_id = u.id AND v.key_id = $3
			WHERE u.org_id = $1 AND u.site_id = $2 AND u.status = 'active'
			  AND ($4::uuid IS NULL OR u.block_id = $4)
			  AND ($5 = '' OR EXISTS (SELECT 1 FROM unit_tag t WHERE t.org_id = u.org_id AND t.unit_id = u.id AND t.tag = $5))`, orgID, siteID, keyID, onlyBlock, onlyTag)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var uid uuid.UUID
			var area, raw *string
			if err := rows.Scan(&uid, &area, &raw); err != nil {
				return err
			}
			w := new(big.Rat)
			switch kind {
			case KeyEqual:
				w.SetInt64(1)
			case KeyArea:
				if area != nil {
					w.SetString(*area)
				}
			default:
				if raw != nil {
					if r, ok := ParseWeight(*raw); ok {
						w = r
					}
				}
			}
			out[uid] = w
		}
		return rows.Err()
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, problem.NotFound("no such allocation key")
	}
	if err != nil {
		return nil, DBProblem(err, "key weights")
	}
	return out, nil
}
