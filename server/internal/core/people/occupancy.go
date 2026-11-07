package people

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

const dateLayout = "2006-01-02"

// Occupancy kinds and sides as stored.
const (
	KindOwner     = "owner"
	KindTenant    = "tenant"
	KindHousehold = "household"
	SideOwner     = "owner"
	SideTenant    = "tenant"
)

// Occupancy ties a person to a unit side for a period (S3).
type Occupancy struct {
	ID              uuid.UUID `json:"id"`
	SiteID          uuid.UUID `json:"site_id"`
	UnitID          uuid.UUID `json:"unit_id"`
	DoorNumber      string    `json:"door_number"`
	PersonID        uuid.UUID `json:"person_id"`
	PersonName      string    `json:"person_name"`
	Kind            string    `json:"kind"`
	Side            string    `json:"side"`
	SharePct        *float64  `json:"share_pct"`
	IsCorrespondent bool      `json:"is_correspondent"`
	MayMoney        bool      `json:"may_see_money"`
	Resides         bool      `json:"resides"`
	ValidFrom       string    `json:"valid_from"`
	ValidTo         *string   `json:"valid_to"`
	Current         bool      `json:"current"`
	Version         int       `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	blockID         uuid.UUID
}

type occAudit struct {
	PersonID        uuid.UUID `json:"person_id"`
	Kind            string    `json:"kind"`
	Side            string    `json:"side"`
	SharePct        *float64  `json:"share_pct"`
	IsCorrespondent bool      `json:"is_correspondent"`
	MayMoney        bool      `json:"may_see_money"`
	Resides         bool      `json:"resides"`
	ValidFrom       string    `json:"valid_from"`
	ValidTo         *string   `json:"valid_to"`
}

func (o Occupancy) audit() occAudit {
	return occAudit{o.PersonID, o.Kind, o.Side, o.SharePct, o.IsCorrespondent, o.MayMoney, o.Resides, o.ValidFrom, o.ValidTo}
}

const occSelect = `
	SELECT o.id, o.site_id, o.unit_id, u.door_number, o.person_id, p.display_name, o.kind, o.side, o.share_pct::float8,
	       o.is_correspondent, o.may_see_money, o.resides, to_char(o.valid_from, 'YYYY-MM-DD'), to_char(o.valid_to, 'YYYY-MM-DD'),
	       o.valid_from <= (now() AT TIME ZONE s.timezone)::date AND (o.valid_to IS NULL OR o.valid_to > (now() AT TIME ZONE s.timezone)::date),
	       o.version, o.created_at, o.updated_at, u.block_id
	FROM occupancy o
	JOIN unit u ON u.org_id = o.org_id AND u.id = o.unit_id
	JOIN person p ON p.org_id = o.org_id AND p.id = o.person_id
	JOIN site s ON s.org_id = o.org_id AND s.id = o.site_id`

func scanOcc(row pgx.Row) (Occupancy, error) {
	var o Occupancy
	err := row.Scan(&o.ID, &o.SiteID, &o.UnitID, &o.DoorNumber, &o.PersonID, &o.PersonName, &o.Kind, &o.Side, &o.SharePct,
		&o.IsCorrespondent, &o.MayMoney, &o.Resides, &o.ValidFrom, &o.ValidTo, &o.Current, &o.Version, &o.CreatedAt, &o.UpdatedAt, &o.blockID)
	o.CreatedAt, o.UpdatedAt = o.CreatedAt.UTC(), o.UpdatedAt.UTC()
	return o, err
}

func loadOcc(ctx context.Context, tx pgx.Tx, orgID, occID uuid.UUID, forUpdate bool) (Occupancy, error) {
	q := occSelect + ` WHERE o.org_id = $1 AND o.id = $2`
	if forUpdate {
		q += ` FOR UPDATE OF o`
	}
	o, err := scanOcc(tx.QueryRow(ctx, q, orgID, occID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Occupancy{}, problem.NotFound("no such occupancy")
	}
	return o, err
}

func (o Occupancy) scope(ctx context.Context, tx pgx.Tx) (authz.Scope, error) {
	return authz.ScopeOfBlock(ctx, tx, o.blockID)
}

// OccFilter narrows ListOccupancies.
type OccFilter struct {
	SiteID, UnitID, PersonID *uuid.UUID
	CurrentOnly              bool
}

// ListOccupancies lists occupancies, newest id last. It needs occupancy.read
// for the sites or blocks the units are in.
func (s *Service) ListOccupancies(ctx context.Context, orgID uuid.UUID, f OccFilter, p tenancy.PageParams) ([]Occupancy, string, error) {
	if err := authz.RequireAnywhere(ctx, PermOccupancyRead); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	orgWide, sites, blocks := authz.GrantsFromContext(ctx).ScopeIDs(PermOccupancyRead)
	var out []Occupancy
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, occSelect+` WHERE o.org_id = $1 AND o.id > $2 AND ($3 OR o.site_id = ANY($4) OR u.block_id = ANY($5))
			AND ($6::uuid IS NULL OR o.site_id = $6) AND ($7::uuid IS NULL OR o.unit_id = $7) AND ($8::uuid IS NULL OR o.person_id = $8)
			AND (NOT $9 OR (o.valid_from <= (now() AT TIME ZONE s.timezone)::date AND (o.valid_to IS NULL OR o.valid_to > (now() AT TIME ZONE s.timezone)::date)))
			AND s.status = 'active' ORDER BY o.id LIMIT $10`,
			orgID, after, orgWide, sites, blocks, f.SiteID, f.UnitID, f.PersonID, f.CurrentOnly, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			o, err := scanOcc(rows)
			if err != nil {
				return err
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list occupancies")
	}
	items, next := tenancy.Page(out, limit, func(o Occupancy) uuid.UUID { return o.ID })
	return items, next, nil
}

// occInput is a validated occupancy to insert.
type occInput struct {
	unitID, personID uuid.UUID
	kind, side       string
	sharePct         *float64
	correspondent    bool
	resides          bool
	from             time.Time
	to               *time.Time
}

func parseDate(d Doc, key string, def *time.Time) (*time.Time, error) {
	if !d.Has(key) {
		return def, nil
	}
	if d.IsNull(key) {
		return nil, nil
	}
	var v string
	if err := d.Get(key, &v); err != nil {
		return nil, err
	}
	t, err := time.Parse(dateLayout, v)
	if err != nil {
		return nil, fieldProblem(key, "invalid_format")
	}
	return &t, nil
}

var occFields = []string{"person_id", "kind", "side", "share_pct", "is_correspondent", "resides", "valid_from", "valid_to"}

// insertOccTx inserts an occupancy after enforcing the unit rules (shares of
// co-owners sum to at most 100, one correspondent per side, no duplicate).
// It locks the unit row so concurrent changes serialize. The unit must belong
// to siteID (row read by the caller under RLS).
func (s *Service) insertOccTx(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, unit tenancy.Unit, in occInput) (Occupancy, error) {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM unit WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, unit.ID); err != nil {
		return Occupancy{}, err
	}
	if in.to != nil && !in.to.After(in.from) {
		return Occupancy{}, fieldProblem("valid_to", "invalid_value")
	}
	var dup bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM occupancy WHERE org_id = $1 AND unit_id = $2 AND person_id = $3 AND kind = $4
		AND (valid_to IS NULL OR valid_to > $5) AND ($6::date IS NULL OR valid_from < $6))`, orgID, unit.ID, in.personID, in.kind, in.from, in.to).Scan(&dup); err != nil {
		return Occupancy{}, err
	}
	if dup {
		return Occupancy{}, problem.Conflict("this person already has an overlapping " + in.kind + " occupancy on this unit")
	}
	if in.kind == KindOwner && in.sharePct != nil {
		var used float64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(share_pct), 0)::float8 FROM occupancy WHERE org_id = $1 AND unit_id = $2 AND kind = 'owner'
			AND (valid_to IS NULL OR valid_to > $3) AND ($4::date IS NULL OR valid_from < $4)`, orgID, unit.ID, in.from, in.to).Scan(&used); err != nil {
			return Occupancy{}, err
		}
		if used+*in.sharePct > 100.0001 {
			return Occupancy{}, problem.Validation(problem.Field{Field: "share_pct", Code: "too_large", Params: map[string]any{"max": 100 - used}})
		}
	}
	if in.correspondent {
		if _, err := tx.Exec(ctx, `UPDATE occupancy SET is_correspondent = false, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND unit_id = $2 AND side = $3 AND is_correspondent AND valid_to IS NULL`, orgID, unit.ID, in.side); err != nil {
			return Occupancy{}, err
		}
	}
	oid := id.New()
	if _, err := tx.Exec(ctx, `INSERT INTO occupancy (id, org_id, site_id, unit_id, person_id, kind, side, share_pct, is_correspondent, resides, valid_from, valid_to)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		oid, orgID, unit.SiteID, unit.ID, in.personID, in.kind, in.side, in.sharePct, in.correspondent, in.resides, in.from, in.to); err != nil {
		return Occupancy{}, err
	}
	o, err := loadOcc(ctx, tx, orgID, oid, false)
	if err != nil {
		return Occupancy{}, err
	}
	diff, err := audit.Diff("occupancy", nil, o.audit())
	if err != nil {
		return Occupancy{}, err
	}
	if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &unit.SiteID, Action: "occupancy.create", TargetType: "occupancy", TargetID: oid, Diff: diff}); err != nil {
		return Occupancy{}, err
	}
	return o, nil
}

// defaultSide picks the side of a household member: the tenant's when the
// unit has a tenant on the date, else the owner's.
func defaultSide(ctx context.Context, tx pgx.Tx, orgID, unitID uuid.UUID, on time.Time) (string, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM occupancy WHERE org_id = $1 AND unit_id = $2 AND kind = 'tenant'
		AND valid_from <= $3 AND (valid_to IS NULL OR valid_to > $3)`, orgID, unitID, on).Scan(&n)
	if n > 0 {
		return SideTenant, err
	}
	return SideOwner, err
}

// CreateOccupancy adds an occupancy to a unit. It needs occupancy.manage for
// the unit. Household members get no money access (grant it afterwards); a
// correspondent replaces the unit side's previous one.
func (s *Service) CreateOccupancy(ctx context.Context, orgID, unitID uuid.UUID, d Doc) (Occupancy, error) {
	if err := authz.RequireAnywhere(ctx, PermOccupancyManage); err != nil {
		return Occupancy{}, err
	}
	var out Occupancy
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		unit, err := tenancy.GetUnitRow(ctx, tx, orgID, unitID)
		if err != nil {
			return err
		}
		if err := requireBlockErr(ctx, tx, PermOccupancyManage, unit.BlockID); err != nil {
			return err
		}
		if _, err := tenancy.LoadSite(ctx, tx, orgID, unit.SiteID, false, false); err != nil {
			return err
		}
		in, err := parseOcc(ctx, tx, orgID, unit, d, s.today())
		if err != nil {
			return err
		}
		out, err = s.insertOccTx(ctx, tx, orgID, unit, in)
		return err
	})
	if err != nil {
		return Occupancy{}, tenancy.DBProblem(err, "occupancy")
	}
	return out, nil
}

func parseOcc(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, unit tenancy.Unit, d Doc, today time.Time) (occInput, error) {
	fields := d.Unknown(occFields...)
	in := occInput{unitID: unit.ID, resides: true}
	if !d.Has("person_id") {
		fields = append(fields, problem.Field{Field: "person_id", Code: "required"})
	} else if err := d.Get("person_id", &in.personID); err != nil {
		return in, err
	}
	var kind string
	if !d.Has("kind") {
		fields = append(fields, problem.Field{Field: "kind", Code: "required"})
	} else if err := d.Get("kind", &kind); err != nil {
		return in, err
	}
	if !slices.Contains([]string{KindOwner, KindTenant, KindHousehold}, kind) && kind != "" {
		fields = append(fields, problem.Field{Field: "kind", Code: "invalid_value"})
	}
	in.kind = kind
	from, err := parseDate(d, "valid_from", &today)
	if err != nil {
		return in, err
	}
	in.from = *from
	if in.to, err = parseDate(d, "valid_to", nil); err != nil {
		return in, err
	}
	if d.Has("share_pct") && !d.IsNull("share_pct") {
		var v float64
		if err := d.Get("share_pct", &v); err != nil {
			return in, err
		}
		if v <= 0 || v > 100 {
			fields = append(fields, problem.Field{Field: "share_pct", Code: "invalid_value"})
		}
		if kind != KindOwner {
			fields = append(fields, problem.Field{Field: "share_pct", Code: "invalid_value", Params: map[string]any{"reason": "owners_only"}})
		}
		in.sharePct = &v
	}
	if err := d.Get("is_correspondent", &in.correspondent); err != nil {
		return in, err
	}
	if kind == KindHousehold && in.correspondent {
		fields = append(fields, problem.Field{Field: "is_correspondent", Code: "invalid_value"})
	}
	if err := d.Get("resides", &in.resides); err != nil {
		return in, err
	}
	switch kind {
	case KindOwner:
		in.side = SideOwner
	case KindTenant:
		in.side = SideTenant
		in.resides = true
	case KindHousehold:
		in.resides = true
		var side string
		if d.Has("side") {
			if err := d.Get("side", &side); err != nil {
				return in, err
			}
			if side != SideOwner && side != SideTenant {
				fields = append(fields, problem.Field{Field: "side", Code: "invalid_value"})
			}
		} else if len(fields) == 0 {
			if side, err = defaultSide(ctx, tx, orgID, unit.ID, in.from); err != nil {
				return in, err
			}
		}
		in.side = side
	}
	if d.Has("side") && kind != KindHousehold {
		var side string
		if err := d.Get("side", &side); err != nil {
			return in, err
		}
		if side != in.side {
			fields = append(fields, problem.Field{Field: "side", Code: "invalid_value", Params: map[string]any{"reason": "implied_by_kind"}})
		}
	}
	if len(fields) > 0 {
		return in, problem.Validation(fields...)
	}
	return in, nil
}

// UpdateOccupancy changes the share, correspondent flag, residence flag and
// dates of an occupancy. It needs occupancy.manage for the unit.
func (s *Service) UpdateOccupancy(ctx context.Context, orgID, occID uuid.UUID, expectedVersion int, d Doc) (Occupancy, error) {
	if err := authz.RequireAnywhere(ctx, PermOccupancyManage); err != nil {
		return Occupancy{}, err
	}
	var out Occupancy
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := loadOcc(ctx, tx, orgID, occID, true)
		if err != nil {
			return err
		}
		sc0, err := before.scope(ctx, tx)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermOccupancyManage, sc0); err != nil {
			return err
		}
		if _, err := tenancy.LoadSite(ctx, tx, orgID, before.SiteID, false, false); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return tenancy.ErrVersion
		}
		fields := d.Unknown("share_pct", "is_correspondent", "resides", "valid_from", "valid_to")
		if len(fields) > 0 {
			return problem.Validation(fields...)
		}
		next := before
		if d.Has("share_pct") {
			if d.IsNull("share_pct") {
				next.SharePct = nil
			} else {
				var v float64
				if err := d.Get("share_pct", &v); err != nil {
					return err
				}
				if v <= 0 || v > 100 || before.Kind != KindOwner {
					return fieldProblem("share_pct", "invalid_value")
				}
				next.SharePct = &v
			}
		}
		if err := d.Get("is_correspondent", &next.IsCorrespondent); err != nil {
			return err
		}
		if next.IsCorrespondent && before.Kind == KindHousehold {
			return fieldProblem("is_correspondent", "invalid_value")
		}
		if err := d.Get("resides", &next.Resides); err != nil {
			return err
		}
		from, err := parseDate(d, "valid_from", nil)
		if err != nil {
			return err
		}
		if from != nil {
			next.ValidFrom = from.Format(dateLayout)
		}
		if d.Has("valid_to") {
			to, err := parseDate(d, "valid_to", nil)
			if err != nil {
				return err
			}
			next.ValidTo = nil
			if to != nil {
				v := to.Format(dateLayout)
				next.ValidTo = &v
			}
		}
		if next.ValidTo != nil && *next.ValidTo <= next.ValidFrom {
			return fieldProblem("valid_to", "invalid_value")
		}
		diff, err := audit.Diff("occupancy", before.audit(), next.audit())
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			out = before
			return nil
		}
		if next.SharePct != nil && (before.SharePct == nil || *next.SharePct != *before.SharePct) {
			var used float64
			if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(share_pct), 0)::float8 FROM occupancy WHERE org_id = $1 AND unit_id = $2 AND kind = 'owner' AND id <> $3
				AND (valid_to IS NULL OR valid_to > $4::date) AND ($5::date IS NULL OR valid_from < $5::date)`, orgID, before.UnitID, occID, next.ValidFrom, next.ValidTo).Scan(&used); err != nil {
				return err
			}
			if used+*next.SharePct > 100.0001 {
				return problem.Validation(problem.Field{Field: "share_pct", Code: "too_large", Params: map[string]any{"max": 100 - used}})
			}
		}
		if next.IsCorrespondent && !before.IsCorrespondent {
			if _, err := tx.Exec(ctx, `UPDATE occupancy SET is_correspondent = false, updated_at = now(), version = version + 1
				WHERE org_id = $1 AND unit_id = $2 AND side = $3 AND is_correspondent AND valid_to IS NULL AND id <> $4`, orgID, before.UnitID, before.Side, occID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE occupancy SET share_pct = $3, is_correspondent = $4, resides = $5, valid_from = $6::date, valid_to = $7::date,
			updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			orgID, occID, next.SharePct, next.IsCorrespondent, next.Resides, next.ValidFrom, next.ValidTo); err != nil {
			return err
		}
		if out, err = loadOcc(ctx, tx, orgID, occID, false); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &before.SiteID, Action: "occupancy.update", TargetType: "occupancy", TargetID: occID, Diff: diff}); err != nil {
			return err
		}
		if next.ValidTo != nil && (before.ValidTo == nil || *before.ValidTo != *next.ValidTo) {
			ends, perr := time.Parse(dateLayout, *next.ValidTo)
			if perr != nil {
				return perr
			}
			return s.occupancyEnded(ctx, tx, orgID, out, ends)
		}
		return nil
	})
	if errors.Is(err, tenancy.ErrVersion) {
		return Occupancy{}, tenancy.VersionConflict("occupancy")
	}
	if err != nil {
		return Occupancy{}, tenancy.DBProblem(err, "update occupancy")
	}
	return out, nil
}

// SetMoneyAccess turns money visibility on or off for a household member (S3).
// Managers may do it; so may the owner of the unit, or the tenant for the
// tenant side's household. Everyone else needs occupancy.grant_money and a
// matching current occupancy.
func (s *Service) SetMoneyAccess(ctx context.Context, orgID, occID uuid.UUID, expectedVersion int, allow bool) (Occupancy, error) {
	if err := authz.RequireAnywhere(ctx, PermMoneyAccessGrant); err != nil {
		return Occupancy{}, err
	}
	var out Occupancy
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := loadOcc(ctx, tx, orgID, occID, true)
		if err != nil {
			return err
		}
		sc, err := before.scope(ctx, tx)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermMoneyAccessGrant, sc); err != nil {
			return err
		}
		if !authz.Has(ctx, PermOccupancyManage, sc) {
			// a portal user: the owner of the unit, or the tenant for the tenant side
			own, ok := authz.CurrentOccupancy(ctx, before.UnitID)
			if !ok || (own.Kind != authz.OccOwner && (own.Kind != authz.OccTenant || before.Side != SideTenant)) {
				return problem.Forbidden("only the unit's owner or tenant can change a household member's money access")
			}
		}
		if _, err := tenancy.LoadSite(ctx, tx, orgID, before.SiteID, false, false); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return tenancy.ErrVersion
		}
		if before.Kind != KindHousehold {
			return problem.Validation(problem.Field{Field: "may_see_money", Code: "invalid_value", Params: map[string]any{"reason": "household_only"}})
		}
		if before.MayMoney == allow {
			out = before
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE occupancy SET may_see_money = $3, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, occID, allow); err != nil {
			return err
		}
		if out, err = loadOcc(ctx, tx, orgID, occID, false); err != nil {
			return err
		}
		diff, err := audit.Diff("occupancy", before.audit(), out.audit())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &before.SiteID, Action: "occupancy.set_money_access", TargetType: "occupancy", TargetID: occID, Diff: diff})
	})
	if err != nil {
		return Occupancy{}, tenancy.DBProblem(err, "money access")
	}
	return out, nil
}

// MoveOut is the record of a move-out or sale (S7). The closing statement
// and the debt certificate are placeholders the money module fills (phase C).
type MoveOut struct {
	ID                     uuid.UUID  `json:"id"`
	SiteID                 uuid.UUID  `json:"site_id"`
	OccupancyID            uuid.UUID  `json:"occupancy_id"`
	UnitID                 uuid.UUID  `json:"unit_id"`
	PersonID               uuid.UUID  `json:"person_id"`
	AsOf                   string     `json:"as_of"`
	OpenCharges            string     `json:"open_charges"`
	CarriedAmountMinor     *int64     `json:"carried_amount_minor"`
	CarriedCurrency        *string    `json:"carried_currency"`
	ClosingStatementStatus string     `json:"closing_statement_status"`
	ClosingStatementFileID *uuid.UUID `json:"closing_statement_file_id"`
	CertificateStatus      string     `json:"certificate_status"`
	CertificateFileID      *uuid.UUID `json:"certificate_file_id"`
	SuccessorOccupancyID   *uuid.UUID `json:"successor_occupancy_id"`
	DecidedBy              *uuid.UUID `json:"decided_by"`
	Notes                  *string    `json:"notes"`
	Version                int        `json:"version"`
	CreatedAt              time.Time  `json:"created_at"`
	blockID                uuid.UUID
}

const moveOutSelect = `SELECT m.id, m.site_id, m.occupancy_id, m.unit_id, m.person_id, to_char(m.as_of, 'YYYY-MM-DD'), m.open_charges, m.carried_amount_minor, m.carried_currency,
	m.closing_statement_status, m.closing_statement_file_id, m.certificate_status, m.certificate_file_id, m.successor_occupancy_id,
	m.decided_by, m.notes, m.version, m.created_at, u.block_id
	FROM move_out m JOIN unit u ON u.org_id = m.org_id AND u.id = m.unit_id`

func scanMoveOut(row pgx.Row) (MoveOut, error) {
	var m MoveOut
	err := row.Scan(&m.ID, &m.SiteID, &m.OccupancyID, &m.UnitID, &m.PersonID, &m.AsOf, &m.OpenCharges, &m.CarriedAmountMinor, &m.CarriedCurrency, &m.ClosingStatementStatus,
		&m.ClosingStatementFileID, &m.CertificateStatus, &m.CertificateFileID, &m.SuccessorOccupancyID, &m.DecidedBy, &m.Notes, &m.Version, &m.CreatedAt, &m.blockID)
	m.CreatedAt = m.CreatedAt.UTC()
	return m, err
}

// GetMoveOut returns a move-out record. It needs moveout.read for the unit.
func (s *Service) GetMoveOut(ctx context.Context, orgID, moveOutID uuid.UUID) (MoveOut, error) {
	if err := authz.RequireAnywhere(ctx, PermMoveOutRead); err != nil {
		return MoveOut{}, err
	}
	var out MoveOut
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, err := scanMoveOut(tx.QueryRow(ctx, moveOutSelect+` WHERE m.org_id = $1 AND m.id = $2`, orgID, moveOutID))
		if err != nil {
			return err
		}
		block := m.blockID
		if err := requireBlockErr(ctx, tx, PermMoveOutRead, block); err != nil {
			return err
		}
		out = m
		return nil
	})
	if err != nil {
		return MoveOut{}, tenancy.DBProblem(err, "get move-out")
	}
	return out, nil
}

// MoveOutInput is the content of a move-out request.
type MoveOutInput struct {
	AsOf        time.Time
	OpenCharges string
	Notes       *string
	// SuccessorPersonID, when set, takes over the unit side from AsOf (a
	// transfer): same kind, side and share, a new occupancy.
	SuccessorPersonID *uuid.UUID
	// EndHousehold also ends the household members of the same unit side (default true in the API).
	EndHousehold bool
}

// ParseMoveOut reads the request body of a move-out.
func ParseMoveOut(d Doc) (MoveOutInput, error) {
	fields := d.Unknown("as_of", "open_charges", "notes", "successor_person_id", "end_household")
	in := MoveOutInput{OpenCharges: "carry_to_person", EndHousehold: true}
	t, err := parseDate(d, "as_of", nil)
	if err != nil {
		return in, err
	}
	if t == nil {
		fields = append(fields, problem.Field{Field: "as_of", Code: "required"})
	} else {
		in.AsOf = *t
	}
	if d.Has("open_charges") {
		if err := d.Get("open_charges", &in.OpenCharges); err != nil {
			return in, err
		}
		if !slices.Contains([]string{"carry_to_person", "keep_on_unit", "write_off"}, in.OpenCharges) {
			fields = append(fields, problem.Field{Field: "open_charges", Code: "invalid_value"})
		}
	}
	if in.Notes, err = d.String("notes"); err != nil {
		return in, err
	}
	if d.Has("successor_person_id") && !d.IsNull("successor_person_id") {
		var v uuid.UUID
		if err := d.Get("successor_person_id", &v); err != nil {
			return in, err
		}
		in.SuccessorPersonID = &v
	}
	if err := d.Get("end_household", &in.EndHousehold); err != nil {
		return in, err
	}
	if len(fields) > 0 {
		return in, problem.Validation(fields...)
	}
	return in, nil
}

// MoveOutOccupancy ends an occupancy as of a date (the first day the person is
// no longer there), records the move-out with placeholder closing documents
// and, for a transfer, starts the successor's occupancy the same day. The
// leaving person's open charges are carried to their own receivable (carry_to_person) unless open_charges
// says otherwise (S7); no month is pro-rated. It needs moveout.manage for the unit.
func (s *Service) MoveOutOccupancy(ctx context.Context, orgID, occID uuid.UUID, in MoveOutInput) (MoveOut, error) {
	if err := authz.RequireAnywhere(ctx, PermMoveOutManage); err != nil {
		return MoveOut{}, err
	}
	var out MoveOut
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		occ, err := loadOcc(ctx, tx, orgID, occID, true)
		if err != nil {
			return err
		}
		sc1, err := occ.scope(ctx, tx)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermMoveOutManage, sc1); err != nil {
			return err
		}
		if _, err := tenancy.LoadSite(ctx, tx, orgID, occ.SiteID, false, false); err != nil {
			return err
		}
		if occ.Kind == KindHousehold {
			return problem.Validation(problem.Field{Field: "occupancy", Code: "invalid_value", Params: map[string]any{"reason": "household_members_are_ended_with_their_owner_or_tenant"}})
		}
		if occ.ValidTo != nil && *occ.ValidTo <= in.AsOf.Format(dateLayout) {
			return problem.Conflict("this occupancy already ends on or before that date")
		}
		if in.AsOf.Format(dateLayout) <= occ.ValidFrom {
			return fieldProblem("as_of", "invalid_value")
		}
		unit, err := tenancy.GetUnitRow(ctx, tx, orgID, occ.UnitID)
		if err != nil {
			return err
		}
		var uid *uuid.UUID
		if p := authz.PrincipalFromContext(ctx); p != nil {
			u := p.UserID
			uid = &u
		}
		if _, err := tx.Exec(ctx, `UPDATE occupancy SET valid_to = $3, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, occID, in.AsOf); err != nil {
			return err
		}
		ended := []Occupancy{occ}
		if in.EndHousehold {
			rows, err := tx.Query(ctx, `UPDATE occupancy SET valid_to = $4, updated_at = now(), version = version + 1
				WHERE org_id = $1 AND unit_id = $2 AND side = $3 AND kind = 'household' AND valid_from < $4 AND (valid_to IS NULL OR valid_to > $4)
				RETURNING id, person_id`,
				orgID, occ.UnitID, occ.Side, in.AsOf)
			if err != nil {
				return err
			}
			for rows.Next() {
				m := Occupancy{SiteID: occ.SiteID, UnitID: occ.UnitID, Kind: KindHousehold, Side: occ.Side}
				if err := rows.Scan(&m.ID, &m.PersonID); err != nil {
					rows.Close()
					return err
				}
				ended = append(ended, m)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		for _, e := range ended {
			if err := s.occupancyEnded(ctx, tx, orgID, e, in.AsOf); err != nil {
				return err
			}
		}
		var succ *uuid.UUID
		if in.SuccessorPersonID != nil {
			no, err := s.insertOccTx(ctx, tx, orgID, unit, occInput{
				unitID: occ.UnitID, personID: *in.SuccessorPersonID, kind: occ.Kind, side: occ.Side, sharePct: occ.SharePct,
				correspondent: occ.IsCorrespondent, resides: occ.Resides, from: in.AsOf,
			})
			if err != nil {
				return err
			}
			succ = &no.ID
		}
		mid := id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO move_out (id, org_id, site_id, occupancy_id, unit_id, person_id, as_of, open_charges, successor_occupancy_id, decided_by, notes)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			mid, orgID, occ.SiteID, occID, occ.UnitID, occ.PersonID, in.AsOf, in.OpenCharges, succ, uid, in.Notes); err != nil {
			return err
		}
		if out, err = scanMoveOut(tx.QueryRow(ctx, moveOutSelect+` WHERE m.org_id = $1 AND m.id = $2`, orgID, mid)); err != nil {
			return err
		}
		diff, err := audit.Diff("move_out", nil, map[string]any{
			"occupancy_id": occID, "as_of": out.AsOf, "open_charges": in.OpenCharges, "successor_occupancy_id": succ, "notes": in.Notes})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &occ.SiteID, Action: "occupancy.move_out", TargetType: "move_out", TargetID: mid, Diff: diff})
	})
	if err != nil {
		return MoveOut{}, tenancy.DBProblem(err, fmt.Sprintf("move-out of occupancy %s", occID))
	}
	return out, nil
}
