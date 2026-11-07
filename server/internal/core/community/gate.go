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
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Guard minimization (S3, C2): everything below carries what a guard needs
// and nothing else: names, plates, door numbers, windows and the gate contact
// number of one resident for one open request. No contacts, balances,
// national ids or QR tokens. The types list their fields explicitly.

// GuardVisitor is what a guard sees of a visitor.
type GuardVisitor struct {
	ID                uuid.UUID  `json:"id"`
	Name              string     `json:"name"`
	Plate             *string    `json:"plate"`
	UnitDoorNumber    string     `json:"unit_door_number"`
	WindowFrom        time.Time  `json:"window_from"`
	WindowTo          time.Time  `json:"window_to"`
	ApprovalStatus    string     `json:"approval_status"`
	ApprovalExpiresAt *time.Time `json:"approval_expires_at"`
	ArrivedAt         *time.Time `json:"arrived_at"`
	LeftAt            *time.Time `json:"left_at"`
	Recurring         bool       `json:"recurring"`
}

// guardView converts an entry for the guard; an open request past its expiry
// reads as expired before the expiry job has run.
func guardView(v VisitorEntry, now time.Time) GuardVisitor {
	g := GuardVisitor{ID: v.ID, Name: v.Name, Plate: v.Plate, UnitDoorNumber: v.UnitDoorNumber, WindowFrom: v.WindowFrom, WindowTo: v.WindowTo,
		ApprovalStatus: v.ApprovalStatus, ApprovalExpiresAt: v.approvalExpiresAt, ArrivedAt: v.ArrivedAt, LeftAt: v.LeftAt, Recurring: v.RecurringVisitorID != nil}
	if v.ApprovalStatus == "requested" && v.approvalExpiresAt != nil && !v.approvalExpiresAt.After(now) {
		g.ApprovalStatus, g.ApprovalExpiresAt = "expired", nil
	}
	return g
}

// GateDelivery is what a guard sees of a parcel.
type GateDelivery struct {
	ID              uuid.UUID `json:"id"`
	UnitDoorNumber  string    `json:"unit_door_number"`
	ReceivedAt      time.Time `json:"received_at"`
	Description     *string   `json:"description"`
	Status          string    `json:"status"`
	CollectedByName *string   `json:"collected_by_name"`
}

// GatePlate is a registered resident plate: plate and door number only.
type GatePlate struct {
	Plate          string `json:"plate"`
	UnitDoorNumber string `json:"unit_door_number"`
}

// ScanResult is the verdict on a scanned QR.
type ScanResult struct {
	Valid  bool          `json:"valid"`
	Reason string        `json:"reason"`
	Entry  *GuardVisitor `json:"entry"`
}

// CallContact is the one contact a guard can reach: the number to call when
// the resident does not answer.
type CallContact struct {
	DisplayName *string `json:"display_name"`
	Phone       string  `json:"phone"`
}

// GatePoint and GateAgentStatus describe the access agent; the access module
// fills them (agent is null while the site has none).
type GatePoint struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Direction string    `json:"direction"`
	Enabled   bool      `json:"enabled"`
}

// GateAgentStatus is the state of the site's access agent.
type GateAgentStatus struct {
	Connected  bool        `json:"connected"`
	LastSeenAt *time.Time  `json:"last_seen_at"`
	Points     []GatePoint `json:"points"`
}

// GateConsole is everything the booth page shows at once.
type GateConsole struct {
	ServerTime        time.Time        `json:"server_time"`
	ExpectedVisitors  []GuardVisitor   `json:"expected_visitors"`
	PendingApprovals  []GuardVisitor   `json:"pending_approvals"`
	WaitingDeliveries []GateDelivery   `json:"waiting_deliveries"`
	Agent             *GateAgentStatus `json:"agent"`
}

// dayBounds returns [00:00, 24:00) of the site-local day containing t.
func dayBounds(t time.Time, loc *time.Location) (time.Time, time.Time) {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc), time.Date(y, m, d+1, 0, 0, 0, 0, loc)
}

// GateConsole returns the booth snapshot (gate.operate).
func (s *Service) GateConsole(ctx context.Context, orgID, siteID uuid.UUID, guardToken string) (GateConsole, error) {
	now := s.now()
	out := GateConsole{ServerTime: now.UTC(), ExpectedVisitors: []GuardVisitor{}, PendingApprovals: []GuardVisitor{}, WaitingDeliveries: []GateDelivery{}}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := s.gateAt(ctx, tx, orgID, siteID, guardToken); err != nil {
			return err
		}
		si, err := loadSite(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		from, to := dayBounds(now, si.Loc)
		rows, err := tx.Query(ctx, entrySelect+` WHERE e.org_id = $1 AND e.site_id = $2 AND NOT e.cancelled AND e.approval_status IN ('pre_approved', 'approved')
			AND e.window_to >= $3 AND e.window_from < $4 ORDER BY e.window_from, e.id LIMIT 300`, orgID, siteID, from.Add(-visitorSlack), to.Add(visitorSlack))
		if err != nil {
			return err
		}
		for rows.Next() {
			v, err := scanEntry(rows)
			if err != nil {
				rows.Close()
				return err
			}
			out.ExpectedVisitors = append(out.ExpectedVisitors, guardView(v, now))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, entrySelect+` WHERE e.org_id = $1 AND e.site_id = $2 AND NOT e.cancelled AND e.approval_status = 'requested'
			AND EXISTS (SELECT 1 FROM visitor_approval_request r WHERE r.org_id = e.org_id AND r.entry_id = e.id AND r.status = 'pending' AND r.expires_at > $3)
			ORDER BY e.created_at, e.id LIMIT 100`, orgID, siteID, now)
		if err != nil {
			return err
		}
		for rows.Next() {
			v, err := scanEntry(rows)
			if err != nil {
				rows.Close()
				return err
			}
			out.PendingApprovals = append(out.PendingApprovals, guardView(v, now))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		out.WaitingDeliveries, _, err = listGateDeliveries(ctx, tx, orgID, siteID, "", "waiting", uuid.Nil, 100)
		return err
	})
	if err != nil {
		return GateConsole{}, tenancy.DBProblem(err, "gate console")
	}
	return out, nil
}

// GateVisitorFilter narrows ListGateVisitors.
type GateVisitorFilter struct {
	SiteID         uuid.UUID
	Q, Plate, Door string
	Date           string
	ApprovalStatus string
}

// ListGateVisitors searches a site's visitors by name, plate or door number
// (gate.operate); without a date it looks at today.
func (s *Service) ListGateVisitors(ctx context.Context, orgID uuid.UUID, f GateVisitorFilter, guardToken string, p tenancy.PageParams) ([]GuardVisitor, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	now := s.now()
	var out []GuardVisitor
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := s.gateAt(ctx, tx, orgID, f.SiteID, guardToken); err != nil {
			return err
		}
		si, err := loadSite(ctx, tx, orgID, f.SiteID)
		if err != nil {
			return err
		}
		day := now
		if f.Date != "" {
			t, perr := time.ParseInLocation("2006-01-02", f.Date, si.Loc)
			if perr != nil {
				return validation("date", "invalid_format")
			}
			day = t
		}
		from, to := dayBounds(day, si.Loc)
		plate := ""
		if f.Plate != "" {
			plate, _ = tenancy.NormalizePlate(f.Plate)
			if plate == "" {
				return validation("plate", "invalid_format")
			}
		}
		rows, err := tx.Query(ctx, entrySelect+` WHERE e.org_id = $1 AND e.site_id = $2 AND e.id > $3 AND e.window_to >= $4 AND e.window_from < $5
			AND ($6 = '' OR e.name ILIKE '%' || $6 || '%' OR e.plate LIKE '%' || upper(regexp_replace($6, '[^[:alnum:]]', '', 'g')) || '%')
			AND ($7 = '' OR e.plate LIKE '%' || $7 || '%') AND ($8 = '' OR u.door_number = $8) AND ($9 = '' OR e.approval_status = $9)
			ORDER BY e.id LIMIT $10`, orgID, f.SiteID, after, from.Add(-visitorSlack), to.Add(visitorSlack),
			escapeLike(strings.TrimSpace(f.Q)), plate, f.Door, f.ApprovalStatus, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		var ids []uuid.UUID
		for rows.Next() {
			v, err := scanEntry(rows)
			if err != nil {
				return err
			}
			out = append(out, guardView(v, now))
			ids = append(ids, v.ID)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list gate visitors")
	}
	items, next := tenancy.Page(out, limit, func(v GuardVisitor) uuid.UUID { return v.ID })
	return items, next, nil
}

// GetGateVisitor polls one visitor, for the approval status (gate.operate).
func (s *Service) GetGateVisitor(ctx context.Context, orgID, entryID uuid.UUID, guardToken string) (GuardVisitor, error) {
	now := s.now()
	var out GuardVisitor
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		v, err := loadEntry(ctx, tx, orgID, entryID, false)
		if err != nil {
			return err
		}
		if _, err := s.gateAt(ctx, tx, orgID, v.SiteID, guardToken); err != nil {
			return err
		}
		out = guardView(v, now)
		return nil
	})
	if err != nil {
		return GuardVisitor{}, tenancy.DBProblem(err, "visitor")
	}
	return out, nil
}

// ScanGateVisitor validates a scanned QR online against the visitor entries
// (gate.operate); it does not admit: arriving does. The verdict is audited.
func (s *Service) ScanGateVisitor(ctx context.Context, orgID uuid.UUID, text, guardToken string) (ScanResult, error) {
	now := s.now()
	out := ScanResult{Reason: ReasonUnknown}
	hash, parsed := ParseQR(text)
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if !parsed {
			return nil
		}
		entry, err := scanEntry(tx.QueryRow(ctx, entrySelect+` WHERE e.org_id = $1 AND e.qr_token_hash = $2`, orgID, hash))
		isEntry := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var rv RecurringVisitor
		if !isEntry {
			rv, err = scanRecurring(tx.QueryRow(ctx, recurringSelect+` WHERE r.org_id = $1 AND r.qr_token_hash = $2 FOR UPDATE OF r`, orgID, hash))
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
		}
		siteID := entry.SiteID
		if !isEntry {
			siteID = rv.SiteID
		}
		// the console may only judge QRs of its own site; a token of another site is not revealed
		sc, err := siteScope(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if !authz.Has(ctx, PermGateOperate, sc) {
			out.Reason = ReasonWrongSite
			return nil
		}
		g, err := s.guardFor(ctx, tx, orgID, siteID, guardToken)
		if err != nil {
			var pe *problem.Error
			if errors.As(err, &pe) && pe.Status == 403 {
				out.Reason = ReasonWrongSite
				return nil
			}
			return err
		}
		si, err := loadSite(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		var visit *VisitorEntry
		if isEntry {
			out.Reason = evalEntry(entry, now)
			visit = &entry
		} else {
			out.Reason = evalRecurring(rv, now, si.Loc)
			if out.Reason == ReasonMatch {
				v, err := s.recurringVisit(ctx, tx, orgID, rv, now)
				if err != nil {
					return err
				}
				visit = &v
			}
		}
		out.Valid = out.Reason == ReasonMatch
		if visit != nil {
			gv := guardView(*visit, now)
			out.Entry = &gv
		}
		target, ttype := entry.ID, "visitor_entry"
		if !isEntry {
			target, ttype = rv.ID, "recurring_visitor"
		}
		return gateAudit(ctx, tx, orgID, siteID, g, "gate.scan", ttype, target, audit.Changes{"result": {After: mustJSON(out.Reason)}})
	})
	if err != nil {
		return ScanResult{}, tenancy.DBProblem(err, "scan")
	}
	return out, nil
}

// evalEntry judges a pre-registered entry at time now.
func evalEntry(e VisitorEntry, now time.Time) string {
	switch {
	case e.Cancelled || e.ApprovalStatus == "denied":
		return ReasonRevoked
	case e.ApprovalStatus != "pre_approved" && e.ApprovalStatus != "approved":
		return ReasonOutsideWindow
	case e.ArrivedAt != nil:
		return ReasonUsedUp
	case !InWindow(e.WindowFrom, e.WindowTo, now):
		return ReasonOutsideWindow
	}
	return ReasonMatch
}

// evalRecurring judges a recurring visitor at time now in the site's zone.
func evalRecurring(r RecurringVisitor, now time.Time, loc *time.Location) string {
	if r.Status == "revoked" {
		return ReasonRevoked
	}
	local := now.In(loc).Format("2006-01-02")
	if local < r.ValidFrom || r.ValidTo != nil && local > *r.ValidTo || !r.rule().Matches(now, loc) {
		return ReasonOutsideWindow
	}
	return ReasonMatch
}

// recurringVisit returns the open visit of a recurring visitor (one created
// by a scan in the last half hour that has not arrived), or creates it.
func (s *Service) recurringVisit(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, rv RecurringVisitor, now time.Time) (VisitorEntry, error) {
	v, err := scanEntry(tx.QueryRow(ctx, entrySelect+` WHERE e.org_id = $1 AND e.recurring_visitor_id = $2 AND e.arrived_at IS NULL AND NOT e.cancelled AND e.window_to >= $3
		ORDER BY e.created_at DESC LIMIT 1`, orgID, rv.ID, now.Add(-visitorSlack)))
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return v, err
	}
	entryID := id.New()
	if _, err := tx.Exec(ctx, `INSERT INTO visitor_entry (id, org_id, site_id, unit_id, name, plate, window_from, window_to, approval_status, source, recurring_visitor_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7, 'pre_approved', 'recurring', $8)`, entryID, orgID, rv.SiteID, rv.UnitID, rv.Name, rv.Plate, now, rv.ID); err != nil {
		return v, err
	}
	return loadEntry(ctx, tx, orgID, entryID, false)
}

// WalkIn is a visitor without a QR the guard registers.
type WalkIn struct {
	SiteID, UnitID uuid.UUID
	Name, Plate    string
}

// CreateWalkInVisitor registers a visitor without a QR and asks the unit's
// residents to approve or deny (gate.operate): the entry is "requested", every
// current occupant is notified with a link, and the request expires (10
// minutes by default) after which the guard calls the resident.
func (s *Service) CreateWalkInVisitor(ctx context.Context, orgID uuid.UUID, in WalkIn, guardToken string) (GuardVisitor, error) {
	name := strings.TrimSpace(in.Name)
	if n := len([]rune(name)); n < 1 || n > 120 {
		return GuardVisitor{}, validation("name", "invalid_value")
	}
	plate, err := normalizePlate("plate", in.Plate)
	if err != nil {
		return GuardVisitor{}, err
	}
	now := s.now()
	var out GuardVisitor
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		g, err := s.gateAt(ctx, tx, orgID, in.SiteID, guardToken)
		if err != nil {
			return err
		}
		si, err := loadSite(ctx, tx, orgID, in.SiteID)
		if err != nil {
			return err
		}
		unit, err := loadUnit(ctx, tx, orgID, in.SiteID, in.UnitID, "unit_id")
		if err != nil {
			return err
		}
		people, err := unitOccupants(ctx, tx, orgID, in.UnitID, now.In(si.Loc))
		if err != nil {
			return err
		}
		if len(people) == 0 {
			return problem.Conflict("nobody lives in that unit; there is nobody to ask")
		}
		entryID, requestID := id.New(), id.New()
		expires := now.Add(si.requestExpiry())
		if _, err := tx.Exec(ctx, `INSERT INTO visitor_entry (id, org_id, site_id, unit_id, name, plate, window_from, window_to, approval_status, source, registered_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $7, 'requested', 'walk_in', $8)`, entryID, orgID, in.SiteID, in.UnitID, name, plate, now, g.Person); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO visitor_approval_request (id, org_id, site_id, entry_id, unit_id, requested_by, requested_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, requestID, orgID, in.SiteID, entryID, in.UnitID, g.Person, now, expires); err != nil {
			return err
		}
		data := map[string]any{"request_id": requestID, "visitor_name": name, "unit_door_number": unit.DoorNumber, "expires_at": expires.UTC().Format(time.RFC3339)}
		if plate != nil {
			data["plate"] = *plate
		}
		if s.cfg.BaseURL != "" {
			data["link"] = s.cfg.BaseURL + "/visitors/requests/" + requestID.String()
		}
		if err := s.cfg.Notifier.Notify(ctx, tx, jobs.Notify{OrgID: orgID, SiteID: &in.SiteID, PersonIDs: people, Template: "visitor.approval_requested",
			Data: data, Channels: []string{"inapp", "sms"}, DedupeKey: "visitor.approval_requested:" + requestID.String()}); err != nil {
			return err
		}
		v, err := loadEntry(ctx, tx, orgID, entryID, false)
		if err != nil {
			return err
		}
		out = guardView(v, now)
		if err := gateAudit(ctx, tx, orgID, in.SiteID, g, "gate.walk_in", "visitor_entry", entryID, audit.Changes{"unit_id": {After: mustJSON(in.UnitID.String())}}); err != nil {
			return err
		}
		return s.visitorChanged(ctx, tx, in.SiteID, "visitor_entry", entryID)
	})
	if err != nil {
		return GuardVisitor{}, tenancy.DBProblem(err, "walk-in visitor")
	}
	return out, nil
}

// ArriveGateVisitor admits a visitor (gate.operate): only a valid
// pre-approved or approved entry that has not been used.
func (s *Service) ArriveGateVisitor(ctx context.Context, orgID, entryID uuid.UUID, guardToken string) (GuardVisitor, error) {
	now := s.now()
	var out GuardVisitor
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		v, err := loadEntry(ctx, tx, orgID, entryID, true)
		if err != nil {
			return err
		}
		g, err := s.gateAt(ctx, tx, orgID, v.SiteID, guardToken)
		if err != nil {
			return err
		}
		switch reason := evalEntry(v, now); {
		case v.ApprovalStatus == "requested":
			return problem.Conflict("the resident has not answered yet")
		case reason == ReasonUsedUp:
			return problem.Conflict("the visitor already arrived")
		case reason != ReasonMatch:
			return problem.Conflict("the visitor is not admitted: " + reason)
		}
		if _, err := tx.Exec(ctx, `UPDATE visitor_entry SET arrived_at = $3, arrived_by = $4, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			orgID, entryID, now, g.Person); err != nil {
			return err
		}
		after, err := loadEntry(ctx, tx, orgID, entryID, false)
		if err != nil {
			return err
		}
		out = guardView(after, now)
		if err := gateAudit(ctx, tx, orgID, v.SiteID, g, "gate.arrive", "visitor_entry", entryID, nil); err != nil {
			return err
		}
		return s.visitorChanged(ctx, tx, v.SiteID, "visitor_entry", entryID)
	})
	if err != nil {
		return GuardVisitor{}, tenancy.DBProblem(err, "visitor")
	}
	return out, nil
}

// LeaveGateVisitor marks a visitor who arrived as left (gate.operate).
func (s *Service) LeaveGateVisitor(ctx context.Context, orgID, entryID uuid.UUID, guardToken string) (GuardVisitor, error) {
	now := s.now()
	var out GuardVisitor
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		v, err := loadEntry(ctx, tx, orgID, entryID, true)
		if err != nil {
			return err
		}
		g, err := s.gateAt(ctx, tx, orgID, v.SiteID, guardToken)
		if err != nil {
			return err
		}
		if v.ArrivedAt == nil || v.LeftAt != nil {
			return problem.Conflict("the visitor is not inside")
		}
		if _, err := tx.Exec(ctx, `UPDATE visitor_entry SET left_at = $3, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, entryID, now); err != nil {
			return err
		}
		after, err := loadEntry(ctx, tx, orgID, entryID, false)
		if err != nil {
			return err
		}
		out = guardView(after, now)
		if err := gateAudit(ctx, tx, orgID, v.SiteID, g, "gate.leave", "visitor_entry", entryID, nil); err != nil {
			return err
		}
		return s.visitorChanged(ctx, tx, v.SiteID, "visitor_entry", entryID)
	})
	if err != nil {
		return GuardVisitor{}, tenancy.DBProblem(err, "visitor")
	}
	return out, nil
}

// GetGateVisitorCallContact returns the one number a guard may call when the
// resident does not answer (gate.operate): the gate contact number a resident
// of the unit chose to share, only for a pending request, audited.
func (s *Service) GetGateVisitorCallContact(ctx context.Context, orgID, entryID uuid.UUID, guardToken string) (CallContact, error) {
	now := s.now()
	var out CallContact
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		v, err := loadEntry(ctx, tx, orgID, entryID, false)
		if err != nil {
			return err
		}
		g, err := s.gateAt(ctx, tx, orgID, v.SiteID, guardToken)
		if err != nil {
			return err
		}
		if v.ApprovalStatus != "requested" || v.approvalExpiresAt == nil || !v.approvalExpiresAt.After(now) {
			return problem.Conflict("there is no open request for this visitor")
		}
		si, err := loadSite(ctx, tx, orgID, v.SiteID)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `SELECT p.display_name, p.gate_contact_phone FROM occupancy o JOIN person p ON p.org_id = o.org_id AND p.id = o.person_id
			WHERE o.org_id = $1 AND o.unit_id = $2 AND o.valid_from <= $3::date AND (o.valid_to IS NULL OR o.valid_to > $3::date)
			  AND p.gate_contact_phone IS NOT NULL AND p.gate_contact_phone <> ''
			ORDER BY o.is_correspondent DESC, (o.kind = 'household'), o.valid_from, p.id LIMIT 1`, orgID, v.UnitID, now.In(si.Loc).Format("2006-01-02")).
			Scan(&out.DisplayName, &out.Phone)
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no resident of this unit shared a gate contact number")
		}
		if err != nil {
			return err
		}
		return gateAudit(ctx, tx, orgID, v.SiteID, g, "gate.call_contact_read", "visitor_entry", entryID, nil)
	})
	if err != nil {
		return CallContact{}, tenancy.DBProblem(err, "call contact")
	}
	return out, nil
}

// SearchGatePlates looks up registered resident plates (gate.operate): plate
// and door number only.
func (s *Service) SearchGatePlates(ctx context.Context, orgID, siteID uuid.UUID, q, guardToken string, p tenancy.PageParams) ([]GatePlate, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	norm, ok := tenancy.NormalizePlate(q)
	if !ok && len(norm) == 0 {
		return nil, "", validation("q", "invalid_format")
	}
	type row struct {
		id uuid.UUID
		GatePlate
	}
	var rs []row
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := s.gateAt(ctx, tx, orgID, siteID, guardToken); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT v.id, v.plate, u.door_number FROM vehicle v JOIN unit u ON u.org_id = v.org_id AND u.id = v.unit_id
			WHERE v.org_id = $1 AND v.site_id = $2 AND v.id > $3 AND v.plate LIKE '%' || $4 || '%' ORDER BY v.id LIMIT $5`, orgID, siteID, after, norm, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.Plate, &r.UnitDoorNumber); err != nil {
				return err
			}
			r.Plate = tenancy.FormatPlate(r.Plate)
			rs = append(rs, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "plates")
	}
	rs, next := tenancy.Page(rs, limit, func(r row) uuid.UUID { return r.id })
	out := make([]GatePlate, len(rs))
	for i, r := range rs {
		out[i] = r.GatePlate
	}
	return out, next, nil
}
