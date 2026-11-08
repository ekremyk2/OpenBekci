package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/community"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
)

// bookingCharger implements community.BookingCharger with the billing
// module's one-off charge path: the fee of a booking is a booking_fee charge
// on the unit side (billing.Service.CreateCharge, in the booking's
// transaction), and a refunded cancellation cancels that charge
// (billing.Service.CancelCharge, which reverses its journal entry; payments
// already applied to it stay as credit). A resident books, but the charge is
// the system's posting, so billing runs under the system context.
type bookingCharger struct{ bill *billing.Service }

var _ community.BookingCharger = bookingCharger{}

// ChargeBooking implements community.BookingCharger. A zero fee creates no
// charge (uuid.Nil: the booking keeps no charge id).
func (b bookingCharger) ChargeBooking(ctx context.Context, tx pgx.Tx, c community.BookingCharge) (uuid.UUID, error) {
	if c.Amount.Amount <= 0 {
		return uuid.Nil, nil
	}
	var due string
	if err := tx.QueryRow(ctx, `SELECT (now() AT TIME ZONE timezone)::date::text FROM site WHERE org_id = $1 AND id = $2`, c.OrgID, c.SiteID).Scan(&due); err != nil {
		return uuid.Nil, fmt.Errorf("booking fee: site: %w", err)
	}
	ch, err := b.bill.WithTx(tx).CreateCharge(ledger.SystemContext(ctx, c.OrgID), c.OrgID, billing.ChargeInput{
		SiteID: c.SiteID, UnitID: c.UnitID, ChargeType: "booking_fee", DebtorKind: c.Side,
		Amount:  money.New(c.Amount.Amount, money.Code(c.Amount.Currency)),
		DueDate: due, Description: c.Description,
	})
	if err != nil {
		return uuid.Nil, err
	}
	return ch.ID, nil
}

// RefundBooking implements community.BookingCharger.
func (b bookingCharger) RefundBooking(ctx context.Context, tx pgx.Tx, r community.BookingRefund) error {
	reason := r.Reason
	if reason == "" {
		reason = "booking cancelled"
	}
	_, err := b.bill.WithTx(tx).CancelCharge(ledger.SystemContext(ctx, r.OrgID), r.OrgID, r.ChargeID, reason)
	return err
}
