package community

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// VisitorEntry is a visitor: pre-registered with a QR, a walk-in the guard
// registered, or one visit of a recurring visitor. It is valid for its window
// plus 30 minutes either side, for one entry.
type VisitorEntry struct {
	ID                 uuid.UUID  `json:"id"`
	OrgID              uuid.UUID  `json:"org_id"`
	SiteID             uuid.UUID  `json:"site_id"`
	UnitID             uuid.UUID  `json:"unit_id"`
	UnitDoorNumber     string     `json:"unit_door_number"`
	Name               string     `json:"name"`
	Plate              *string    `json:"plate"`
	WindowFrom         time.Time  `json:"window_from"`
	WindowTo           time.Time  `json:"window_to"`
	ApprovalStatus     string     `json:"approval_status"`
	Source             string     `json:"source"`
	QRToken            *string    `json:"qr_token"`
	DecidedBy          *uuid.UUID `json:"decided_by"`
	DecidedAt          *time.Time `json:"decided_at"`
	ArrivedAt          *time.Time `json:"arrived_at"`
	LeftAt             *time.Time `json:"left_at"`
	Cancelled          bool       `json:"cancelled"`
	RecurringVisitorID *uuid.UUID `json:"recurring_visitor_id"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	Version            int        `json:"version"`

	sealed []byte
	hash   []byte
	// approvalExpiresAt is the expiry of the open approval request, if any (guard views).
	approvalExpiresAt *time.Time
}

// visitorAudit is what entry changes compare.
type visitorAudit struct {
	Unit       uuid.UUID `json:"unit_id"`
	Name       string    `json:"name"`
	Plate      *string   `json:"plate"`
	WindowFrom time.Time `json:"window_from"`
	WindowTo   time.Time `json:"window_to"`
	Status     string    `json:"approval_status"`
	Cancelled  bool      `json:"cancelled"`
}

func (v VisitorEntry) auditView() visitorAudit {
	return visitorAudit{v.UnitID, v.Name, v.Plate, v.WindowFrom, v.WindowTo, v.ApprovalStatus, v.Cancelled}
}

const entrySelect = `SELECT e.id, e.org_id, e.site_id, e.unit_id, u.door_number, e.name, e.plate, e.window_from, e.window_to, e.approval_status, e.source,
		e.decided_by, e.decided_at, e.arrived_at, e.left_at, e.cancelled, e.recurring_visitor_id, e.created_at, e.updated_at, e.version,
		e.qr_token_sealed, e.qr_token_hash,
		(SELECT r.expires_at FROM visitor_approval_request r WHERE r.org_id = e.org_id AND r.entry_id = e.id AND r.status = 'pending') AS approval_expires_at
	FROM visitor_entry e JOIN unit u ON u.org_id = e.org_id AND u.id = e.unit_id`

func scanEntry(row pgx.Row) (VisitorEntry, error) {
	var v VisitorEntry
	err := row.Scan(&v.ID, &v.OrgID, &v.SiteID, &v.UnitID, &v.UnitDoorNumber, &v.Name, &v.Plate, &v.WindowFrom, &v.WindowTo, &v.ApprovalStatus, &v.Source,
		&v.DecidedBy, &v.DecidedAt, &v.ArrivedAt, &v.LeftAt, &v.Cancelled, &v.RecurringVisitorID, &v.CreatedAt, &v.UpdatedAt, &v.Version,
		&v.sealed, &v.hash, &v.approvalExpiresAt)
	if err != nil {
		return v, err
	}
	v.WindowFrom, v.WindowTo, v.CreatedAt, v.UpdatedAt = v.WindowFrom.UTC(), v.WindowTo.UTC(), v.CreatedAt.UTC(), v.UpdatedAt.UTC()
	utcPtr(&v.DecidedAt)
	utcPtr(&v.ArrivedAt)
	utcPtr(&v.LeftAt)
	utcPtr(&v.approvalExpiresAt)
	return v, nil
}

func utcPtr(t **time.Time) {
	if *t != nil {
		u := (*t).UTC()
		*t = &u
	}
}

// loadEntry loads an entry of the organization; forUpdate locks it.
func loadEntry(ctx context.Context, tx pgx.Tx, orgID, entryID uuid.UUID, forUpdate bool) (VisitorEntry, error) {
	q := entrySelect + ` WHERE e.org_id = $1 AND e.id = $2`
	if forUpdate {
		q += ` FOR UPDATE OF e`
	}
	v, err := scanEntry(tx.QueryRow(ctx, q, orgID, entryID))
	if errors.Is(err, pgx.ErrNoRows) {
		return v, problem.NotFound("no such visitor entry")
	}
	return v, err
}

// visitorAccess decides what the caller may do with something of a unit:
// readers of the whole site (visitors.read: board, managers, auditors) see
// everything, residents only their own unit's (anything else is a 404, so
// units do not leak). token says whether the QR token may be shown.
func visitorAccess(ctx context.Context, sc authz.Scope, unitID uuid.UUID) (token bool, err error) {
	if authz.PrincipalFromContext(ctx) == nil {
		return false, problem.Unauthenticated("authentication is required")
	}
	if authz.Has(ctx, PermVisitorsRead, sc) {
		return managerLevel(ctx, sc) && authz.Has(ctx, PermVisitorsManage, sc), nil
	}
	if authz.Has(ctx, PermVisitorsManage, sc) && authz.CanActOnUnit(ctx, unitID) == nil {
		return true, nil
	}
	if authz.Has(ctx, PermVisitorsManage, sc) {
		return false, problem.NotFound("no such visitor")
	}
	return false, problem.Forbidden("you do not have the visitors.read permission here")
}

// requireManageUnit passes for board and managers, and for residents of the unit.
func requireManageUnit(ctx context.Context, sc authz.Scope, unitID uuid.UUID) error {
	if err := authz.Require(ctx, PermVisitorsManage, sc); err != nil {
		return err
	}
	if managerLevel(ctx, sc) {
		return nil
	}
	if authz.CanActOnUnit(ctx, unitID) != nil {
		return problem.NotFound("no such unit")
	}
	return nil
}

// withToken fills the QR token of an entry the caller may see: the plaintext
// when it was just created, else the unsealed stored one.
func (s *Service) withToken(v *VisitorEntry, show bool, plain string) {
	switch {
	case plain != "":
		v.QRToken = &plain
	case show && len(v.sealed) > 0 && v.Source == "pre_registered":
		if t := s.openQRToken(v.OrgID, v.ID, v.sealed); t != "" {
			v.QRToken = &t
		}
	}
}

func (s *Service) visitorChanged(ctx context.Context, tx pgx.Tx, siteID uuid.UUID, sourceType string, sourceID uuid.UUID) error {
	if h := s.cfg.Hooks.OnVisitorChanged; h != nil {
		return h(ctx, tx, siteID, sourceType, sourceID)
	}
	return nil
}

// CreateVisitorEntry pre-registers a visitor (visitors.manage). The response
// carries the QR text the resident shares with the guest.
func (s *Service) CreateVisitorEntry(ctx context.Context, orgID uuid.UUID, d Doc) (VisitorEntry, error) {
	if bad := d.Unknown("site_id", "unit_id", "name", "plate", "window_from", "window_to"); len(bad) > 0 {
		return VisitorEntry{}, problem.Validation(bad...)
	}
	var siteID, unitID uuid.UUID
	var from, to time.Time
	if err := d.Get("site_id", &siteID); err != nil || siteID == uuid.Nil {
		return VisitorEntry{}, validation("site_id", "required")
	}
	if err := d.Get("unit_id", &unitID); err != nil || unitID == uuid.Nil {
		return VisitorEntry{}, validation("unit_id", "required")
	}
	name, err := visitorName(d)
	if err != nil {
		return VisitorEntry{}, err
	}
	var plate *string
	if p, perr := d.String("plate"); perr != nil {
		return VisitorEntry{}, perr
	} else if p != nil {
		if plate, err = normalizePlate("plate", *p); err != nil {
			return VisitorEntry{}, err
		}
	}
	if err := d.Get("window_from", &from); err != nil || from.IsZero() {
		return VisitorEntry{}, validation("window_from", "required")
	}
	if err := d.Get("window_to", &to); err != nil || to.IsZero() {
		return VisitorEntry{}, validation("window_to", "required")
	}
	if bad := checkWindow(s.now(), from, to); len(bad) > 0 {
		return VisitorEntry{}, problem.Validation(bad...)
	}
	var out VisitorEntry
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := siteScope(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := requireManageUnit(ctx, sc, unitID); err != nil {
			return err
		}
		if _, err := loadSite(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		if _, err := loadUnit(ctx, tx, orgID, siteID, unitID, "unit_id"); err != nil {
			return err
		}
		person, _, err := callerPerson(ctx, tx, orgID)
		if err != nil {
			return err
		}
		entryID := id.New()
		qr, hash, sealed, err := s.newQRToken(orgID, entryID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO visitor_entry (id, org_id, site_id, unit_id, name, plate, window_from, window_to, approval_status, source, qr_token_hash, qr_token_sealed, registered_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pre_approved', 'pre_registered', $9, $10, $11)`,
			entryID, orgID, siteID, unitID, name, plate, from, to, hash, sealed, nilIfZero(person)); err != nil {
			return err
		}
		out, err = loadEntry(ctx, tx, orgID, entryID, false)
		if err != nil {
			return err
		}
		s.withToken(&out, true, qr)
		diff, err := audit.Diff("visitor_entry", visitorAudit{}, out.auditView())
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "visitor.pre_register", TargetType: "visitor_entry", TargetID: entryID, Diff: diff}); err != nil {
			return err
		}
		return s.visitorChanged(ctx, tx, siteID, "visitor_entry", entryID)
	})
	if err != nil {
		return VisitorEntry{}, tenancy.DBProblem(err, "visitor entry")
	}
	return out, nil
}

func visitorName(d Doc) (string, error) {
	n, err := d.String("name")
	if err != nil {
		return "", err
	}
	if n == nil || len([]rune(strings.TrimSpace(*n))) < 1 || len([]rune(*n)) > 120 {
		return "", validation("name", "invalid_value")
	}
	return strings.TrimSpace(*n), nil
}

// GetVisitorEntry reads an entry (visitors.read or visitors.manage).
func (s *Service) GetVisitorEntry(ctx context.Context, orgID, entryID uuid.UUID) (VisitorEntry, error) {
	var out VisitorEntry
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		v, err := loadEntry(ctx, tx, orgID, entryID, false)
		if err != nil {
			return err
		}
		sc, err := siteScope(ctx, tx, v.SiteID)
		if err != nil {
			return err
		}
		token, err := visitorAccess(ctx, sc, v.UnitID)
		if err != nil {
			return err
		}
		s.withToken(&v, token, "")
		out = v
		return nil
	})
	if err != nil {
		return VisitorEntry{}, tenancy.DBProblem(err, "visitor entry")
	}
	return out, nil
}

// EntryFilter narrows ListVisitorEntries.
type EntryFilter struct {
	SiteID         uuid.UUID
	UnitID         *uuid.UUID
	ApprovalStatus string
	From, To       *time.Time
	Arrived        *bool
	Q              string
}

// ListVisitorEntries lists a site's visitors: board, managers and auditors
// see all, residents those of their own unit.
func (s *Service) ListVisitorEntries(ctx context.Context, orgID uuid.UUID, f EntryFilter, p tenancy.PageParams) ([]VisitorEntry, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []VisitorEntry
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := siteScope(ctx, tx, f.SiteID)
		if err != nil {
			return err
		}
		if err := requireAny(ctx, sc, PermVisitorsRead, PermVisitorsManage); err != nil {
			return err
		}
		all := authz.Has(ctx, PermVisitorsRead, sc)
		showTokens := all && managerLevel(ctx, sc)
		var units []uuid.UUID
		if !all {
			units = residentUnits(ctx, f.SiteID)
			if f.UnitID != nil {
				if err := authz.CanActOnUnit(ctx, *f.UnitID); err != nil {
					return err
				}
			}
			showTokens = true
		}
		q := strings.TrimSpace(f.Q)
		rows, err := tx.Query(ctx, entrySelect+` WHERE e.org_id = $1 AND e.site_id = $2 AND e.id > $3
			AND ($4::uuid IS NULL OR e.unit_id = $4) AND ($5 = '' OR e.approval_status = $5)
			AND ($6::timestamptz IS NULL OR e.window_to >= $6) AND ($7::timestamptz IS NULL OR e.window_from <= $7)
			AND ($8::boolean IS NULL OR (e.arrived_at IS NOT NULL) = $8)
			AND ($9 = '' OR e.name ILIKE '%' || $9 || '%' OR e.plate LIKE '%' || upper(regexp_replace($9, '[^[:alnum:]]', '', 'g')) || '%')
			AND ($10 OR e.unit_id = ANY($11))
			ORDER BY e.id LIMIT $12`,
			orgID, f.SiteID, after, f.UnitID, f.ApprovalStatus, f.From, f.To, f.Arrived, escapeLike(q), all, units, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			v, err := scanEntry(rows)
			if err != nil {
				return err
			}
			s.withToken(&v, showTokens, "")
			out = append(out, v)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list visitor entries")
	}
	items, next := tenancy.Page(out, limit, func(v VisitorEntry) uuid.UUID { return v.ID })
	return items, next, nil
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// UpdateVisitorEntry changes a pre-registration that has not been used (visitors.manage).
func (s *Service) UpdateVisitorEntry(ctx context.Context, orgID, entryID uuid.UUID, expectedVersion int, d Doc) (VisitorEntry, error) {
	if bad := d.Unknown("name", "plate", "window_from", "window_to"); len(bad) > 0 {
		return VisitorEntry{}, problem.Validation(bad...)
	}
	var out VisitorEntry
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := loadEntry(ctx, tx, orgID, entryID, true)
		if err != nil {
			return err
		}
		sc, err := siteScope(ctx, tx, before.SiteID)
		if err != nil {
			return err
		}
		if err := requireManageUnit(ctx, sc, before.UnitID); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return tenancy.ErrVersion
		}
		if before.Source != "pre_registered" || before.Cancelled || before.ArrivedAt != nil {
			return problem.Conflict("only a pre-registration that has not been used can be changed")
		}
		next := before
		if d.Has("name") {
			if next.Name, err = visitorName(d); err != nil {
				return err
			}
		}
		if d.Has("plate") {
			next.Plate = nil
			if p, perr := d.String("plate"); perr != nil {
				return perr
			} else if p != nil {
				if next.Plate, err = normalizePlate("plate", *p); err != nil {
					return err
				}
			}
		}
		if d.Has("window_from") {
			if err := d.Get("window_from", &next.WindowFrom); err != nil {
				return err
			}
		}
		if d.Has("window_to") {
			if err := d.Get("window_to", &next.WindowTo); err != nil {
				return err
			}
		}
		if bad := checkWindow(s.now(), next.WindowFrom, next.WindowTo); len(bad) > 0 {
			return problem.Validation(bad...)
		}
		diff, err := audit.Diff("visitor_entry", before.auditView(), next.auditView())
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			s.withToken(&before, true, "")
			out = before
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE visitor_entry SET name = $3, plate = $4, window_from = $5, window_to = $6, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			orgID, entryID, next.Name, next.Plate, next.WindowFrom, next.WindowTo); err != nil {
			return err
		}
		out, err = loadEntry(ctx, tx, orgID, entryID, false)
		if err != nil {
			return err
		}
		s.withToken(&out, true, "")
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &out.SiteID, Action: "visitor.update", TargetType: "visitor_entry", TargetID: entryID, Diff: diff}); err != nil {
			return err
		}
		return s.visitorChanged(ctx, tx, out.SiteID, "visitor_entry", entryID)
	})
	if err != nil {
		return VisitorEntry{}, tenancy.DBProblem(err, "visitor entry")
	}
	return out, nil
}

// CancelVisitorEntry cancels an entry that has not arrived (visitors.manage);
// its QR stops working at once.
func (s *Service) CancelVisitorEntry(ctx context.Context, orgID, entryID uuid.UUID) (VisitorEntry, error) {
	var out VisitorEntry
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := loadEntry(ctx, tx, orgID, entryID, true)
		if err != nil {
			return err
		}
		sc, err := siteScope(ctx, tx, before.SiteID)
		if err != nil {
			return err
		}
		if err := requireManageUnit(ctx, sc, before.UnitID); err != nil {
			return err
		}
		if before.Cancelled || before.ArrivedAt != nil {
			return problem.Conflict("the visitor entry is already cancelled or used")
		}
		if _, err := tx.Exec(ctx, `UPDATE visitor_entry SET cancelled = true, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, entryID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE visitor_approval_request SET status = 'expired', updated_at = now() WHERE org_id = $1 AND entry_id = $2 AND status = 'pending'`, orgID, entryID); err != nil {
			return err
		}
		out, err = loadEntry(ctx, tx, orgID, entryID, false)
		if err != nil {
			return err
		}
		s.withToken(&out, true, "")
		diff, err := audit.Diff("visitor_entry", before.auditView(), out.auditView())
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &out.SiteID, Action: "visitor.cancel", TargetType: "visitor_entry", TargetID: entryID, Diff: diff}); err != nil {
			return err
		}
		return s.visitorChanged(ctx, tx, out.SiteID, "visitor_entry", entryID)
	})
	if err != nil {
		return VisitorEntry{}, tenancy.DBProblem(err, "visitor entry")
	}
	return out, nil
}

// EntryQR returns the QR text of an entry for the QR image operations; it is
// empty when the token is not kept (no encryption key).
func (s *Service) EntryQR(ctx context.Context, orgID, entryID uuid.UUID) (string, error) {
	v, err := s.GetVisitorEntry(ctx, orgID, entryID)
	if err != nil {
		return "", err
	}
	if v.QRToken == nil {
		return "", problem.NotFound("the QR is not available")
	}
	return *v.QRToken, nil
}

// ---------------------------------------------------------------------------
// recurring visitors

// RecurringVisitor is a standing permission with its own long-lived QR.
type RecurringVisitor struct {
	ID        uuid.UUID `json:"id"`
	OrgID     uuid.UUID `json:"org_id"`
	SiteID    uuid.UUID `json:"site_id"`
	UnitID    uuid.UUID `json:"unit_id"`
	Name      string    `json:"name"`
	Plate     *string   `json:"plate"`
	Days      []int     `json:"days"`
	Start     string    `json:"start"`
	End       string    `json:"end"`
	ValidFrom string    `json:"valid_from"`
	ValidTo   *string   `json:"valid_to"`
	Status    string    `json:"status"`
	QRToken   *string   `json:"qr_token"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int       `json:"version"`

	sealed []byte
}

type recurringAudit struct {
	Unit      uuid.UUID `json:"unit_id"`
	Name      string    `json:"name"`
	Plate     *string   `json:"plate"`
	Days      []int     `json:"days"`
	Start     string    `json:"start"`
	End       string    `json:"end"`
	ValidFrom string    `json:"valid_from"`
	ValidTo   *string   `json:"valid_to"`
	Status    string    `json:"status"`
}

func (r RecurringVisitor) auditView() recurringAudit {
	return recurringAudit{r.UnitID, r.Name, r.Plate, r.Days, r.Start, r.End, r.ValidFrom, r.ValidTo, r.Status}
}

func (r RecurringVisitor) rule() RecurringRule {
	return RecurringRule{Days: r.Days, Start: r.Start, End: r.End}
}

const recurringSelect = `SELECT r.id, r.org_id, r.site_id, r.unit_id, r.name, r.plate, r.days, r.start_time, r.end_time,
		to_char(r.valid_from, 'YYYY-MM-DD'), to_char(r.valid_to, 'YYYY-MM-DD'),
		CASE WHEN r.status = 'revoked' THEN 'revoked' WHEN r.valid_to IS NOT NULL AND r.valid_to < (now() AT TIME ZONE s.timezone)::date THEN 'expired' ELSE 'active' END,
		r.created_at, r.updated_at, r.version, r.qr_token_sealed
	FROM recurring_visitor r JOIN site s ON s.org_id = r.org_id AND s.id = r.site_id`

func scanRecurring(row pgx.Row) (RecurringVisitor, error) {
	var r RecurringVisitor
	var days []int16
	err := row.Scan(&r.ID, &r.OrgID, &r.SiteID, &r.UnitID, &r.Name, &r.Plate, &days, &r.Start, &r.End, &r.ValidFrom, &r.ValidTo, &r.Status,
		&r.CreatedAt, &r.UpdatedAt, &r.Version, &r.sealed)
	if err != nil {
		return r, err
	}
	r.Days = make([]int, len(days))
	for i, d := range days {
		r.Days[i] = int(d)
	}
	r.CreatedAt, r.UpdatedAt = r.CreatedAt.UTC(), r.UpdatedAt.UTC()
	return r, nil
}

func loadRecurring(ctx context.Context, tx pgx.Tx, orgID, rvID uuid.UUID, forUpdate bool) (RecurringVisitor, error) {
	q := recurringSelect + ` WHERE r.org_id = $1 AND r.id = $2`
	if forUpdate {
		q += ` FOR UPDATE OF r`
	}
	r, err := scanRecurring(tx.QueryRow(ctx, q, orgID, rvID))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, problem.NotFound("no such recurring visitor")
	}
	return r, err
}

func (s *Service) recurringToken(r *RecurringVisitor, show bool, plain string) {
	switch {
	case plain != "":
		r.QRToken = &plain
	case show && len(r.sealed) > 0:
		if t := s.openQRToken(r.OrgID, r.ID, r.sealed); t != "" {
			r.QRToken = &t
		}
	}
}

func parseDate(d Doc, key string) (*time.Time, error) {
	s, err := d.String(key)
	if err != nil || s == nil {
		return nil, err
	}
	t, perr := time.Parse("2006-01-02", *s)
	if perr != nil {
		return nil, validation(key, "invalid_format")
	}
	return &t, nil
}

// CreateRecurringVisitor creates a standing permission (visitors.manage); the
// response carries the long-lived QR text.
func (s *Service) CreateRecurringVisitor(ctx context.Context, orgID uuid.UUID, d Doc) (RecurringVisitor, error) {
	if bad := d.Unknown("site_id", "unit_id", "name", "plate", "days", "start", "end", "valid_from", "valid_to"); len(bad) > 0 {
		return RecurringVisitor{}, problem.Validation(bad...)
	}
	var siteID, unitID uuid.UUID
	if err := d.Get("site_id", &siteID); err != nil || siteID == uuid.Nil {
		return RecurringVisitor{}, validation("site_id", "required")
	}
	if err := d.Get("unit_id", &unitID); err != nil || unitID == uuid.Nil {
		return RecurringVisitor{}, validation("unit_id", "required")
	}
	name, err := visitorName(d)
	if err != nil {
		return RecurringVisitor{}, err
	}
	var plate *string
	if p, perr := d.String("plate"); perr != nil {
		return RecurringVisitor{}, perr
	} else if p != nil {
		if plate, err = normalizePlate("plate", *p); err != nil {
			return RecurringVisitor{}, err
		}
	}
	var rule RecurringRule
	if err := d.Get("days", &rule.Days); err != nil {
		return RecurringVisitor{}, err
	}
	for k, dst := range map[string]*string{"start": &rule.Start, "end": &rule.End} {
		if err := d.Get(k, dst); err != nil {
			return RecurringVisitor{}, err
		}
	}
	if bad := rule.Validate(); len(bad) > 0 {
		return RecurringVisitor{}, problem.Validation(bad...)
	}
	from, err := parseDate(d, "valid_from")
	if err != nil || from == nil {
		return RecurringVisitor{}, validation("valid_from", "required")
	}
	to, err := parseDate(d, "valid_to")
	if err != nil {
		return RecurringVisitor{}, err
	}
	if to != nil && to.Before(*from) {
		return RecurringVisitor{}, validation("valid_to", "end_before_start")
	}
	days := make([]int16, len(rule.Days))
	for i, x := range rule.Days {
		days[i] = int16(x) //nolint:gosec // weekdays are validated to 1..7
	}
	var out RecurringVisitor
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := siteScope(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := requireManageUnit(ctx, sc, unitID); err != nil {
			return err
		}
		if _, err := loadSite(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		if _, err := loadUnit(ctx, tx, orgID, siteID, unitID, "unit_id"); err != nil {
			return err
		}
		person, _, err := callerPerson(ctx, tx, orgID)
		if err != nil {
			return err
		}
		rvID := id.New()
		qr, hash, sealed, err := s.newQRToken(orgID, rvID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO recurring_visitor (id, org_id, site_id, unit_id, name, plate, days, start_time, end_time, valid_from, valid_to, qr_token_hash, qr_token_sealed, registered_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
			rvID, orgID, siteID, unitID, name, plate, days, rule.Start, rule.End, *from, to, hash, sealed, nilIfZero(person)); err != nil {
			return err
		}
		out, err = loadRecurring(ctx, tx, orgID, rvID, false)
		if err != nil {
			return err
		}
		s.recurringToken(&out, true, qr)
		diff, err := audit.Diff("recurring_visitor", recurringAudit{}, out.auditView())
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "recurring_visitor.create", TargetType: "recurring_visitor", TargetID: rvID, Diff: diff}); err != nil {
			return err
		}
		return s.visitorChanged(ctx, tx, siteID, "recurring_visitor", rvID)
	})
	if err != nil {
		return RecurringVisitor{}, tenancy.DBProblem(err, "recurring visitor")
	}
	return out, nil
}

// GetRecurringVisitor reads a recurring visitor (visitors.read or visitors.manage).
func (s *Service) GetRecurringVisitor(ctx context.Context, orgID, rvID uuid.UUID) (RecurringVisitor, error) {
	var out RecurringVisitor
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		r, err := loadRecurring(ctx, tx, orgID, rvID, false)
		if err != nil {
			return err
		}
		sc, err := siteScope(ctx, tx, r.SiteID)
		if err != nil {
			return err
		}
		token, err := visitorAccess(ctx, sc, r.UnitID)
		if err != nil {
			return err
		}
		s.recurringToken(&r, token, "")
		out = r
		return nil
	})
	if err != nil {
		return RecurringVisitor{}, tenancy.DBProblem(err, "recurring visitor")
	}
	return out, nil
}

// ListRecurringVisitors lists a site's recurring visitors (readers of the site; residents their unit's).
func (s *Service) ListRecurringVisitors(ctx context.Context, orgID, siteID uuid.UUID, unitID *uuid.UUID, status string, p tenancy.PageParams) ([]RecurringVisitor, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []RecurringVisitor
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := siteScope(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := requireAny(ctx, sc, PermVisitorsRead, PermVisitorsManage); err != nil {
			return err
		}
		all := authz.Has(ctx, PermVisitorsRead, sc)
		show := !all || managerLevel(ctx, sc)
		var units []uuid.UUID
		if !all {
			units = residentUnits(ctx, siteID)
			if unitID != nil {
				if err := authz.CanActOnUnit(ctx, *unitID); err != nil {
					return err
				}
			}
		}
		rows, err := tx.Query(ctx, recurringSelect+` WHERE r.org_id = $1 AND r.site_id = $2 AND r.id > $3 AND ($4::uuid IS NULL OR r.unit_id = $4)
			AND ($5 OR r.unit_id = ANY($6)) ORDER BY r.id LIMIT $7`, orgID, siteID, after, unitID, all, units, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanRecurring(rows)
			if err != nil {
				return err
			}
			if status != "" && r.Status != status {
				continue
			}
			s.recurringToken(&r, show, "")
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list recurring visitors")
	}
	items, next := tenancy.Page(out, limit, func(r RecurringVisitor) uuid.UUID { return r.ID })
	return items, next, nil
}

// UpdateRecurringVisitor changes a recurring visitor (visitors.manage).
func (s *Service) UpdateRecurringVisitor(ctx context.Context, orgID, rvID uuid.UUID, expectedVersion int, d Doc) (RecurringVisitor, error) {
	if bad := d.Unknown("name", "plate", "days", "start", "end", "valid_to"); len(bad) > 0 {
		return RecurringVisitor{}, problem.Validation(bad...)
	}
	var out RecurringVisitor
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := loadRecurring(ctx, tx, orgID, rvID, true)
		if err != nil {
			return err
		}
		sc, err := siteScope(ctx, tx, before.SiteID)
		if err != nil {
			return err
		}
		if err := requireManageUnit(ctx, sc, before.UnitID); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return tenancy.ErrVersion
		}
		if before.Status == "revoked" {
			return problem.Conflict("the recurring visitor was revoked")
		}
		next := before
		if d.Has("name") {
			if next.Name, err = visitorName(d); err != nil {
				return err
			}
		}
		if d.Has("plate") {
			next.Plate = nil
			if p, perr := d.String("plate"); perr != nil {
				return perr
			} else if p != nil {
				if next.Plate, err = normalizePlate("plate", *p); err != nil {
					return err
				}
			}
		}
		for k, dst := range map[string]*string{"start": &next.Start, "end": &next.End} {
			if d.Has(k) {
				if err := d.Get(k, dst); err != nil {
					return err
				}
			}
		}
		if d.Has("days") {
			if err := d.Get("days", &next.Days); err != nil {
				return err
			}
		}
		if bad := next.rule().Validate(); len(bad) > 0 {
			return problem.Validation(bad...)
		}
		if d.Has("valid_to") {
			t, err := parseDate(d, "valid_to")
			if err != nil {
				return err
			}
			next.ValidTo = nil
			if t != nil {
				if t.Format("2006-01-02") < next.ValidFrom {
					return validation("valid_to", "end_before_start")
				}
				v := t.Format("2006-01-02")
				next.ValidTo = &v
			}
		}
		diff, err := audit.Diff("recurring_visitor", before.auditView(), next.auditView())
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			s.recurringToken(&before, true, "")
			out = before
			return nil
		}
		days := make([]int16, len(next.Days))
		for i, x := range next.Days {
			days[i] = int16(x) //nolint:gosec // weekdays are validated to 1..7
		}
		if _, err := tx.Exec(ctx, `UPDATE recurring_visitor SET name = $3, plate = $4, days = $5, start_time = $6, end_time = $7, valid_to = $8::date, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2`, orgID, rvID, next.Name, next.Plate, days, next.Start, next.End, next.ValidTo); err != nil {
			return err
		}
		out, err = loadRecurring(ctx, tx, orgID, rvID, false)
		if err != nil {
			return err
		}
		s.recurringToken(&out, true, "")
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &out.SiteID, Action: "recurring_visitor.update", TargetType: "recurring_visitor", TargetID: rvID, Diff: diff}); err != nil {
			return err
		}
		return s.visitorChanged(ctx, tx, out.SiteID, "recurring_visitor", rvID)
	})
	if err != nil {
		return RecurringVisitor{}, tenancy.DBProblem(err, "recurring visitor")
	}
	return out, nil
}

// RevokeRecurringVisitor ends a standing permission at once, also at the gate (visitors.manage).
func (s *Service) RevokeRecurringVisitor(ctx context.Context, orgID, rvID uuid.UUID) (RecurringVisitor, error) {
	var out RecurringVisitor
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := loadRecurring(ctx, tx, orgID, rvID, true)
		if err != nil {
			return err
		}
		sc, err := siteScope(ctx, tx, before.SiteID)
		if err != nil {
			return err
		}
		if err := requireManageUnit(ctx, sc, before.UnitID); err != nil {
			return err
		}
		if before.Status == "revoked" {
			return problem.Conflict("the recurring visitor is already revoked")
		}
		if _, err := tx.Exec(ctx, `UPDATE recurring_visitor SET status = 'revoked', updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, rvID); err != nil {
			return err
		}
		out, err = loadRecurring(ctx, tx, orgID, rvID, false)
		if err != nil {
			return err
		}
		s.recurringToken(&out, true, "")
		diff, err := audit.Diff("recurring_visitor", before.auditView(), out.auditView())
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &out.SiteID, Action: "recurring_visitor.revoke", TargetType: "recurring_visitor", TargetID: rvID, Diff: diff}); err != nil {
			return err
		}
		return s.visitorChanged(ctx, tx, out.SiteID, "recurring_visitor", rvID)
	})
	if err != nil {
		return RecurringVisitor{}, tenancy.DBProblem(err, "recurring visitor")
	}
	return out, nil
}

// RecurringQR returns the QR text of a recurring visitor.
func (s *Service) RecurringQR(ctx context.Context, orgID, rvID uuid.UUID) (string, error) {
	r, err := s.GetRecurringVisitor(ctx, orgID, rvID)
	if err != nil {
		return "", err
	}
	if r.QRToken == nil {
		return "", problem.NotFound("the QR is not available")
	}
	return *r.QRToken, nil
}

// ---------------------------------------------------------------------------
// approval requests

// ApprovalRequest is the approve / deny request a resident answers for an
// unexpected visitor.
type ApprovalRequest struct {
	ID             uuid.UUID  `json:"id"`
	OrgID          uuid.UUID  `json:"org_id"`
	EntryID        uuid.UUID  `json:"entry_id"`
	UnitID         uuid.UUID  `json:"unit_id"`
	UnitDoorNumber string     `json:"unit_door_number"`
	VisitorName    string     `json:"visitor_name"`
	Plate          *string    `json:"plate"`
	RequestedAt    time.Time  `json:"requested_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	Status         string     `json:"status"`
	DecidedBy      *uuid.UUID `json:"decided_by"`
	DecidedAt      *time.Time `json:"decided_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`

	siteID uuid.UUID
}

const requestSelect = `SELECT r.id, r.org_id, r.entry_id, r.unit_id, u.door_number, e.name, e.plate, r.requested_at, r.expires_at,
		CASE WHEN r.status = 'pending' AND r.expires_at <= now() THEN 'expired' ELSE r.status END,
		r.decided_by, r.decided_at, r.created_at, r.updated_at, r.site_id
	FROM visitor_approval_request r
	JOIN visitor_entry e ON e.org_id = r.org_id AND e.id = r.entry_id
	JOIN unit u ON u.org_id = r.org_id AND u.id = r.unit_id`

func scanRequest(row pgx.Row) (ApprovalRequest, error) {
	var r ApprovalRequest
	err := row.Scan(&r.ID, &r.OrgID, &r.EntryID, &r.UnitID, &r.UnitDoorNumber, &r.VisitorName, &r.Plate, &r.RequestedAt, &r.ExpiresAt, &r.Status,
		&r.DecidedBy, &r.DecidedAt, &r.CreatedAt, &r.UpdatedAt, &r.siteID)
	r.RequestedAt, r.ExpiresAt, r.CreatedAt, r.UpdatedAt = r.RequestedAt.UTC(), r.ExpiresAt.UTC(), r.CreatedAt.UTC(), r.UpdatedAt.UTC()
	utcPtr(&r.DecidedAt)
	return r, err
}

// ListApprovalRequests lists the approval requests of the caller's own units
// (visitors.decide); pending ones past their expiry read as expired.
func (s *Service) ListApprovalRequests(ctx context.Context, orgID uuid.UUID, status string, unitID *uuid.UUID, p tenancy.PageParams) ([]ApprovalRequest, string, error) {
	if err := authz.RequireInOrg(ctx, PermVisitorsDecide, orgID); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var units []uuid.UUID
	for _, o := range authz.GrantsFromContext(ctx).Current() {
		units = append(units, o.UnitID)
	}
	if unitID != nil {
		if err := authz.CanActOnUnit(ctx, *unitID); err != nil {
			return nil, "", err
		}
	}
	var out []ApprovalRequest
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, requestSelect+` WHERE r.org_id = $1 AND r.id > $2 AND r.unit_id = ANY($3) AND ($4::uuid IS NULL OR r.unit_id = $4)
			AND ($5 = '' OR CASE WHEN r.status = 'pending' AND r.expires_at <= now() THEN 'expired' ELSE r.status END = $5)
			ORDER BY r.id LIMIT $6`, orgID, after, units, unitID, status, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanRequest(rows)
			if err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list approval requests")
	}
	items, next := tenancy.Page(out, limit, func(r ApprovalRequest) uuid.UUID { return r.ID })
	return items, next, nil
}

// DecideApprovalRequest answers a request: the first answer of any occupant of
// the unit decides (visitors.decide). Approving admits the visitor for the
// next 30 minutes; an answer after the expiry is a 409 conflict.
func (s *Service) DecideApprovalRequest(ctx context.Context, orgID, requestID uuid.UUID, decision string) (ApprovalRequest, error) {
	if decision != "approve" && decision != "deny" {
		return ApprovalRequest{}, validation("decision", "invalid_value")
	}
	now := s.now()
	var out ApprovalRequest
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		req, err := scanRequest(tx.QueryRow(ctx, requestSelect+` WHERE r.org_id = $1 AND r.id = $2 FOR UPDATE OF r`, orgID, requestID))
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such approval request")
		}
		if err != nil {
			return err
		}
		sc, err := siteScope(ctx, tx, req.siteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermVisitorsDecide, sc); err != nil {
			return err
		}
		if authz.CanActOnUnit(ctx, req.UnitID) != nil {
			return problem.NotFound("no such approval request")
		}
		switch req.Status {
		case "pending":
		case "expired":
			return problem.Conflict("the request expired; the guard will call you")
		default:
			return problem.Conflict("the request was already answered")
		}
		person, ok, err := callerPerson(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if !ok {
			return problem.Forbidden("no person is linked to this account")
		}
		status, entryStatus := "denied", "denied"
		if decision == "approve" {
			status, entryStatus = "approved", "approved"
		}
		if _, err := tx.Exec(ctx, `UPDATE visitor_approval_request SET status = $3, decided_by = $4, decided_at = $5, updated_at = now() WHERE org_id = $1 AND id = $2`,
			orgID, requestID, status, person, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE visitor_entry SET approval_status = $3, decided_by = $4, decided_at = $5,
				window_from = CASE WHEN $3::text = 'approved' THEN $5::timestamptz ELSE window_from END, window_to = CASE WHEN $3::text = 'approved' THEN $5::timestamptz ELSE window_to END,
				updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			orgID, req.EntryID, entryStatus, person, now); err != nil {
			return err
		}
		out, err = scanRequest(tx.QueryRow(ctx, requestSelect+` WHERE r.org_id = $1 AND r.id = $2`, orgID, requestID))
		if err != nil {
			return err
		}
		diff := audit.Changes{"status": {Before: mustJSON("pending"), After: mustJSON(status)}}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &req.siteID, Action: "visitor.decide", TargetType: "visitor_approval_request", TargetID: requestID, Diff: diff}); err != nil {
			return err
		}
		if h := s.cfg.Hooks.OnVisitorDecided; h != nil {
			if err := h(ctx, tx, VisitorDecision{OrgID: orgID, SiteID: req.siteID, EntryID: req.EntryID, UnitID: req.UnitID, RequestID: requestID, Approved: decision == "approve", DecidedBy: person, At: now}); err != nil {
				return err
			}
		}
		return s.visitorChanged(ctx, tx, req.siteID, "visitor_entry", req.EntryID)
	})
	if err != nil {
		return ApprovalRequest{}, tenancy.DBProblem(err, "approval request")
	}
	return out, nil
}

// unitOccupants returns the people who currently own, rent or live in the unit.
func unitOccupants(ctx context.Context, tx pgx.Tx, orgID, unitID uuid.UUID, day time.Time) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT person_id FROM occupancy WHERE org_id = $1 AND unit_id = $2 AND valid_from <= $3::date AND (valid_to IS NULL OR valid_to > $3::date)`,
		orgID, unitID, day.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var p uuid.UUID
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
