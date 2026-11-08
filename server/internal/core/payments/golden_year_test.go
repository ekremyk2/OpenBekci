package payments_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/moneyhooks"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/treasury"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// TestIntegrationGoldenYear runs a simulated year through the real services
// with a fake clock: opening balances, monthly dues with a rate change, late
// payments, a backdated payment, a freeze, a waiver, a move-out that carries
// debt to a person, a quarterly lock date, and checks the section 9.13
// invariants at every month end.
func TestIntegrationGoldenYear(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx
	// more units so allocation rounding is exercised: A-3 (tenant from the start), B-1 co-owned
	a3 := e.Unit("A-3", 200)
	extra := e.Person("Owner Three")
	e.Occupy(a3, extra, "owner", "2025-01-01", "")
	_ = a3

	if _, err := w.bill.SetLateFeePolicy(ctx, e.OrgID, e.SiteID, billing.PolicyRow{From: "2026-01-01", RateBPSPerMonth: 500, GraceDays: 5}); err != nil {
		t.Fatal(err)
	}
	// opening balances (as the B importer leaves them) before the year starts
	e.Clock.Set("2026-01-01")
	for _, ob := range []billing.OpeningInput{
		{SiteID: e.SiteID, AsOf: "2025-12-31", Kind: "unit_receivable", UnitID: &w.a1, DebtorKind: "owner", Principal: 45000, LateFee: 1200},
		{SiteID: e.SiteID, AsOf: "2025-12-31", Kind: "unit_receivable", UnitID: &w.a2, DebtorKind: "owner", Principal: -5000},
	} {
		if _, err := w.bill.CreateOpeningBalance(ctx, e.OrgID, ob, false); err != nil {
			t.Fatal(err)
		}
	}
	first, second := int64(120000), int64(132000)
	plan, err := w.bill.CreatePlan(ctx, e.OrgID, billing.PlanInput{SiteID: e.SiteID, Name: "Aidat", DueDay: 5, Start: "2026-01-01", AmountMode: "total_allocated", AllocationKey: ledger.Ptr("arsa_payi"), Amount: &first})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.bill.AddRate(ctx, e.OrgID, plan.ID, billing.RateInput{From: "2026-07-01", Amount: second}); err != nil {
		t.Fatal(err)
	}
	a1 := billing.Debtor{UnitID: &w.a1, Kind: "owner"}
	a2 := billing.Debtor{UnitID: &w.a2, Kind: "owner"}

	// the money that goes out: a cash box, a monthly expense, a transfer, a refund and a budget
	tr := treasury.NewService(e.DB, nil, e.Clock.Now)
	var gch ledger.Chart
	if err := e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) (err error) { gch, err = ledger.EnsureChart(ctx, tx, e.OrgID, e.SiteID); return err }); err != nil {
		t.Fatal(err)
	}
	cashBox := uuid.New()
	e.Exec(`INSERT INTO account (id, org_id, site_id, code, name, type, subtype, fund_id, is_system) VALUES ($1,$2,$3,'1001','Cash box','asset','cash_box',$4,true)`, cashBox, e.OrgID, e.SiteID, gch.OperatingFund)
	bankAcc := gch.Account(ledger.SubBank)
	nop := slog.New(slog.NewTextHandler(io.Discard, nil))
	refunded, transferred := false, false

	start, _ := time.Parse(ledger.DateLayout, "2026-01-01")
	moved, waived, locked := false, false, false
	for day := 0; day < 360; day++ {
		d := start.AddDate(0, 0, day)
		ds := d.Format(ledger.DateLayout)
		e.Clock.Set(ds)
		if _, err := w.bill.GenerateCharges(ctx, e.OrgID, e.SiteID); err != nil {
			t.Fatalf("%s generate: %v", ds, err)
		}
		if day%7 == 0 {
			if _, err := w.bill.CrystallizeLateFees(ctx, e.OrgID, e.SiteID); err != nil {
				t.Fatalf("%s crystallize: %v", ds, err)
			}
			if _, err := tr.RunRecurring(ctx, e.OrgID, e.SiteID, nop); err != nil {
				t.Fatalf("%s recurring expenses: %v", ds, err)
			}
		}
		switch ds {
		case "2026-02-12": // the electricity bill, paid every month on the 12th from here on
			if _, err := tr.CreateExpense(ctx, e.OrgID, treasury.ExpenseInput{SiteID: e.SiteID, CategoryAccountID: gch.AccountCode("5010"), FundID: gch.OperatingFund, Amount: money.New(8000, "TRY"),
				PaidOn: ds, PaidFromAccountID: bankAcc, Description: "Electricity", Recurring: &treasury.Recurrence{Frequency: "monthly", Day: 12}}); err != nil {
				t.Fatal(err)
			}
		case "2026-03-20": // a cash payment into the collector's box, handed over to the bank the next day
			if _, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a2, Method: "cash", Amount: money.New(10000, "TRY"), ReceivedOn: ds, AssetAccountID: &cashBox}); err != nil {
				t.Fatal(err)
			}
		case "2026-03-21":
			if _, err := tr.CreateTransfer(ctx, e.OrgID, treasury.TransferInput{SiteID: e.SiteID, FromAccountID: cashBox, ToAccountID: bankAcc, Amount: money.New(10000, "TRY"), TransferredOn: ds}); err != nil {
				t.Fatal(err)
			}
			transferred = true
		}
		if d.Day() == 15 && d.Month() == time.June && !refunded {
			// A-2 pays more than it owes every month: give half of the credit back
			var credit int64
			_ = e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
				credit, _ = billing.CreditOf(ctx, tx, e.OrgID, e.SiteID, gch, a2)
				return nil
			})
			if credit > 1 {
				if _, err := tr.CreateRefund(ctx, e.OrgID, treasury.RefundInput{SiteID: e.SiteID, UnitID: &w.a2, DebtorKind: "owner", Amount: money.New(credit/2, "TRY"), Reason: "paid too much",
					Kind: "overpayment", PaidFromAccount: bankAcc, RefundedOn: ds}); err != nil {
					t.Fatalf("refund: %v", err)
				}
				refunded = true
			}
		}
		// A-2 pays on time every month on the 10th; A-1 pays every second month, late
		if d.Day() == 10 {
			if _, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a2, Method: "bank_transfer", Amount: money.New(52800, "TRY"), ReceivedOn: ds}); err != nil {
				t.Fatalf("%s pay: %v", ds, err)
			}
			if int(d.Month())%2 == 0 {
				if _, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "bank_transfer", Amount: money.New(150000, "TRY"), ReceivedOn: ds}); err != nil {
					t.Fatalf("%s pay a1: %v", ds, err)
				}
			}
		}
		switch {
		case ds == "2026-05-15": // a backdated payment (received on the 4th) on A-1
			if _, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "bank_transfer", Amount: money.New(10000, "TRY"), ReceivedOn: "2026-05-04"}); err != nil {
				t.Fatal(err)
			}
		case ds == "2026-06-20" && !waived:
			// the booked fee may already be paid: only a refused waiver (409) is acceptable then
			if _, err := w.bill.WaiveLateFee(ctx, e.OrgID, billing.WaiveInput{SiteID: e.SiteID, UnitID: w.a1, DebtorKind: "owner", Amount: 10, Reason: "goodwill"}); err != nil && problem.From(err).Status != 409 {
				t.Fatal(err)
			}
			waived = true
		case ds == "2026-08-01" && !moved: // the tenant of A-2 never existed here; the owner of A-3 sells: carry to the person
			moved = true
			err := e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
				mid := uuid.New()
				_, err := moneyhooks.MoveOut{}.OnMoveOut(ctx, tx, people.MoveOutEvent{OrgID: e.OrgID, SiteID: e.SiteID, MoveOutID: mid, UnitID: a3, PersonID: extra, Side: "owner", AsOf: "2026-08-01", OpenCharges: "carry_to_person"})
				return err
			})
			if err != nil {
				t.Fatalf("move-out: %v", err)
			}
		case ds == "2026-04-02" && !locked:
			locked = true
			ld, _ := w.led.GetLockDate(ctx, e.OrgID, e.SiteID)
			if _, err := w.led.SetLockDate(ctx, e.OrgID, e.SiteID, ld.Version, ledger.Ptr("2026-03-31")); err != nil {
				t.Fatal(err)
			}
			_, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "bank_transfer", Amount: money.New(100, "TRY"), ReceivedOn: "2026-03-15"})
			if problem.From(err).Code != problem.CodeLockedPeriod {
				t.Fatalf("payment inside the lock: %v", err)
			}
		}
		if d.AddDate(0, 0, 1).Day() == 1 {
			w.invariantsFor(t, a3)
		}
	}
	w.invariantsFor(t, a3)
	if !moved || !waived || !locked {
		t.Fatalf("scenario steps skipped: moved=%v waived=%v locked=%v", moved, waived, locked)
	}
	if !refunded || !transferred {
		t.Fatalf("money-out steps skipped: refunded=%v transferred=%v", refunded, transferred)
	}
	// budget versus actual: the lines of the year against the expenses the job posted
	var fyID uuid.UUID
	e.Query([]any{&fyID}, `SELECT id FROM fiscal_year WHERE org_id=$1 AND site_id=$2 AND start_on = '2026-01-01'`, e.OrgID, e.SiteID)
	bud, err := tr.CreateBudget(ctx, e.OrgID, e.SiteID, fyID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.PutBudgetLines(ctx, e.OrgID, bud.ID, bud.Version, []treasury.BudgetLine{{ExpenseCategoryAccountID: gch.AccountCode("5010"), FundID: gch.OperatingFund, Amount: money.New(90000, "TRY")}}); err != nil {
		t.Fatal(err)
	}
	rep, err := tr.BudgetVsActual(ctx, e.OrgID, e.SiteID, fyID)
	if err != nil || len(rep.Rows) != 1 {
		t.Fatalf("budget vs actual %+v %v", rep, err)
	}
	spent := e.Int(`SELECT COALESCE(SUM(amount), 0) FROM expense WHERE org_id=$1 AND status='posted'`, e.OrgID)
	// 12 February to 12 December, once a month
	if spent != 11*8000 || rep.Rows[0].Actual.Amount != spent || rep.Rows[0].Variance.Amount != 90000-spent {
		t.Errorf("expenses %d, report %+v", spent, rep.Rows[0])
	}
	// the cash box holds nothing after the hand-over, the journal balances
	if cash := e.Int(`SELECT COALESCE(SUM(debit - credit), 0) FROM journal_line WHERE org_id=$1 AND account_id=$2`, e.OrgID, cashBox); cash != 0 {
		t.Errorf("cash box holds %d after the hand-over", cash)
	}
	// the move-out turned A-3's debt into person debt and the person receivable matches it
	if n := e.Int(`SELECT count(*) FROM charge WHERE org_id=$1 AND debtor_kind='person'`, e.OrgID); n == 0 {
		t.Error("no charge was carried to the person")
	}
	// rebuilding the statuses changes nothing
	if ch, err := w.bill.RebuildStatus(ctx, e.OrgID, e.SiteID); err != nil || ch != 0 {
		t.Errorf("rebuild changed %d statuses (%v)", ch, err)
	}
	// receipts are gap-free
	count := e.Int(`SELECT count(*) FROM payment WHERE org_id=$1 AND receipt_no IS NOT NULL AND receipt_no LIKE '2026-%'`, e.OrgID)
	last := e.Int(`SELECT last FROM sequence WHERE org_id=$1 AND kind='receipt' AND year=2026`, e.OrgID)
	if count != last {
		t.Errorf("receipt numbers have gaps: %d payments, counter at %d", count, last)
	}
}
