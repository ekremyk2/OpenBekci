package community

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Facility is a bookable thing: hall, barbecue area, court, guest flat. The
// JSON tags are the contract's property names (and the audit field names).
type Facility struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"org_id"`
	SiteID      uuid.UUID `json:"site_id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Rules       Rules     `json:"rules"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Version     int       `json:"version"`
}

// facilityAudit is what facility changes compare.
type facilityAudit struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Rules       Rules   `json:"rules"`
	Enabled     bool    `json:"enabled"`
}

func (f Facility) auditView() facilityAudit {
	return facilityAudit{f.Name, f.Description, f.Rules, f.Enabled}
}

const facilityColumns = `id, org_id, site_id, name, description, rules, enabled, created_at, updated_at, version`

func scanFacility(row pgx.Row) (Facility, error) {
	var f Facility
	var rules []byte
	if err := row.Scan(&f.ID, &f.OrgID, &f.SiteID, &f.Name, &f.Description, &rules, &f.Enabled, &f.CreatedAt, &f.UpdatedAt, &f.Version); err != nil {
		return f, err
	}
	if err := json.Unmarshal(rules, &f.Rules); err != nil {
		return f, fmt.Errorf("facility %s rules: %w", f.ID, err)
	}
	f.Rules.normalize()
	f.CreatedAt, f.UpdatedAt = f.CreatedAt.UTC(), f.UpdatedAt.UTC()
	return f, nil
}

// normalize makes the slices non-nil so they encode as arrays.
func (r *Rules) normalize() {
	if r.OpeningHours == nil {
		r.OpeningHours = []OpeningHours{}
	}
	for i := range r.OpeningHours {
		if r.OpeningHours[i].Days == nil {
			r.OpeningHours[i].Days = []int{}
		}
	}
	if r.WhoMayBook == nil {
		r.WhoMayBook = []string{}
	}
}

// requireAny passes when the caller holds one of the permissions for the scope.
func requireAny(ctx context.Context, sc authz.Scope, perms ...authz.Permission) error {
	if authz.PrincipalFromContext(ctx) == nil {
		return problem.Unauthenticated("authentication is required")
	}
	for _, p := range perms {
		if authz.Has(ctx, p, sc) {
			return nil
		}
	}
	return problem.Forbidden("you do not have the " + string(perms[0]) + " permission here")
}

func requireAt(ctx context.Context, tx pgx.Tx, perm authz.Permission, siteID uuid.UUID) (authz.Scope, error) {
	sc, err := siteScope(ctx, tx, siteID)
	if err != nil {
		return sc, err
	}
	return sc, authz.Require(ctx, perm, sc)
}

// loadFacility reads a facility; forUpdate takes the row lock that serializes
// bookings and blocks of one facility.
func loadFacility(ctx context.Context, tx pgx.Tx, orgID, facilityID uuid.UUID, forUpdate bool) (Facility, error) {
	q := `SELECT ` + facilityColumns + ` FROM facility WHERE org_id = $1 AND id = $2`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	f, err := scanFacility(tx.QueryRow(ctx, q, orgID, facilityID))
	if errors.Is(err, pgx.ErrNoRows) {
		return f, problem.NotFound("no such facility")
	}
	return f, err
}

func parseRules(d Doc, key string, siteCurrency string) (Rules, error) {
	var r Rules
	if err := d.Get(key, &r); err != nil {
		return r, err
	}
	r.normalize()
	return r, fieldsErr(r.Validate(siteCurrency))
}

// CreateFacility creates a facility (facilities.manage).
func (s *Service) CreateFacility(ctx context.Context, orgID uuid.UUID, d Doc) (Facility, error) {
	var siteID uuid.UUID
	if err := d.Get("site_id", &siteID); err != nil || siteID == uuid.Nil {
		return Facility{}, validation("site_id", "required")
	}
	name, _ := d.String("name")
	if name == nil || len([]rune(*name)) < 1 || len([]rune(*name)) > 120 {
		return Facility{}, validation("name", "invalid_value")
	}
	desc, err := d.String("description")
	if err != nil {
		return Facility{}, err
	}
	enabled := true
	if d.Has("enabled") {
		if err := d.Get("enabled", &enabled); err != nil {
			return Facility{}, err
		}
	}
	var out Facility
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := requireAt(ctx, tx, PermFacilitiesManage, siteID); err != nil {
			return err
		}
		si, err := loadSite(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		rules, err := parseRules(d, "rules", si.Currency)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(rules)
		out, err = scanFacility(tx.QueryRow(ctx, `
			INSERT INTO facility (id, org_id, site_id, name, description, rules, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+facilityColumns, id.New(), orgID, siteID, *name, desc, raw, enabled))
		if err != nil {
			return err
		}
		diff, err := audit.Diff("facility", facilityAudit{}, out.auditView())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "facility.create", TargetType: "facility", TargetID: out.ID, Diff: diff})
	})
	if err != nil {
		return Facility{}, tenancy.DBProblem(err, "facility")
	}
	return out, nil
}

// GetFacility reads a facility (bookings.read or facilities.manage).
func (s *Service) GetFacility(ctx context.Context, orgID, facilityID uuid.UUID) (Facility, error) {
	var out Facility
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		f, err := loadFacility(ctx, tx, orgID, facilityID, false)
		if err != nil {
			return err
		}
		sc, err := siteScope(ctx, tx, f.SiteID)
		if err != nil {
			return err
		}
		if err := requireAny(ctx, sc, PermBookingsRead, PermFacilitiesManage); err != nil {
			return err
		}
		out = f
		return nil
	})
	if err != nil {
		return Facility{}, tenancy.DBProblem(err, "facility")
	}
	return out, nil
}

// ListFacilities lists a site's facilities (bookings.read or facilities.manage).
func (s *Service) ListFacilities(ctx context.Context, orgID, siteID uuid.UUID, enabled *bool, p tenancy.PageParams) ([]Facility, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Facility
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := siteScope(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := requireAny(ctx, sc, PermBookingsRead, PermFacilitiesManage); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+facilityColumns+` FROM facility
			WHERE org_id = $1 AND site_id = $2 AND id > $3 AND ($4::boolean IS NULL OR enabled = $4) ORDER BY id LIMIT $5`,
			orgID, siteID, after, enabled, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			f, err := scanFacility(rows)
			if err != nil {
				return err
			}
			out = append(out, f)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list facilities")
	}
	items, next := tenancy.Page(out, limit, func(f Facility) uuid.UUID { return f.ID })
	return items, next, nil
}

// UpdateFacility applies a partial update at expectedVersion (facilities.manage).
func (s *Service) UpdateFacility(ctx context.Context, orgID, facilityID uuid.UUID, expectedVersion int, d Doc) (Facility, error) {
	if bad := d.Unknown("name", "description", "rules", "enabled"); len(bad) > 0 {
		return Facility{}, problem.Validation(bad...)
	}
	var out Facility
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := loadFacility(ctx, tx, orgID, facilityID, true)
		if err != nil {
			return err
		}
		if _, err := requireAt(ctx, tx, PermFacilitiesManage, before.SiteID); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return tenancy.ErrVersion
		}
		next := before
		if d.Has("name") {
			n, _ := d.String("name")
			if n == nil || len([]rune(*n)) < 1 || len([]rune(*n)) > 120 {
				return validation("name", "invalid_value")
			}
			next.Name = *n
		}
		if d.Has("description") {
			if next.Description, err = d.String("description"); err != nil {
				return err
			}
		}
		if d.Has("enabled") {
			if err := d.Get("enabled", &next.Enabled); err != nil {
				return err
			}
		}
		if d.Has("rules") {
			si, err := loadSite(ctx, tx, orgID, before.SiteID)
			if err != nil {
				return err
			}
			if next.Rules, err = parseRules(d, "rules", si.Currency); err != nil {
				return err
			}
		}
		diff, err := audit.Diff("facility", before.auditView(), next.auditView())
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			out = before
			return nil
		}
		raw, _ := json.Marshal(next.Rules)
		out, err = scanFacility(tx.QueryRow(ctx, `
			UPDATE facility SET name = $3, description = $4, rules = $5, enabled = $6, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2 RETURNING `+facilityColumns, orgID, facilityID, next.Name, next.Description, raw, next.Enabled))
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &out.SiteID, Action: "facility.update", TargetType: "facility", TargetID: out.ID, Diff: diff})
	})
	if err != nil {
		return Facility{}, tenancy.DBProblem(err, "facility")
	}
	return out, nil
}

// DeleteFacility deletes a facility that has no bookings at all (facilities.manage);
// one with history is disabled instead.
func (s *Service) DeleteFacility(ctx context.Context, orgID, facilityID uuid.UUID, expectedVersion int) error {
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		f, err := loadFacility(ctx, tx, orgID, facilityID, true)
		if err != nil {
			return err
		}
		if _, err := requireAt(ctx, tx, PermFacilitiesManage, f.SiteID); err != nil {
			return err
		}
		if f.Version != expectedVersion {
			return tenancy.ErrVersion
		}
		var future, total int
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'confirmed' AND end_at > now()), count(*) FROM booking WHERE org_id = $1 AND facility_id = $2`,
			orgID, facilityID).Scan(&future, &total); err != nil {
			return err
		}
		if future > 0 {
			return problem.Conflict("the facility has future bookings; cancel them first")
		}
		if total > 0 {
			return problem.Conflict("the facility has booking history; disable it instead of deleting it")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM facility_block WHERE org_id = $1 AND facility_id = $2`, orgID, facilityID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM facility WHERE org_id = $1 AND id = $2`, orgID, facilityID); err != nil {
			return err
		}
		diff, err := audit.Diff("facility", f.auditView(), facilityAudit{})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &f.SiteID, Action: "facility.delete", TargetType: "facility", TargetID: f.ID, Diff: diff})
	})
	return tenancy.DBProblem(err, "facility")
}

// ---------------------------------------------------------------------------
// blocks and availability

// FacilityBlock is a range a manager blocked.
type FacilityBlock struct {
	ID         uuid.UUID `json:"id"`
	OrgID      uuid.UUID `json:"org_id"`
	FacilityID uuid.UUID `json:"facility_id"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Reason     *string   `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type blockAudit struct {
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Reason *string   `json:"reason"`
}

const blockColumns = `id, org_id, facility_id, start_at, end_at, reason, created_at, updated_at`

func scanBlock(row pgx.Row) (FacilityBlock, error) {
	var b FacilityBlock
	err := row.Scan(&b.ID, &b.OrgID, &b.FacilityID, &b.Start, &b.End, &b.Reason, &b.CreatedAt, &b.UpdatedAt)
	b.Start, b.End, b.CreatedAt, b.UpdatedAt = b.Start.UTC(), b.End.UTC(), b.CreatedAt.UTC(), b.UpdatedAt.UTC()
	return b, err
}

// CreateFacilityBlock blocks a range (facilities.manage). Bookings inside it
// are not cancelled: they are a 409 conflict that names them.
func (s *Service) CreateFacilityBlock(ctx context.Context, orgID, facilityID uuid.UUID, d Doc) (FacilityBlock, error) {
	var start, end time.Time
	if err := d.Get("start", &start); err != nil || start.IsZero() {
		return FacilityBlock{}, validation("start", "required")
	}
	if err := d.Get("end", &end); err != nil || end.IsZero() {
		return FacilityBlock{}, validation("end", "required")
	}
	if !end.After(start) {
		return FacilityBlock{}, validation("end", "end_before_start")
	}
	reason, err := d.String("reason")
	if err != nil {
		return FacilityBlock{}, err
	}
	var out FacilityBlock
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		f, err := loadFacility(ctx, tx, orgID, facilityID, true)
		if err != nil {
			return err
		}
		if _, err := requireAt(ctx, tx, PermFacilitiesManage, f.SiteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, start_at FROM booking WHERE org_id = $1 AND facility_id = $2 AND status = 'confirmed'
			AND tstzrange(start_at, end_at, '[)') && tstzrange($3, $4, '[)') ORDER BY start_at LIMIT 10`, orgID, facilityID, start, end)
		if err != nil {
			return err
		}
		var clash []string
		for rows.Next() {
			var bid uuid.UUID
			var at time.Time
			if err := rows.Scan(&bid, &at); err != nil {
				rows.Close()
				return err
			}
			clash = append(clash, fmt.Sprintf("%s (%s)", bid, at.UTC().Format(time.RFC3339)))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(clash) > 0 {
			return problem.Conflict(fmt.Sprintf("confirmed bookings exist in that range: %v; cancel them first", clash))
		}
		out, err = scanBlock(tx.QueryRow(ctx, `INSERT INTO facility_block (id, org_id, site_id, facility_id, start_at, end_at, reason, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+blockColumns, id.New(), orgID, f.SiteID, facilityID, start, end, reason, principalUser(ctx)))
		if err != nil {
			return err
		}
		diff, err := audit.Diff("facility_block", blockAudit{}, blockAudit{out.Start, out.End, out.Reason})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &f.SiteID, Action: "facility.block", TargetType: "facility_block", TargetID: out.ID, Diff: diff})
	})
	if err != nil {
		return FacilityBlock{}, tenancy.DBProblem(err, "block")
	}
	return out, nil
}

// DeleteFacilityBlock removes a block (facilities.manage).
func (s *Service) DeleteFacilityBlock(ctx context.Context, orgID, facilityID, blockID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		f, err := loadFacility(ctx, tx, orgID, facilityID, true)
		if err != nil {
			return err
		}
		if _, err := requireAt(ctx, tx, PermFacilitiesManage, f.SiteID); err != nil {
			return err
		}
		b, err := scanBlock(tx.QueryRow(ctx, `DELETE FROM facility_block WHERE org_id = $1 AND facility_id = $2 AND id = $3 RETURNING `+blockColumns, orgID, facilityID, blockID))
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such block")
		}
		if err != nil {
			return err
		}
		diff, err := audit.Diff("facility_block", blockAudit{b.Start, b.End, b.Reason}, blockAudit{})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &f.SiteID, Action: "facility.unblock", TargetType: "facility_block", TargetID: b.ID, Diff: diff})
	})
	return tenancy.DBProblem(err, "block")
}

// ListFacilityBlocks lists blocked ranges overlapping [from, to] (bookings.read or facilities.manage).
func (s *Service) ListFacilityBlocks(ctx context.Context, orgID, facilityID uuid.UUID, from, to *time.Time, p tenancy.PageParams) ([]FacilityBlock, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []FacilityBlock
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		f, err := loadFacility(ctx, tx, orgID, facilityID, false)
		if err != nil {
			return err
		}
		sc, err := siteScope(ctx, tx, f.SiteID)
		if err != nil {
			return err
		}
		if err := requireAny(ctx, sc, PermBookingsRead, PermFacilitiesManage); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+blockColumns+` FROM facility_block WHERE org_id = $1 AND facility_id = $2 AND id > $3
			AND ($4::timestamptz IS NULL OR end_at >= $4) AND ($5::timestamptz IS NULL OR start_at <= $5) ORDER BY id LIMIT $6`,
			orgID, facilityID, after, from, to, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanBlock(rows)
			if err != nil {
				return err
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list blocks")
	}
	items, next := tenancy.Page(out, limit, func(b FacilityBlock) uuid.UUID { return b.ID })
	return items, next, nil
}

// TimeRange is a range of time.
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Availability is what a resident sees of a facility: ranges, never names.
type Availability struct {
	FacilityID uuid.UUID   `json:"facility_id"`
	From       time.Time   `json:"from"`
	To         time.Time   `json:"to"`
	Taken      []TimeRange `json:"taken"`
	Blocked    []TimeRange `json:"blocked"`
}

// maxAvailabilityRange bounds one availability query.
const maxAvailabilityRange = 62 * 24 * time.Hour

// FacilityAvailability returns the taken and blocked ranges overlapping [from, to] (bookings.create).
func (s *Service) FacilityAvailability(ctx context.Context, orgID, facilityID uuid.UUID, from, to time.Time) (Availability, error) {
	if to.Before(from) {
		return Availability{}, validation("to", "end_before_start")
	}
	if to.Sub(from) > maxAvailabilityRange {
		return Availability{}, problem.Validation(problem.Field{Field: "to", Code: "too_long", Params: map[string]any{"max_days": 62}})
	}
	out := Availability{FacilityID: facilityID, From: from.UTC(), To: to.UTC(), Taken: []TimeRange{}, Blocked: []TimeRange{}}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		f, err := loadFacility(ctx, tx, orgID, facilityID, false)
		if err != nil {
			return err
		}
		sc, err := siteScope(ctx, tx, f.SiteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermBookingsCreate, sc); err != nil {
			return err
		}
		for _, q := range []struct {
			sql string
			dst *[]TimeRange
		}{
			{`SELECT start_at, end_at FROM booking WHERE org_id = $1 AND facility_id = $2 AND status = 'confirmed' AND end_at >= $3 AND start_at <= $4 ORDER BY start_at`, &out.Taken},
			{`SELECT start_at, end_at FROM facility_block WHERE org_id = $1 AND facility_id = $2 AND end_at >= $3 AND start_at <= $4 ORDER BY start_at`, &out.Blocked},
		} {
			rows, err := tx.Query(ctx, q.sql, orgID, facilityID, from, to)
			if err != nil {
				return err
			}
			for rows.Next() {
				var r TimeRange
				if err := rows.Scan(&r.Start, &r.End); err != nil {
					rows.Close()
					return err
				}
				r.Start, r.End = r.Start.UTC(), r.End.UTC()
				*q.dst = append(*q.dst, r)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Availability{}, tenancy.DBProblem(err, "availability")
	}
	return out, nil
}
