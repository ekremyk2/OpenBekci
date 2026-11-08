package payments_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/moneyhooks"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func (w *world) plan(t *testing.T, amount int64) {
	t.Helper()
	e, ctx := w.e, w.e.Ctx
	if _, err := w.bill.CreatePlan(ctx, e.OrgID, billing.PlanInput{SiteID: e.SiteID, Name: "Aidat", DueDay: 5, Start: "2026-01-01", AmountMode: "total_allocated", AllocationKey: ledger.Ptr("arsa_payi"), Amount: &amount}); err != nil {
		t.Fatal(err)
	}
}

func (w *world) credit(t *testing.T, d billing.Debtor) (credit, receivable int64) {
	t.Helper()
	e, ctx := w.e, w.e.Ctx
	if err := e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
		ch, err := ledger.EnsureChart(ctx, tx, e.OrgID, e.SiteID)
		if err != nil {
			return err
		}
		if credit, err = billing.CreditOf(ctx, tx, e.OrgID, e.SiteID, ch, d); err != nil {
			return err
		}
		receivable, err = billing.ReceivableOf(ctx, tx, e.OrgID, e.SiteID, ch, d)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return
}

// Move-out carries the unpaid fee net of a waiver, books the closing fee first and
// leaves nothing on the unit side that the next crystallization could reverse again.
func TestIntegrationMoveOutCarryNetsReversals(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx
	w.plan(t, 100000)
	e.Clock.Set("2026-01-01")
	if _, err := w.bill.SetLateFeePolicy(ctx, e.OrgID, e.SiteID, billing.PolicyRow{From: "2026-01-01", RateBPSPerMonth: 500}); err != nil {
		t.Fatal(err)
	}
	e.Clock.Set("2026-04-02")
	if _, err := w.bill.GenerateCharges(ctx, e.OrgID, e.SiteID); err != nil {
		t.Fatal(err)
	}
	if n, err := w.bill.CrystallizeLateFees(ctx, e.OrgID, e.SiteID); err != nil || n == 0 {
		t.Fatalf("crystallize %d %v", n, err)
	}
	if n := e.Int(`SELECT count(*) FROM late_fee_booking WHERE org_id=$1 AND kind='fee' AND unit_id=$2`, e.OrgID, w.a1); n != 3 {
		t.Errorf("one fee booking per closed month expected, got %d", n)
	}
	// running again, and on later days of the same month, books nothing new
	for _, day := range []string{"2026-04-02", "2026-04-09"} {
		e.Clock.Set(day)
		if n, _ := w.bill.CrystallizeLateFees(ctx, e.OrgID, e.SiteID); n != 0 {
			t.Errorf("%s: %d postings in the same month", day, n)
		}
	}
	if _, err := w.bill.WaiveLateFee(ctx, e.OrgID, billing.WaiveInput{SiteID: e.SiteID, UnitID: w.a1, DebtorKind: "owner", Amount: 100, Reason: "goodwill"}); err != nil {
		t.Fatal(err)
	}
	a1 := billing.Debtor{UnitID: &w.a1, Kind: "owner"}
	_, before := w.credit(t, a1)
	w.invariants(t)

	e.Clock.Set("2026-04-10")
	if err := e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
		_, err := moneyhooks.MoveOut{}.OnMoveOut(ctx, tx, people.MoveOutEvent{OrgID: e.OrgID, SiteID: e.SiteID, MoveOutID: uuid.New(), UnitID: w.a1, PersonID: w.o1, Side: "owner", AsOf: "2026-04-10", OpenCharges: "carry_to_person"})
		return err
	}); err != nil {
		t.Fatalf("move-out: %v", err)
	}
	w.invariants(t) // unit side and person receivable equal their open charges
	p := billing.Debtor{PersonID: &w.o1}
	_, carried := w.credit(t, p)
	// the person owes what the unit side owed (net of the waiver) plus the fee of 1-10 April
	if carried < before || carried > before+60000 {
		t.Errorf("carried %d, unit side owed %d", carried, before)
	}
	if _, rec := w.credit(t, a1); rec < 0 {
		t.Errorf("unit side left at %d", rec)
	}
	reversals := e.Int(`SELECT count(*) FROM late_fee_booking WHERE org_id=$1 AND kind='reversal'`, e.OrgID)
	for _, day := range []string{"2026-05-02", "2026-06-02"} {
		e.Clock.Set(day)
		if _, err := w.bill.CrystallizeLateFees(ctx, e.OrgID, e.SiteID); err != nil {
			t.Fatal(err)
		}
		w.invariants(t)
	}
	if got := e.Int(`SELECT count(*) FROM late_fee_booking WHERE org_id=$1 AND kind='reversal'`, e.OrgID); got != reversals {
		t.Errorf("reversals went from %d to %d after the move-out", reversals, got)
	}
	// the person accrues on the carried principal
	if n := e.Int(`SELECT count(*) FROM late_fee_booking WHERE org_id=$1 AND debtor_kind='person' AND kind='fee'`, e.OrgID); n == 0 {
		t.Error("no fee was booked on the carried debt")
	}
	// a move-out dated inside the lock date is refused, never re-dated
	ld, _ := w.led.GetLockDate(ctx, e.OrgID, e.SiteID)
	if _, err := w.led.SetLockDate(ctx, e.OrgID, e.SiteID, ld.Version, ledger.Ptr("2026-05-31")); err != nil {
		t.Fatal(err)
	}
	err := e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
		_, err := moneyhooks.MoveOut{}.OnMoveOut(ctx, tx, people.MoveOutEvent{OrgID: e.OrgID, SiteID: e.SiteID, MoveOutID: uuid.New(), UnitID: w.a2, PersonID: w.o2, Side: "owner", AsOf: "2026-05-20", OpenCharges: "carry_to_person"})
		return err
	})
	if problem.From(err).Code != problem.CodeLockedPeriod {
		t.Errorf("move-out inside the lock: %v", err)
	}
}

// Prepayment credit is consumed by charges at generation: no debtor is dunned or charged a fee while credit covers the principal.
func TestIntegrationPrepaymentCreditIsConsumed(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx
	w.plan(t, 100000)
	e.Clock.Set("2026-01-01")
	if _, err := w.bill.SetLateFeePolicy(ctx, e.OrgID, e.SiteID, billing.PolicyRow{From: "2026-01-01", RateBPSPerMonth: 500}); err != nil {
		t.Fatal(err)
	}
	a2 := billing.Debtor{UnitID: &w.a2, Kind: "owner"}
	if _, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a2, Method: "bank_transfer", Amount: money.New(120000, "TRY"), ReceivedOn: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := w.credit(t, a2); c != 120000 {
		t.Fatalf("credit %d", c)
	}
	e.Clock.Set("2026-03-10")
	if _, err := w.bill.GenerateCharges(ctx, e.OrgID, e.SiteID); err != nil {
		t.Fatal(err)
	}
	if c, rec := w.credit(t, a2); c != 0 || rec != 0 {
		t.Errorf("credit %d receivable %d after generation", c, rec)
	}
	if n := e.Int(`SELECT count(*) FROM charge WHERE org_id=$1 AND unit_id=$2 AND kind='principal' AND status<>'paid'`, e.OrgID, w.a2); n != 0 {
		t.Errorf("%d charges of A-2 are still open", n)
	}
	acc, err := w.bill.AccrueLateFee(ctx, e.OrgID, e.SiteID, a2, "")
	if err != nil || acc.Accrued.Amount != 0 {
		t.Errorf("late fee while credit covered the principal: %+v %v", acc, err)
	}
	w.invariants(t)
	// a payment made while credit exists is allocated after the credit
	e.Clock.Set("2026-04-06")
	if _, err := w.bill.GenerateCharges(ctx, e.OrgID, e.SiteID); err != nil {
		t.Fatal(err)
	}
	p, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a2, Method: "bank_transfer", Amount: money.New(40000, "TRY"), ReceivedOn: "2026-04-06"})
	if err != nil || p.Unallocated.Amount != 0 {
		t.Fatalf("payment %+v %v", p, err)
	}
	w.invariants(t)
}

// The journal API cannot undo what has a typed operation, and manual lines stay off the subledger.
func TestIntegrationJournalProtectsTheSubledger(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx
	e.Clock.Set("2026-01-10")
	a1 := billing.Debtor{UnitID: &w.a1, Kind: "owner"}
	p, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "bank_transfer", Amount: money.New(500, "TRY"), ReceivedOn: "2026-01-10"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.led.ReverseJournalEntry(ctx, e.OrgID, *p.JournalEntryID, ""); problem.From(err).Status != 409 {
		t.Errorf("reversing a payment entry: %v", err)
	}
	var recv, bank, fund uuid.UUID
	_ = e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
		ch, _ := ledger.EnsureChart(ctx, tx, e.OrgID, e.SiteID)
		recv, bank, fund = ch.Account(ledger.SubReceivableOwner), ch.Account(ledger.SubBank), ch.OperatingFund
		return nil
	})
	_, err = w.led.PostManual(ctx, e.OrgID, ledger.ManualEntryInput{SiteID: e.SiteID, EntryDate: "2026-01-10", Lines: []ledger.Line{
		{AccountID: recv, FundID: fund, Debit: 100, UnitID: &w.a1, DebtorKind: "owner"}, {AccountID: bank, FundID: fund, Credit: 100}}})
	if problem.From(err).Status != 422 {
		t.Errorf("manual line on a receivable: %v", err)
	}
	// a line whose fund differs from the account's fund is refused
	var reserve uuid.UUID
	_ = e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
		ch, _ := ledger.EnsureChart(ctx, tx, e.OrgID, e.SiteID)
		reserve = ch.Funds[ledger.FundReserve]
		return nil
	})
	other := ledger.Line{AccountID: bank, FundID: reserve, Debit: 100}
	_, err = w.led.PostManual(ctx, e.OrgID, ledger.ManualEntryInput{SiteID: e.SiteID, EntryDate: "2026-01-10", Lines: []ledger.Line{other, {AccountID: bank, FundID: reserve, Credit: 100}}})
	if problem.From(err).Status != 422 {
		t.Errorf("fund of the bank account: %v", err)
	}
	// the lock date must lie before today; payments may not be dated in the future
	ld, _ := w.led.GetLockDate(ctx, e.OrgID, e.SiteID)
	if _, err := w.led.SetLockDate(ctx, e.OrgID, e.SiteID, ld.Version, ledger.Ptr("2026-01-10")); problem.From(err).Status != 422 {
		t.Errorf("lock date today: %v", err)
	}
	if _, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "bank_transfer", Amount: money.New(500, "TRY"), ReceivedOn: "2026-02-10"}); problem.From(err).Status != 422 {
		t.Errorf("future received_on: %v", err)
	}
}

// A fee reversal larger than the unpaid fee turns the excess into credit; nothing goes negative.
func TestIntegrationReversalIsCappedAtTheUnpaidFee(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx
	w.plan(t, 100000)
	e.Clock.Set("2026-01-01")
	if _, err := w.bill.SetLateFeePolicy(ctx, e.OrgID, e.SiteID, billing.PolicyRow{From: "2026-01-01", RateBPSPerMonth: 500}); err != nil {
		t.Fatal(err)
	}
	e.Clock.Set("2026-03-05")
	if _, err := w.bill.GenerateCharges(ctx, e.OrgID, e.SiteID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.bill.CrystallizeLateFees(ctx, e.OrgID, e.SiteID); err != nil {
		t.Fatal(err)
	}
	a1 := billing.Debtor{UnitID: &w.a1, Kind: "owner"}
	// everything, fees included, is paid
	if _, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "bank_transfer", Amount: money.New(300000, "TRY"), ReceivedOn: "2026-03-05"}); err != nil {
		t.Fatal(err)
	}
	creditBefore, _ := w.credit(t, a1)
	fee := e.Int(`SELECT COALESCE(SUM(amount),0) FROM late_fee_booking WHERE org_id=$1 AND unit_id=$2 AND kind='fee'`, e.OrgID, w.a1)
	if fee == 0 {
		t.Fatal("no fee was booked")
	}
	// a backdated freeze removes the whole fee
	if _, err := w.bill.CreateLateFeeFreeze(ctx, e.OrgID, billing.FreezeInput{SiteID: e.SiteID, UnitID: w.a1, DebtorKind: "owner", From: "2026-01-01", Reason: "legal"}); err != nil {
		t.Fatal(err)
	}
	e.Clock.Set("2026-03-06")
	if _, err := w.bill.CrystallizeLateFees(ctx, e.OrgID, e.SiteID); err != nil {
		t.Fatal(err)
	}
	creditAfter, rec := w.credit(t, a1)
	if rec < 0 {
		t.Errorf("receivable %d: the reversal exceeded the unpaid fee", rec)
	}
	if creditAfter-creditBefore != fee {
		t.Errorf("credit grew by %d, the reversed fee was %d", creditAfter-creditBefore, fee)
	}
	if n := e.Int(`SELECT count(*) FROM charge WHERE org_id=$1 AND kind='fee' AND amount < 0`, e.OrgID); n != 0 {
		t.Errorf("%d negative fee rows although no fee was unpaid", n)
	}
	w.invariants(t)
}
