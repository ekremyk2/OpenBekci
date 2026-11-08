package payments_test

import (
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
)

// Cancelling a charge that has payments applied keeps the payments as credit, and voiding such a payment afterwards balances.
func TestIntegrationCancelPaidChargeKeepsPaymentsAsCredit(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx
	e.Clock.Set("2026-02-01")
	a1 := billing.Debtor{UnitID: &w.a1, Kind: "owner"}
	ch, err := w.bill.CreateCharge(ctx, e.OrgID, billing.ChargeInput{SiteID: e.SiteID, UnitID: w.a1, ChargeType: "dues", DebtorKind: "owner", Amount: money.New(30000, "TRY"), DueDate: "2026-02-10", Description: "=cmd one-off"})
	if err != nil {
		t.Fatal(err)
	}
	if ch.EffectiveDebtor.Source != "charge" || ch.EffectiveDebtor.DebtorKind != "owner" {
		t.Errorf("effective debtor %+v", ch.EffectiveDebtor)
	}
	e.Clock.Set("2026-02-12")
	p, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "bank_transfer", Amount: money.New(30000, "TRY"), ReceivedOn: "2026-02-12"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.bill.CancelCharge(ctx, e.OrgID, ch.ID, "billed twice"); err != nil {
		t.Fatal(err)
	}
	if c, rec := w.credit(t, a1); c != 30000 || rec != 0 {
		t.Errorf("credit %d receivable %d after cancelling a paid charge", c, rec)
	}
	w.invariants(t)
	if _, err := w.pay.Void(ctx, e.OrgID, p.ID, "refunded in cash"); err != nil {
		t.Fatal(err)
	}
	if c, rec := w.credit(t, a1); c != 0 || rec != 0 {
		t.Errorf("credit %d receivable %d after voiding", c, rec)
	}
	w.invariants(t)
	_ = ledger.FundOperating
}
