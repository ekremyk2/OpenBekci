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

// CreateBooking books a facility (bookings.create). Residents book for their
// own unit; board and managers may book on behalf of any unit of the site.
func (s *Service) CreateBooking(ctx context.Context, orgID uuid.UUID, d Doc) (Booking, error) {
	var facilityID, unitID uuid.UUID
	var start, end time.Time
	if err := d.Get("facility_id", &facilityID); err != nil || facilityID == uuid.Nil {
		return Booking{}, validation("facility_id", "required")
	}
	if err := d.Get("unit_id", &unitID); err != nil || unitID == uuid.Nil {
		return Booking{}, validation("unit_id", "required")
	}
	if err := d.Get("start", &start); err != nil || start.IsZero() {
		return Booking{}, validation("start", "required")
	}
	if err := d.Get("end", &end); err != nil || end.IsZero() {
		return Booking{}, validation("end", "required")
	}
	var onBehalf uuid.UUID
	if d.Has("person_id") && !d.IsNull("person_id") {
		if err := d.Get("person_id", &onBehalf); err != nil {
			return Booking{}, err
		}
	}
	var debtor *string
	if d.Has("debtor_kind") && !d.IsNull("debtor_kind") {
		debtor, _ = d.String("debtor_kind")
		if debtor == nil || *debtor != "owner" && *debtor != "tenant" {
			return Booking{}, validation("debtor_kind", "invalid_value")
		}
	}
	now := s.now()
	var out Booking
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		f, err := loadFacility(ctx, tx, orgID, facilityID, true)
		if err != nil {
			return err
		}
		sc, err := requireAt(ctx, tx, PermBookingsCreate, f.SiteID)
		if err != nil {
			return err
		}
		manager := managerLevel(ctx, sc)
		unit, err := loadUnit(ctx, tx, orgID, f.SiteID, unitID, "unit_id")
		if err != nil {
			return err
		}
		callerP, _, err := callerPerson(ctx, tx, orgID)
		if err != nil {
			return err
		}
		personID := callerP
		kind, side := "", ""
		if !manager {
			if err := authz.CanActOnUnit(ctx, unitID); err != nil {
				return err
			}
			occ, _ := currentOccupancy(ctx, unitID)
			kind, side = string(occ.Kind), string(occ.Side)
			if !f.Rules.MayBook(kind) {
				return problem.Validation(problem.Field{Field: "unit_id", Code: "not_allowed_to_book", Params: map[string]any{"kind": kind}})
			}
			if onBehalf != uuid.Nil && onBehalf != callerP {
				return validation("person_id", "invalid_value")
			}
		} else if onBehalf != uuid.Nil {
			personID = onBehalf
		}
		if !f.Enabled {
			return problem.Conflict("the facility is not open for booking")
		}
		if side == "" {
			side = "owner"
			var tenants int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM occupancy WHERE org_id = $1 AND unit_id = $2 AND kind = 'tenant' AND valid_from <= current_date AND (valid_to IS NULL OR valid_to > current_date)`,
				orgID, unitID).Scan(&tenants); err != nil {
				return err
			}
			if tenants > 0 {
				side = "tenant"
			}
		}
		if debtor != nil {
			if !manager {
				if err := authz.CanAccessUnit(ctx, unitID, authz.UnitSide(*debtor)); err != nil {
					return validation("debtor_kind", "not_allowed")
				}
			}
			side = *debtor
		}
		si, err := loadSite(ctx, tx, orgID, f.SiteID)
		if err != nil {
			return err
		}
		used := 0
		if f.Rules.QuotaPerUnit != nil && f.Rules.QuotaPeriod != nil {
			pf, pt := PeriodBounds(*f.Rules.QuotaPeriod, start, si.Loc)
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM booking WHERE org_id = $1 AND facility_id = $2 AND unit_id = $3 AND status = 'confirmed' AND start_at >= $4 AND start_at < $5`,
				orgID, facilityID, unitID, pf, pt).Scan(&used); err != nil {
				return err
			}
		}
		if bad := f.Rules.CheckBooking(now, start, end, si.Loc, used); len(bad) > 0 {
			if manager {
				// managers booking on behalf of a unit may go beyond the lead time, the horizon and the quota
				bad = keepDuration(bad)
			}
			if len(bad) > 0 {
				return problem.Validation(bad...)
			}
		}
		var blocked int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM facility_block WHERE org_id = $1 AND facility_id = $2 AND tstzrange(start_at, end_at, '[)') && tstzrange($3, $4, '[)')`,
			orgID, facilityID, start, end).Scan(&blocked); err != nil {
			return err
		}
		if blocked > 0 {
			return problem.Conflict("the facility is blocked at that time")
		}
		var personArg *uuid.UUID
		if personID != uuid.Nil {
			personArg = &personID
		}
		bookingID := id.New()
		var feeAmt *int64
		var feeCur *string
		if f.Rules.Fee != nil {
			feeAmt, feeCur = &f.Rules.Fee.Amount, &f.Rules.Fee.Currency
		}
		if _, err := tx.Exec(ctx, `INSERT INTO booking (id, org_id, site_id, facility_id, unit_id, person_id, start_at, end_at, debtor_kind, fee_amount, fee_currency, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			bookingID, orgID, f.SiteID, facilityID, unitID, personArg, start, end, side, feeAmt, feeCur, principalUser(ctx)); err != nil {
			if isExclusion(err) {
				return problem.Conflict("that time is already booked")
			}
			return err
		}
		if f.Rules.Fee != nil {
			cid, err := s.cfg.Charger.ChargeBooking(ctx, tx, BookingCharge{
				OrgID: orgID, SiteID: f.SiteID, UnitID: unitID, BookingID: bookingID, Side: side,
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
		if b.Fee != nil && b.ChargeID != nil && (manager && b.PersonID != nil && *b.PersonID != callerP || f.Rules.RefundDue(now, b.Start)) {
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
