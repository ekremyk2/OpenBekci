package sdktest_test

import (
	"encoding/json"
	"testing"

	"github.com/openbekci/openbekci/packs/example"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/checkout"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/sdktest"
)

// The payment holds what the provider confirmed. A different amount than the checkout asked for is recorded as
// confirmed, flagged on the checkout and audited once.
func TestCheckoutPaidAmountMismatchIsRecordedFlaggedAndAudited(t *testing.T) {
	e := newCheckoutEnv(t)
	e.charge("owner", 50000, "c")
	c := e.create("owner") // asks for 50000

	if code := e.paid(c, 40000, 400); code != 200 {
		t.Fatalf("webhook: %d", code)
	}
	got := e.get(c)
	if got.Status != checkout.StatusPaid || got.PaymentID == nil || !got.AmountMismatch || got.ConfirmedAmount == nil || got.ConfirmedAmount.Amount != 40000 || got.Amount.Amount != 50000 {
		t.Fatalf("checkout after the webhook = %+v", got)
	}
	if n := e.Env.Int(`SELECT count(*) FROM payment WHERE id = $1 AND gross = 40000 AND fee = 400 AND net = 39600`, *got.PaymentID); n != 1 {
		t.Fatal("the payment does not hold the amount the provider confirmed")
	}
	// the charge stays partly open: 10000 of it is still owed
	if n := e.Env.Int(`SELECT coalesce(sum(amount), 0) FROM payment_allocation WHERE payment_id = $1 AND NOT voided`, *got.PaymentID); n != 40000 {
		t.Errorf("allocated %d, want 40000", n)
	}
	if code := e.paid(c, 40000, 400); code != 200 {
		t.Fatalf("replay: %d", code)
	}
	if n := e.Env.Int(`SELECT count(*) FROM audit_event WHERE org_id = $1 AND action = 'checkout.amount_mismatch' AND target_id = $2`, e.Env.OrgID, c.ID); n != 1 {
		t.Errorf("amount_mismatch audit events = %d, want 1", n)
	}
	// a matching payment is not flagged
	ok := e.create("owner")
	e.paid(ok, 10000, 0)
	if g := e.get(ok); g.AmountMismatch || g.ConfirmedAmount != nil {
		t.Errorf("a matching payment was flagged: %+v", g)
	}
	e.AssertLedgerInvariants()
}

// An event that confirms no amount is refused; the checkout's own amount is not what was paid.
func TestCheckoutPaidWithoutConfirmedAmountIsRefused(t *testing.T) {
	e := newCheckoutEnv(t)
	e.charge("owner", 50000, "c")
	c := e.create("owner")
	if code := e.paid(c, 0, 0); code == 200 {
		t.Fatal("a paid event without a gross was accepted")
	}
	if n := e.Env.Int(`SELECT count(*) FROM payment WHERE org_id = $1`, e.Env.OrgID); n != 0 {
		t.Errorf("%d payments recorded from an event that confirmed no amount", n)
	}
	if n := e.Env.Int(`SELECT count(*) FROM checkout WHERE id = $1 AND status = 'pending'`, c.ID); n != 1 {
		t.Error("the checkout left pending")
	}
}

// A checkout paid after another payment settled its charges still records the money (oldest first, surplus as
// credit) instead of bouncing the webhook.
func TestCheckoutPaidAfterItsChargesWereSettledStillRecords(t *testing.T) {
	e := newCheckoutEnv(t)
	charge := e.charge("owner", 30000, "c")
	first := e.create("owner", charge)
	second := e.create("owner", charge)
	if code := e.paid(second, 30000, 0); code != 200 {
		t.Fatalf("first payment: %d", code)
	}
	if code := e.paid(first, 30000, 0); code != 200 {
		t.Fatalf("payment of a checkout whose charge is settled: %d", code)
	}
	g := e.get(first)
	if g.Status != checkout.StatusPaid || g.PaymentID == nil {
		t.Fatalf("checkout = %+v", g)
	}
	if n := e.Env.Int(`SELECT count(*) FROM payment_allocation WHERE payment_id = $1 AND NOT voided`, *g.PaymentID); n != 0 {
		t.Errorf("a settled charge was allocated again (%d allocations)", n)
	}
	e.AssertLedgerInvariants()
}

// chargeDue creates a charge of the unit with the given due date (a charge not yet overdue accrues no late fee).
func (e *cenv) chargeDue(side string, amount int64, due, desc string) {
	e.T.Helper()
	_, err := e.Billing.CreateCharge(ledger.SystemContext(e.Ctx(), e.Env.OrgID), e.Env.OrgID, billing.ChargeInput{SiteID: e.Env.SiteID, UnitID: e.unit, ChargeType: "special_assessment",
		DebtorKind: side, Amount: money.New(amount, "TRY"), DueDate: due, Description: desc})
	if err != nil {
		e.T.Fatal(err)
	}
}

// A confirmation dated in a locked period is booked today (the lock date is never breached), one dated in the
// future too.
func TestCheckoutPaidDatesRespectTheLockDate(t *testing.T) {
	e := newCheckoutEnv(t)
	now := e.Env.Clock.Now().UTC()
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")
	e.chargeDue("owner", 90000, tomorrow, "c")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	e.Env.Exec(`UPDATE site SET lock_date = $2::date WHERE id = $1`, e.Env.SiteID, yesterday)

	c1 := e.create("owner")
	paidOn := func(c checkout.Checkout, gross int64, day string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"event": "paid", "txn": e.txnOf(c), "gross": gross, "fee": 0, "currency": "TRY", "occurred_on": day})
		if rec := e.Webhook("example", raw, sdktest.Header(example.SignatureHeader, example.Sign(whSec, raw))); rec.Code != 200 {
			t.Fatalf("webhook dated %s: %d %s", day, rec.Code, rec.Body.String())
		}
	}
	paidOn(c1, 90000, yesterday)
	pid := *e.get(c1).PaymentID
	if n := e.Env.Int(`SELECT count(*) FROM payment p JOIN site s ON s.id = p.site_id WHERE p.id = $1 AND p.received_on > s.lock_date`, pid); n != 1 {
		t.Error("a payment confirmed for a locked day was dated inside the locked period")
	}
	e.chargeDue("owner", 5000, tomorrow, "d")
	c2 := e.create("owner")
	paidOn(c2, 5000, now.AddDate(0, 0, 3).Format("2006-01-02"))
	pid2 := *e.get(c2).PaymentID
	if n := e.Env.Int(`SELECT count(*) FROM payment WHERE id = $1 AND received_on <= $2::date + 1`, pid2, now.Format("2006-01-02")); n != 1 {
		t.Error("a payment confirmed for a day in the future was dated ahead")
	}
	e.AssertLedgerInvariants()
}
