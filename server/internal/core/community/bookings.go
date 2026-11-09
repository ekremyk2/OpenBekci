package community

import (
	"context"
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

// Booking is a facility booking; it is confirmed instantly when it satisfies
// the facility's rules (decision C1).
type Booking struct {
	ID             uuid.UUID  `json:"id"`
	OrgID          uuid.UUID  `json:"org_id"`
	SiteID         uuid.UUID  `json:"site_id"`
	FacilityID     uuid.UUID  `json:"facility_id"`
	FacilityName   string     `json:"facility_name"`
	UnitID         uuid.UUID  `json:"unit_id"`
	UnitDoorNumber string     `json:"unit_door_number"`
	PersonID       *uuid.UUID `json:"person_id"`
	// PersonDisplayName is shown to managers and the booker only (KVKK).
	PersonDisplayName *string    `json:"person_display_name"`
	Start             time.Time  `json:"start"`
	End               time.Time  `json:"end"`
	Status            string     `json:"status"`
	ChargeID          *uuid.UUID `json:"charge_id"`
	Fee               *Money     `json:"fee"`
	Refund            *Money     `json:"refund"`
	CancelledReason   *string    `json:"cancelled_reason"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type bookingAudit struct {
	Facility uuid.UUID  `json:"facility_id"`
	Unit     uuid.UUID  `json:"unit_id"`
	Start    time.Time  `json:"start"`
	End      time.Time  `json:"end"`
	Status   string     `json:"status"`
	Fee      *Money     `json:"fee"`
	Charge   *uuid.UUID `json:"charge_id"`
	Refund   *Money     `json:"refund"`
}

func (b Booking) auditView() bookingAudit {
	return bookingAudit{b.FacilityID, b.UnitID, b.Start, b.End, b.Status, b.Fee, b.ChargeID, b.Refund}
}

const bookingSelect = `SELECT b.id, b.org_id, b.site_id, b.facility_id, f.name, b.unit_id, u.door_number, b.person_id, p.display_name,
		b.start_at, b.end_at, b.status, b.charge_id, b.fee_amount, b.fee_currency, b.refund_amount, b.cancelled_reason, b.created_at, b.updated_at
	FROM booking b
	JOIN facility f ON f.org_id = b.org_id AND f.id = b.facility_id
	JOIN unit u ON u.org_id = b.org_id AND u.id = b.unit_id
	LEFT JOIN person p ON p.org_id = b.org_id AND p.id = b.person_id`

func scanBooking(row pgx.Row) (Booking, error) {
	var b Booking
	var feeAmt, refundAmt *int64
	var feeCur *string
	err := row.Scan(&b.ID, &b.OrgID, &b.SiteID, &b.FacilityID, &b.FacilityName, &b.UnitID, &b.UnitDoorNumber, &b.PersonID, &b.PersonDisplayName,
		&b.Start, &b.End, &b.Status, &b.ChargeID, &feeAmt, &feeCur, &refundAmt, &b.CancelledReason, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return b, err
	}
	if feeAmt != nil && feeCur != nil {
		b.Fee = &Money{Amount: *feeAmt, Currency: *feeCur}
		if refundAmt != nil {
			b.Refund = &Money{Amount: *refundAmt, Currency: *feeCur}
		}
	}
	b.Start, b.End, b.CreatedAt, b.UpdatedAt = b.Start.UTC(), b.End.UTC(), b.CreatedAt.UTC(), b.UpdatedAt.UTC()
	return b, nil
}

// maskBooking removes what the caller may not see: names stay with managers
// and the booker.
func maskBooking(b *Booking, manager bool, callerPerson uuid.UUID) {
	if manager || b.PersonID != nil && *b.PersonID == callerPerson {
		return
	}
	b.PersonDisplayName = nil
}

// currentOccupancy returns the caller's current occupancy on the unit with
// the widest right (owner, then tenant, then household).
func currentOccupancy(ctx context.Context, unitID uuid.UUID) (authz.OccupancyAccess, bool) {
	var best authz.OccupancyAccess
	rank := map[authz.OccupancyKind]int{authz.OccOwner: 3, authz.OccTenant: 2, authz.OccHousehold: 1}
	found := false
	for _, o := range authz.GrantsFromContext(ctx).Current() {
		if o.UnitID == unitID && (!found || rank[o.Kind] > rank[best.Kind]) {
			best, found = o, true
		}
	}
	return best, found
}

// bookingInput is a parsed create or preview body.
type bookingInput struct {
	FacilityID, UnitID uuid.UUID
	Start, End         time.Time
	OnBehalf           uuid.UUID
	Debtor             *string
}

// parseBookingInput reads the body shared by create and preview.
func parseBookingInput(d Doc) (bookingInput, error) {
	var in bookingInput
	if err := d.Get("facility_id", &in.FacilityID); err != nil || in.FacilityID == uuid.Nil {
		return in, validation("facility_id", "required")
	}
	if err := d.Get("unit_id", &in.UnitID); err != nil || in.UnitID == uuid.Nil {
		return in, validation("unit_id", "required")
	}
	if err := d.Get("start", &in.Start); err != nil || in.Start.IsZero() {
		return in, validation("start", "required")
	}
	if err := d.Get("end", &in.End); err != nil || in.End.IsZero() {
		return in, validation("end", "required")
	}
	if !in.End.After(in.Start) {
		return in, validation("end", "end_before_start")
	}
	if d.Has("person_id") && !d.IsNull("person_id") {
		if err := d.Get("person_id", &in.OnBehalf); err != nil {
			return in, err
		}
	}
	if d.Has("debtor_kind") && !d.IsNull("debtor_kind") {
		in.Debtor, _ = d.String("debtor_kind")
		if in.Debtor == nil || *in.Debtor != "owner" && *in.Debtor != "tenant" {
			return in, validation("debtor_kind", "invalid_value")
		}
	}
	return in, nil
}

// bookingEval is what checking a booking request against the facility's rules
// finds, without writing anything: the pure "evaluate" step shared by create
// and preview. Hard refusals (not allowed to book, facility closed, unknown
// unit) come back as errors from evaluateBooking; what is left is data.
type bookingEval struct {
	facility Facility
	unit     unitInfo
	manager  bool
	callerP  uuid.UUID
	personID uuid.UUID
	side     string // debtor side the fee would be charged to
	used     int    // confirmed bookings of the unit in the quota period of start
	quotaFr  time.Time
	quotaTo  time.Time
	// conflict is "" or a 409 reason: "facility_blocked" or "slot_taken".
	conflict string
	// bad are the broken rules, the field problems create would answer with.
	bad []problem.Field
}

// evaluateBooking authorizes the caller for the facility and unit, resolves
// who is charged and checks the request against the rules and the calendar.
// It only reads. In is assumed parsed.
func (s *Service) evaluateBooking(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, in bookingInput, forUpdate bool) (bookingEval, error) {
	var ev bookingEval
	f, err := loadFacility(ctx, tx, orgID, in.FacilityID, forUpdate)
	if err != nil {
		return ev, err
	}
	ev.facility = f
	sc, err := requireAt(ctx, tx, PermBookingsCreate, f.SiteID)
	if err != nil {
		return ev, err
	}
	ev.manager = managerLevel(ctx, sc)
	ev.unit, err = loadUnit(ctx, tx, orgID, f.SiteID, in.UnitID, "unit_id")
	if err != nil {
		return ev, err
	}
	ev.callerP, _, err = callerPerson(ctx, tx, orgID)
	if err != nil {
		return ev, err
	}
	ev.personID = ev.callerP
	if !ev.manager {
		if err := authz.CanActOnUnit(ctx, in.UnitID); err != nil {
			return ev, err
		}
		occ, _ := currentOccupancy(ctx, in.UnitID)
		kind := string(occ.Kind)
		ev.side = string(occ.Side)
		if !f.Rules.MayBook(kind) {
			return ev, problem.Validation(problem.Field{Field: "unit_id", Code: "not_allowed_to_book", Params: map[string]any{"kind": kind}})
		}
		if in.OnBehalf != uuid.Nil && in.OnBehalf != ev.callerP {
			return ev, validation("person_id", "invalid_value")
		}
	} else if in.OnBehalf != uuid.Nil {
		ev.personID = in.OnBehalf
	}
	if !f.Enabled {
		return ev, problem.Conflict("the facility is not open for booking")
	}
	if ev.side == "" {
		ev.side = "owner"
		var tenants int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM occupancy WHERE org_id = $1 AND unit_id = $2 AND kind = 'tenant' AND valid_from <= current_date AND (valid_to IS NULL OR valid_to > current_date)`,
			orgID, in.UnitID).Scan(&tenants); err != nil {
			return ev, err
		}
		if tenants > 0 {
			ev.side = "tenant"
		}
	}
	if in.Debtor != nil {
		if !ev.manager {
			if err := authz.CanAccessUnit(ctx, in.UnitID, authz.UnitSide(*in.Debtor)); err != nil {
				return ev, validation("debtor_kind", "not_allowed")
			}
		}
		ev.side = *in.Debtor
	}
	si, err := loadSite(ctx, tx, orgID, f.SiteID)
	if err != nil {
		return ev, err
	}
	if f.Rules.QuotaPerUnit != nil && f.Rules.QuotaPeriod != nil {
		ev.quotaFr, ev.quotaTo = PeriodBounds(*f.Rules.QuotaPeriod, in.Start, si.Loc)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM booking WHERE org_id = $1 AND facility_id = $2 AND unit_id = $3 AND status = 'confirmed' AND start_at >= $4 AND start_at < $5`,
			orgID, in.FacilityID, in.UnitID, ev.quotaFr, ev.quotaTo).Scan(&ev.used); err != nil {
			return ev, err
		}
	}
	var blocked, taken int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM facility_block WHERE org_id = $1 AND facility_id = $2 AND tstzrange(start_at, end_at, '[)') && tstzrange($3, $4, '[)')`,
		orgID, in.FacilityID, in.Start, in.End).Scan(&blocked); err != nil {
		return ev, err
	}
	if blocked > 0 {
		ev.conflict = "facility_blocked"
	} else {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM booking WHERE org_id = $1 AND facility_id = $2 AND status = 'confirmed' AND tstzrange(start_at, end_at, '[)') && tstzrange($3, $4, '[)')`,
			orgID, in.FacilityID, in.Start, in.End).Scan(&taken); err != nil {
			return ev, err
		}
		if taken > 0 {
			ev.conflict = "slot_taken"
		}
	}
	ev.bad = f.Rules.CheckBooking(s.now(), in.Start, in.End, si.Loc, ev.used)
	if ev.manager {
		// managers booking on behalf of a unit may go beyond the lead time, the horizon and the quota
		ev.bad = keepDuration(ev.bad)
	}
	return ev, nil
}

// conflictProblem is the 409 create answers with for a calendar conflict.
func conflictProblem(reason string) error {
	if reason == "facility_blocked" {
		return problem.Conflict("the facility is blocked at that time")
	}
	return problem.Conflict("that time is already booked")
}

// CreateBooking books a facility (bookings.create). Residents book for their
// own unit; board and managers may book on behalf of any unit of the site.
func (s *Service) CreateBooking(ctx context.Context, orgID uuid.UUID, d Doc) (Booking, error) {
	in, err := parseBookingInput(d)
	if err != nil {
		return Booking{}, err
	}
	var out Booking
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		ev, err := s.evaluateBooking(ctx, tx, orgID, in, true)
		if err != nil {
			return err
		}
		f, unit, manager, callerP, side := ev.facility, ev.unit, ev.manager, ev.callerP, ev.side
		// the order create always had: a blocked date, then the rules, then a taken slot
		if ev.conflict == "facility_blocked" {
			return conflictProblem(ev.conflict)
		}
		if len(ev.bad) > 0 {
			return problem.Validation(ev.bad...)
		}
		if ev.conflict != "" {
			return conflictProblem(ev.conflict)
		}
		var personArg *uuid.UUID
		if ev.personID != uuid.Nil {
			personArg = &ev.personID
		}
		bookingID := id.New()
		var feeAmt *int64
		var feeCur *string
		if f.Rules.Fee != nil {
			feeAmt, feeCur = &f.Rules.Fee.Amount, &f.Rules.Fee.Currency
		}
		if _, err := tx.Exec(ctx, `INSERT INTO booking (id, org_id, site_id, facility_id, unit_id, person_id, start_at, end_at, debtor_kind, fee_amount, fee_currency, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			bookingID, orgID, f.SiteID, in.FacilityID, in.UnitID, personArg, in.Start, in.End, side, feeAmt, feeCur, principalUser(ctx)); err != nil {
			if isExclusion(err) {
				return problem.Conflict("that time is already booked")
			}
			return err
		}
		if f.Rules.Fee != nil {
			cid, err := s.cfg.Charger.ChargeBooking(ctx, tx, BookingCharge{
				OrgID: orgID, SiteID: f.SiteID, UnitID: in.UnitID, BookingID: bookingID, Side: side,
				Amount: *f.Rules.Fee, Description: fmt.Sprintf("%s, %s", f.Name, unit.DoorNumber),
			})
			if err != nil {
				return fmt.Errorf("charge booking fee: %w", err)
			}
			if cid != uuid.Nil {
				if _, err := tx.Exec(ctx, `UPDATE booking SET charge_id = $3 WHERE org_id = $1 AND id = $2`, orgID, bookingID, cid); err != nil {
					return err
				}
			}
		}
		out, err = scanBooking(tx.QueryRow(ctx, bookingSelect+` WHERE b.org_id = $1 AND b.id = $2`, orgID, bookingID))
		if err != nil {
			return err
		}
		maskBooking(&out, manager, callerP)
		diff, err := audit.Diff("booking", bookingAudit{}, out.auditView())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &f.SiteID, Action: "booking.create", TargetType: "booking", TargetID: bookingID, Diff: diff})
	})
	if err != nil {
		return Booking{}, tenancy.DBProblem(err, "booking")
	}
	return out, nil
}

// BookingPreviewFee is the fee a booking would post and to whom.
type BookingPreviewFee struct {
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
	DebtorKind string `json:"debtor_kind"`
}

// BookingRefundRule is the facility's cancellation rule as it would apply to
// the previewed booking.
type BookingRefundRule struct {
	CancelRefundCutoffHours *int       `json:"cancel_refund_cutoff_hours"`
	RefundableUntil         *time.Time `json:"refundable_until"`
}

// BookingQuota is the unit's quota at the facility in the period of the
// previewed start (a nil limit means no quota).
type BookingQuota struct {
	Limit      *int       `json:"limit"`
	Period     *string    `json:"period"`
	Used       int        `json:"used"`
	Remaining  *int       `json:"remaining"`
	PeriodFrom *time.Time `json:"period_from"`
	PeriodTo   *time.Time `json:"period_to"`
}

// BookingPreview is what creating the booking would do, computed without
// writing anything.
type BookingPreview struct {
	FacilityID uuid.UUID          `json:"facility_id"`
	UnitID     uuid.UUID          `json:"unit_id"`
	Start      time.Time          `json:"start"`
	End        time.Time          `json:"end"`
	Fee        *BookingPreviewFee `json:"fee"`
	RefundRule BookingRefundRule  `json:"refund_rule"`
	Quota      BookingQuota       `json:"quota"`
	// Violations are the field problems create would answer 422 with.
	Violations []problem.Field `json:"violations"`
	// Conflict is the 409 create would answer with: facility_blocked, slot_taken or null.
	Conflict *string `json:"conflict"`
	// Bookable is true when create would succeed now.
	Bookable bool `json:"bookable"`
}

// PreviewBooking evaluates a booking request exactly as CreateBooking would
// (bookings.create, same visibility and refusals) and returns the fee, the
// refund rule, the quota left and the rule violations, writing nothing.
func (s *Service) PreviewBooking(ctx context.Context, orgID uuid.UUID, d Doc) (BookingPreview, error) {
	in, err := parseBookingInput(d)
	if err != nil {
		return BookingPreview{}, err
	}
	out := BookingPreview{FacilityID: in.FacilityID, UnitID: in.UnitID, Start: in.Start.UTC(), End: in.End.UTC(), Violations: []problem.Field{}}
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		ev, err := s.evaluateBooking(ctx, tx, orgID, in, false)
		if err != nil {
			return err
		}
		r := ev.facility.Rules
		if r.Fee != nil {
			out.Fee = &BookingPreviewFee{Amount: r.Fee.Amount, Currency: r.Fee.Currency, DebtorKind: ev.side}
			out.RefundRule.CancelRefundCutoffHours = r.CancelRefundCutoffHours
			if r.CancelRefundCutoffHours != nil {
				until := in.Start.Add(-time.Duration(*r.CancelRefundCutoffHours) * time.Hour).UTC()
				out.RefundRule.RefundableUntil = &until
			}
		}
		if r.QuotaPerUnit != nil && r.QuotaPeriod != nil {
			rem := max(*r.QuotaPerUnit-ev.used, 0)
			fr, to := ev.quotaFr.UTC(), ev.quotaTo.UTC()
			out.Quota = BookingQuota{Limit: r.QuotaPerUnit, Period: r.QuotaPeriod, Used: ev.used, Remaining: &rem, PeriodFrom: &fr, PeriodTo: &to}
		}
		out.Violations = append(out.Violations, ev.bad...)
		if ev.conflict != "" {
			out.Conflict = &ev.conflict
		}
		out.Bookable = len(ev.bad) == 0 && ev.conflict == ""
		return nil
	})
	if err != nil {
		return BookingPreview{}, tenancy.DBProblem(err, "booking")
	}
	return out, nil
}

// keepDuration keeps the problems managers cannot override: a booking that
// ends before it starts, is longer than allowed or is outside opening hours.
func keepDuration(in []problem.Field) []problem.Field {
	var out []problem.Field
	for _, f := range in {
		if f.Code == "too_long" || f.Code == "end_before_start" || f.Code == "outside_opening_hours" {
			out = append(out, f)
		}
	}
	return out
}

// BookingFilter narrows ListBookings.
type BookingFilter struct {
	SiteID     uuid.UUID
	FacilityID *uuid.UUID
	UnitID     *uuid.UUID
	Status     string
	From, To   *time.Time
	Mine       bool
}

// ListBookings lists a site's bookings (bookings.read): managers see all,
// residents the bookings of their own unit.
func (s *Service) ListBookings(ctx context.Context, orgID uuid.UUID, f BookingFilter, p tenancy.PageParams) ([]Booking, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Booking
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := requireAt(ctx, tx, PermBookingsRead, f.SiteID)
		if err != nil {
			return err
		}
		manager := managerLevel(ctx, sc)
		callerP, _, err := callerPerson(ctx, tx, orgID)
		if err != nil {
			return err
		}
		var units []uuid.UUID
		if !manager {
			units = residentUnits(ctx, f.SiteID)
			if f.UnitID != nil {
				if err := authz.CanActOnUnit(ctx, *f.UnitID); err != nil {
					return err
				}
			}
		}
		var mine *uuid.UUID
		if f.Mine {
			mine = &callerP
		}
		rows, err := tx.Query(ctx, bookingSelect+` WHERE b.org_id = $1 AND b.site_id = $2 AND b.id > $3
			AND ($4::uuid IS NULL OR b.facility_id = $4) AND ($5::uuid IS NULL OR b.unit_id = $5)
			AND ($6 = '' OR b.status = $6) AND ($7::timestamptz IS NULL OR b.end_at >= $7) AND ($8::timestamptz IS NULL OR b.start_at <= $8)
			AND ($9::uuid IS NULL OR b.person_id = $9) AND ($10 OR b.unit_id = ANY($11))
			ORDER BY b.id LIMIT $12`,
			orgID, f.SiteID, after, f.FacilityID, f.UnitID, f.Status, f.From, f.To, mine, manager, units, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanBooking(rows)
			if err != nil {
				return err
			}
			maskBooking(&b, manager, callerP)
			out = append(out, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list bookings")
	}
	items, next := tenancy.Page(out, limit, func(b Booking) uuid.UUID { return b.ID })
	return items, next, nil
}

// GetBooking reads a booking (bookings.read; residents only their unit's).
func (s *Service) GetBooking(ctx context.Context, orgID, bookingID uuid.UUID) (Booking, error) {
	var out Booking
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		b, err := scanBooking(tx.QueryRow(ctx, bookingSelect+` WHERE b.org_id = $1 AND b.id = $2`, orgID, bookingID))
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such booking")
		}
		if err != nil {
			return err
		}
		sc, err := requireAt(ctx, tx, PermBookingsRead, b.SiteID)
		if err != nil {
			return err
		}
		manager := managerLevel(ctx, sc)
		if !manager && authz.CanActOnUnit(ctx, b.UnitID) != nil {
			return problem.NotFound("no such booking")
		}
		callerP, _, err := callerPerson(ctx, tx, orgID)
		if err != nil {
			return err
		}
		maskBooking(&b, manager, callerP)
		out = b
		return nil
	})
	if err != nil {
		return Booking{}, tenancy.DBProblem(err, "booking")
	}
	return out, nil
}

// CancelBooking cancels a booking (bookings.manage, or bookings.create for a
// booking of one's own unit). The fee is refunded automatically when a
// resident cancels before the facility's cutoff, and always when a manager
// cancels.
func (s *Service) CancelBooking(ctx context.Context, orgID, bookingID uuid.UUID, reason string) (Booking, error) {
	now := s.now()
	var out Booking
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		b, err := scanBooking(tx.QueryRow(ctx, bookingSelect+` WHERE b.org_id = $1 AND b.id = $2 FOR UPDATE OF b`, orgID, bookingID))
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such booking")
		}
		if err != nil {
			return err
		}
		sc, err := siteScope(ctx, tx, b.SiteID)
		if err != nil {
			return err
		}
		manager := authz.Has(ctx, PermBookingsManage, sc)
		if !manager {
			if err := authz.Require(ctx, PermBookingsCreate, sc); err != nil {
				return err
			}
			if authz.CanActOnUnit(ctx, b.UnitID) != nil {
				return problem.NotFound("no such booking")
			}
		}
		if b.Status != "confirmed" {
			return problem.Conflict("the booking is already cancelled")
		}
		if !b.End.After(now) {
			return problem.Conflict("the booking is over")
		}
		f, err := loadFacility(ctx, tx, orgID, b.FacilityID, false)
		if err != nil {
			return err
		}
		callerP, _, err := callerPerson(ctx, tx, orgID)
		if err != nil {
			return err
		}
		var refund *Money
		if b.Fee != nil && b.ChargeID != nil && (manager || f.Rules.RefundDue(now, b.Start)) {
			refund = b.Fee
			if err := s.cfg.Charger.RefundBooking(ctx, tx, BookingRefund{OrgID: orgID, SiteID: b.SiteID, BookingID: b.ID, ChargeID: *b.ChargeID, Amount: *b.Fee, Reason: reason}); err != nil {
				return fmt.Errorf("refund booking fee: %w", err)
			}
		}
		var refundAmt *int64
		if refund != nil {
			refundAmt = &refund.Amount
		}
		var reasonArg *string
		if reason != "" {
			reasonArg = &reason
		}
		if _, err := tx.Exec(ctx, `UPDATE booking SET status = 'cancelled', cancelled_reason = $3, cancelled_by = $4, refund_amount = $5, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2`, orgID, bookingID, reasonArg, nilIfZero(callerP), refundAmt); err != nil {
			return err
		}
		after, err := scanBooking(tx.QueryRow(ctx, bookingSelect+` WHERE b.org_id = $1 AND b.id = $2`, orgID, bookingID))
		if err != nil {
			return err
		}
		maskBooking(&after, manager, callerP)
		out = after
		diff, err := audit.Diff("booking", b.auditView(), after.auditView())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &b.SiteID, Action: "booking.cancel", TargetType: "booking", TargetID: b.ID, Diff: diff})
	})
	if err != nil {
		return Booking{}, tenancy.DBProblem(err, "booking")
	}
	return out, nil
}

func nilIfZero(u uuid.UUID) *uuid.UUID {
	if u == uuid.Nil {
		return nil
	}
	return &u
}
