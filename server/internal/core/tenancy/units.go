package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Payer sides a charge type can be assigned to.
const (
	SideOwnerPays  = "owner"
	SideTenantPays = "tenant"
)

func payerJSON(m map[string]string) []byte {
	if m == nil {
		m = map[string]string{}
	}
	b, _ := json.Marshal(m)
	return b
}

// EffectivePayer returns which side pays a charge type for a unit: the unit's
// own override, else the site's `payer_defaults` setting (charge type to
// owner|tenant), else defaultSide. The money module calls it per unit and
// charge type; with no tenant on the date it should still bill the owner side.
func EffectivePayer(u Unit, site Site, chargeType, defaultSide string) string {
	if v, ok := u.PayerOverrides[chargeType]; ok {
		return v
	}
	if m, ok := site.Settings[SettingPayerDefaults].(map[string]any); ok {
		if v, ok := m[chargeType].(string); ok && (v == SideOwnerPays || v == SideTenantPays) {
			return v
		}
	}
	return defaultSide
}

// Unit types.
var UnitTypes = []string{"flat", "shop", "office", "parking", "storage", "common", "other"}

// Unit is a unit as the API shows it. Vacant and Independent are derived.
type Unit struct {
	ID                   uuid.UUID  `json:"id"`
	SiteID               uuid.UUID  `json:"site_id"`
	BlockID              uuid.UUID  `json:"block_id"`
	BlockName            string     `json:"block_name"`
	DoorNumber           string     `json:"door_number"`
	LegalRef             *string    `json:"legal_ref"`
	Type                 string     `json:"type"`
	AreaM2               *float64   `json:"area_m2"`
	ParentUnitID         *uuid.UUID `json:"parent_unit_id"`
	AppurtenanceOverride *string    `json:"appurtenance_override"`
	// PayerOverrides maps a charge type to the side that pays it for this unit, overriding the site default (see EffectivePayer).
	PayerOverrides map[string]string `json:"payer_overrides"`
	Status         string            `json:"status"`
	// Independent is whether the unit has dues and a statement of its own:
	// flats, shops and offices always do; parking and storage follow the
	// per-unit override, else the site default (S2).
	Independent bool `json:"independent"`
	// Vacant is derived: no current occupancy lives in the unit.
	Vacant bool `json:"vacant"`
	// OccOwners, OccTenants and OccHousehold count the current occupancies by kind.
	OccOwners    int       `json:"occ_owners"`
	OccTenants   int       `json:"occ_tenants"`
	OccHousehold int       `json:"occ_household"`
	Tags         []string  `json:"tags"`
	Version      int       `json:"version"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type unitAudit struct {
	BlockID              uuid.UUID         `json:"block_id"`
	DoorNumber           string            `json:"door_number"`
	LegalRef             *string           `json:"legal_ref"`
	Type                 string            `json:"type"`
	AreaM2               *float64          `json:"area_m2"`
	ParentUnitID         *uuid.UUID        `json:"parent_unit_id"`
	AppurtenanceOverride *string           `json:"appurtenance_override"`
	PayerOverrides       map[string]string `json:"payer_overrides"`
}

func (u Unit) audit() unitAudit {
	return unitAudit{u.BlockID, u.DoorNumber, u.LegalRef, u.Type, u.AreaM2, u.ParentUnitID, u.AppurtenanceOverride, u.PayerOverrides}
}

const unitSelect = `
	SELECT u.id, u.site_id, u.block_id, b.name, u.door_number, u.legal_ref, u.type, u.area_m2::float8, u.parent_unit_id,
	       u.appurtenance_override, u.status,
	       NOT EXISTS (SELECT 1 FROM occupancy o WHERE o.org_id = u.org_id AND o.unit_id = u.id AND o.resides
	                   AND o.valid_from <= (now() AT TIME ZONE s.timezone)::date
	                   AND (o.valid_to IS NULL OR o.valid_to > (now() AT TIME ZONE s.timezone)::date)),
	       COALESCE((SELECT array_agg(t.tag ORDER BY t.tag) FROM unit_tag t WHERE t.org_id = u.org_id AND t.unit_id = u.id), '{}'),
	       u.version, u.created_at, u.updated_at, s.settings->>'appurtenance_default', u.payer_overrides,
	       (SELECT count(*) FROM occupancy o WHERE o.org_id = u.org_id AND o.unit_id = u.id AND o.kind = 'owner'
	                   AND o.valid_from <= (now() AT TIME ZONE s.timezone)::date AND (o.valid_to IS NULL OR o.valid_to > (now() AT TIME ZONE s.timezone)::date))::int,
	       (SELECT count(*) FROM occupancy o WHERE o.org_id = u.org_id AND o.unit_id = u.id AND o.kind = 'tenant'
	                   AND o.valid_from <= (now() AT TIME ZONE s.timezone)::date AND (o.valid_to IS NULL OR o.valid_to > (now() AT TIME ZONE s.timezone)::date))::int,
	       (SELECT count(*) FROM occupancy o WHERE o.org_id = u.org_id AND o.unit_id = u.id AND o.kind = 'household'
	                   AND o.valid_from <= (now() AT TIME ZONE s.timezone)::date AND (o.valid_to IS NULL OR o.valid_to > (now() AT TIME ZONE s.timezone)::date))::int
	FROM unit u
	JOIN block b ON b.org_id = u.org_id AND b.id = u.block_id
	JOIN site s ON s.org_id = u.org_id AND s.id = u.site_id`

func scanUnit(row pgx.Row) (Unit, error) {
	var u Unit
	var siteDefault *string
	var payers []byte
	err := row.Scan(&u.ID, &u.SiteID, &u.BlockID, &u.BlockName, &u.DoorNumber, &u.LegalRef, &u.Type, &u.AreaM2, &u.ParentUnitID,
		&u.AppurtenanceOverride, &u.Status, &u.Vacant, &u.Tags, &u.Version, &u.CreatedAt, &u.UpdatedAt, &siteDefault, &payers,
		&u.OccOwners, &u.OccTenants, &u.OccHousehold)
	if err != nil {
		return Unit{}, err
	}
	u.CreatedAt, u.UpdatedAt = u.CreatedAt.UTC(), u.UpdatedAt.UTC()
	u.PayerOverrides = map[string]string{}
	_ = json.Unmarshal(payers, &u.PayerOverrides)
	mode := Appurtenance
	if siteDefault != nil && *siteDefault == Independent {
		mode = Independent
	}
	if u.AppurtenanceOverride != nil {
		mode = *u.AppurtenanceOverride
	}
	u.Independent = (u.Type != "parking" && u.Type != "storage") || mode == Independent
	return u, nil
}

// block-restricted visibility of a unit permission within a site.
// sc is the site's scope derived from its row.
func visibleBlocks(ctx context.Context, perm authz.Permission, sc authz.Scope) (all bool, blocks []uuid.UUID, err error) {
	if authz.Has(ctx, perm, sc) {
		return true, nil, nil
	}
	_, _, blocks = authz.GrantsFromContext(ctx).ScopeIDs(perm)
	if len(blocks) == 0 {
		return false, nil, problem.Forbidden("you do not have the " + string(perm) + " permission here")
	}
	return false, blocks, nil
}

// UnitFilter narrows ListUnits.
type UnitFilter struct {
	BlockID *uuid.UUID
	Type    string
	Query   string
	Vacant  *bool
	Parent  *uuid.UUID
	// Status filters by unit status (active, merged, split); empty: all.
	Status string
}

// ListUnits lists a site's units (all of them, or the permitted blocks'). It
// needs unit.read for the site or for some of its blocks.
func (s *Service) ListUnits(ctx context.Context, orgID, siteID uuid.UUID, f UnitFilter, p PageParams) ([]Unit, string, error) {
	if err := authz.RequireAnywhere(ctx, PermUnitRead); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	if f.Type != "" && !slices.Contains(UnitTypes, f.Type) {
		return nil, "", problem.Validation(problem.Field{Field: "type", Code: "invalid_value"})
	}
	var out []Unit
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := authz.ScopeOfSite(ctx, tx, siteID)
		if err != nil {
			return err
		}
		all, blocks, err := visibleBlocks(ctx, PermUnitRead, sc)
		if err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, siteID, false, false); err != nil {
			return err
		}
		q := unitSelect + ` WHERE u.org_id = $1 AND u.site_id = $2 AND u.id > $3 AND ($4 OR u.block_id = ANY($5))
			AND ($6::uuid IS NULL OR u.block_id = $6) AND ($7 = '' OR u.type = $7) AND ($8 = '' OR u.door_number ILIKE '%' || $8 || '%' OR u.legal_ref ILIKE '%' || $8 || '%')
			AND ($9::uuid IS NULL OR u.parent_unit_id = $9) AND ($11 = '' OR u.status = $11)`
		rows, err := tx.Query(ctx, q+` ORDER BY u.id LIMIT $10`, orgID, siteID, after, all, blocks, f.BlockID, f.Type, escapeLike(f.Query), f.Parent, limit*4+1, f.Status)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			u, err := scanUnit(rows)
			if err != nil {
				return err
			}
			if f.Vacant != nil && u.Vacant != *f.Vacant {
				continue
			}
			out = append(out, u)
			if len(out) == limit+1 {
				break
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", DBProblem(err, "list units")
	}
	items, next := Page(out, limit, func(u Unit) uuid.UUID { return u.ID })
	return items, next, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// GetUnitRow reads one unit inside tx (no authorization; callers did it).
func GetUnitRow(ctx context.Context, tx pgx.Tx, orgID, unitID uuid.UUID) (Unit, error) {
	u, err := scanUnit(tx.QueryRow(ctx, unitSelect+` WHERE u.org_id = $1 AND u.id = $2`, orgID, unitID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Unit{}, problem.NotFound("no such unit")
	}
	return u, err
}

// GetUnit returns a unit. It needs unit.read for the unit's block.
func (s *Service) GetUnit(ctx context.Context, orgID, unitID uuid.UUID) (Unit, error) {
	if err := authz.RequireAnywhere(ctx, PermUnitRead); err != nil {
		return Unit{}, err
	}
	var out Unit
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		if out, err = GetUnitRow(ctx, tx, orgID, unitID); err != nil {
			return err
		}
		if err := requireBlockErr(ctx, tx, PermUnitRead, out.BlockID); err != nil {
			return err
		}
		_, err = LoadSite(ctx, tx, orgID, out.SiteID, false, false)
		return err
	})
	if err != nil {
		return Unit{}, DBProblem(err, "get unit")
	}
	return out, nil
}

var unitFields = []string{"block_id", "door_number", "legal_ref", "type", "area_m2", "parent_unit_id", "appurtenance_override", "payer_overrides"}

// applyUnit validates d over base. It checks shape only; references are
// checked against the database by checkUnitRefs.
func applyUnit(base Unit, d Doc, create bool) (Unit, error) {
	fields := d.Unknown(unitFields...)
	u := base
	if d.Has("door_number") {
		var v string
		if err := d.Get("door_number", &v); err != nil {
			return u, err
		}
		v = strings.TrimSpace(v)
		if n := len([]rune(v)); n < 1 || n > 40 || HasFormulaPrefix(v) {
			fields = append(fields, problem.Field{Field: "door_number", Code: "invalid_value"})
		}
		u.DoorNumber = v
	} else if create {
		fields = append(fields, problem.Field{Field: "door_number", Code: "required"})
	}
	if d.Has("legal_ref") {
		v, err := d.String("legal_ref")
		if err != nil {
			return u, err
		}
		u.LegalRef = v
	}
	if d.Has("type") {
		var v string
		if err := d.Get("type", &v); err != nil {
			return u, err
		}
		if !slices.Contains(UnitTypes, v) {
			fields = append(fields, problem.Field{Field: "type", Code: "invalid_value"})
		}
		u.Type = v
	}
	if d.Has("area_m2") {
		if d.IsNull("area_m2") {
			u.AreaM2 = nil
		} else {
			var v float64
			if err := d.Get("area_m2", &v); err != nil {
				return u, err
			}
			if v <= 0 || v > 1e6 {
				fields = append(fields, problem.Field{Field: "area_m2", Code: "invalid_value"})
			}
			u.AreaM2 = &v
		}
	}
	if d.Has("block_id") {
		if err := d.Get("block_id", &u.BlockID); err != nil {
			return u, err
		}
	}
	if d.Has("parent_unit_id") {
		if d.IsNull("parent_unit_id") {
			u.ParentUnitID = nil
		} else {
			var v uuid.UUID
			if err := d.Get("parent_unit_id", &v); err != nil {
				return u, err
			}
			u.ParentUnitID = &v
		}
	}
	if d.Has("payer_overrides") {
		var m map[string]string
		if err := d.Get("payer_overrides", &m); err != nil {
			return u, err
		}
		for k, v := range m {
			if !codeRE.MatchString(k) || (v != SideOwnerPays && v != SideTenantPays) {
				fields = append(fields, problem.Field{Field: "payer_overrides." + k, Code: "invalid_value"})
			}
		}
		if m == nil {
			m = map[string]string{}
		}
		u.PayerOverrides = m
	}
	if d.Has("appurtenance_override") {
		v, err := d.String("appurtenance_override")
		if err != nil {
			return u, err
		}
		if v != nil && *v != Appurtenance && *v != Independent {
			fields = append(fields, problem.Field{Field: "appurtenance_override", Code: "invalid_value"})
		}
		u.AppurtenanceOverride = v
	}
	if len(fields) > 0 {
		return u, problem.Validation(fields...)
	}
	return u, nil
}

// checkUnitRefs verifies the appurtenance parent: a unit of the same site that
// is not itself an appurtenance, and only parking, storage and other units may
// have one.
func checkUnitRefs(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, u Unit) error {
	if u.ParentUnitID == nil {
		return nil
	}
	if u.Type == "flat" || u.Type == "shop" || u.Type == "office" || u.Type == "common" {
		return problem.Validation(problem.Field{Field: "parent_unit_id", Code: "invalid_value", Params: map[string]any{"reason": "type_cannot_be_appurtenance"}})
	}
	var parentSite uuid.UUID
	var parentOfParent *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT site_id, parent_unit_id FROM unit WHERE org_id = $1 AND id = $2`, orgID, *u.ParentUnitID).Scan(&parentSite, &parentOfParent)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && parentSite != u.SiteID) {
		return problem.Validation(problem.Field{Field: "parent_unit_id", Code: "invalid_value"})
	}
	if err != nil {
		return err
	}
	if parentOfParent != nil || *u.ParentUnitID == u.ID {
		return problem.Validation(problem.Field{Field: "parent_unit_id", Code: "invalid_value", Params: map[string]any{"reason": "no_chains"}})
	}
	var children int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM unit WHERE org_id = $1 AND parent_unit_id = $2`, orgID, u.ID).Scan(&children); err != nil {
		return err
	}
	if children > 0 {
		return problem.Validation(problem.Field{Field: "parent_unit_id", Code: "invalid_value", Params: map[string]any{"reason": "no_chains"}})
	}
	return nil
}

// CreateUnit adds a unit to a site. It needs unit.manage for the block.
func (s *Service) CreateUnit(ctx context.Context, orgID, siteID uuid.UUID, d Doc) (Unit, error) {
	if err := authz.RequireAnywhere(ctx, PermUnitManage); err != nil {
		return Unit{}, err
	}
	var out Unit
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := authz.ScopeOfSite(ctx, tx, siteID); err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, siteID, false, false); err != nil {
			return err
		}
		u, err := applyUnit(Unit{ID: id.New(), SiteID: siteID, Type: "flat"}, d, true)
		if err != nil {
			return err
		}
		if u.BlockID == uuid.Nil {
			var blocks []uuid.UUID
			rows, err := tx.Query(ctx, `SELECT id FROM block WHERE org_id = $1 AND site_id = $2 LIMIT 2`, orgID, siteID)
			if err != nil {
				return err
			}
			for rows.Next() {
				var b uuid.UUID
				if err := rows.Scan(&b); err != nil {
					rows.Close()
					return err
				}
				blocks = append(blocks, b)
			}
			rows.Close()
			if len(blocks) != 1 {
				return problem.Validation(problem.Field{Field: "block_id", Code: "required"})
			}
			u.BlockID = blocks[0]
		}
		if err := requireBlockOfSite(ctx, tx, PermUnitManage, u.BlockID, siteID); err != nil {
			return err
		}
		if err := checkUnitRefs(ctx, tx, orgID, u); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO unit (id, org_id, site_id, block_id, door_number, legal_ref, type, area_m2, parent_unit_id, appurtenance_override, payer_overrides)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, u.ID, orgID, siteID, u.BlockID, u.DoorNumber, u.LegalRef, u.Type, u.AreaM2, u.ParentUnitID, u.AppurtenanceOverride, payerJSON(u.PayerOverrides)); err != nil {
			return err
		}
		if out, err = GetUnitRow(ctx, tx, orgID, u.ID); err != nil {
			return err
		}
		diff, err := audit.Diff("unit", nil, out.audit())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "unit.create", TargetType: "unit", TargetID: out.ID, Diff: diff})
	})
	if err != nil {
		return Unit{}, DBProblem(err, "unit")
	}
	return out, nil
}

// UpdateUnit applies d if the unit is still at expectedVersion. It needs
// unit.manage for the unit's block (and for the target block when moved).
func (s *Service) UpdateUnit(ctx context.Context, orgID, unitID uuid.UUID, expectedVersion int, d Doc) (Unit, error) {
	if err := authz.RequireAnywhere(ctx, PermUnitManage); err != nil {
		return Unit{}, err
	}
	var out Unit
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var before Unit
		var err error
		if err = tx.QueryRow(ctx, `SELECT id FROM unit WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, unitID).Scan(&unitID); err != nil {
			return err
		}
		if before, err = GetUnitRow(ctx, tx, orgID, unitID); err != nil {
			return err
		}
		if err := requireBlockErr(ctx, tx, PermUnitManage, before.BlockID); err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, before.SiteID, false, false); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return ErrVersion
		}
		next, err := applyUnit(before, d, false)
		if err != nil {
			return err
		}
		if next.BlockID != before.BlockID {
			if err := requireBlockOfSite(ctx, tx, PermUnitManage, next.BlockID, before.SiteID); err != nil {
				return err
			}
		}
		if err := checkUnitRefs(ctx, tx, orgID, next); err != nil {
			return err
		}
		diff, err := audit.Diff("unit", before.audit(), next.audit())
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			out = before
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE unit SET block_id = $3, door_number = $4, legal_ref = $5, type = $6, area_m2 = $7, parent_unit_id = $8,
			appurtenance_override = $9, payer_overrides = $10, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			orgID, unitID, next.BlockID, next.DoorNumber, next.LegalRef, next.Type, next.AreaM2, next.ParentUnitID, next.AppurtenanceOverride, payerJSON(next.PayerOverrides)); err != nil {
			return err
		}
		if out, err = GetUnitRow(ctx, tx, orgID, unitID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &before.SiteID, Action: "unit.update", TargetType: "unit", TargetID: unitID, Diff: diff})
	})
	if errors.Is(err, ErrVersion) {
		return Unit{}, VersionConflict("unit")
	}
	if err != nil {
		return Unit{}, DBProblem(err, "update unit")
	}
	return out, nil
}

// SetUnitTags replaces a unit's tags. It needs unit.manage for the unit's block.
func (s *Service) SetUnitTags(ctx context.Context, orgID, unitID uuid.UUID, expectedVersion int, tags []string) (Unit, error) {
	if err := authz.RequireAnywhere(ctx, PermUnitManage); err != nil {
		return Unit{}, err
	}
	set := map[string]bool{}
	var norm []string
	for i, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if !tagRE.MatchString(t) {
			return Unit{}, problem.Validation(problem.Field{Field: fmt.Sprintf("tags.%d", i), Code: "invalid_format"})
		}
		if !set[t] {
			set[t] = true
			norm = append(norm, t)
		}
	}
	if len(norm) > 20 {
		return Unit{}, problem.Validation(problem.Field{Field: "tags", Code: "too_long", Params: map[string]any{"max": 20}})
	}
	slices.Sort(norm)
	var out Unit
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := GetUnitRow(ctx, tx, orgID, unitID)
		if err != nil {
			return err
		}
		if err := requireBlockErr(ctx, tx, PermUnitManage, before.BlockID); err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, before.SiteID, false, false); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return ErrVersion
		}
		if _, err := tx.Exec(ctx, `DELETE FROM unit_tag WHERE org_id = $1 AND unit_id = $2 AND NOT (tag = ANY($3))`, orgID, unitID, norm); err != nil {
			return err
		}
		for _, t := range norm {
			if _, err := tx.Exec(ctx, `INSERT INTO unit_tag (id, org_id, site_id, unit_id, tag) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (org_id, unit_id, tag) DO NOTHING`,
				id.New(), orgID, before.SiteID, unitID, t); err != nil {
				return err
			}
		}
		if out, err = GetUnitRow(ctx, tx, orgID, unitID); err != nil {
			return err
		}
		if slices.Equal(before.Tags, out.Tags) {
			return nil
		}
		diff, err := audit.Diff("unit", map[string]any{"tags": before.Tags}, map[string]any{"tags": out.Tags})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &before.SiteID, Action: "unit.set_tags", TargetType: "unit", TargetID: unitID, Diff: diff})
	})
	if err != nil {
		return Unit{}, DBProblem(err, "unit tags")
	}
	return out, nil
}
