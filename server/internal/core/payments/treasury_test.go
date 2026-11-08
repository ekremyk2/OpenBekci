package payments_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/core/treasury"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

type tw struct {
	*world
	tr   *treasury.Service
	ch   ledger.Chart
	cash uuid.UUID
}

func newTW(t *testing.T) *tw {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx
	x := &tw{world: w, tr: treasury.NewService(e.DB, nil, e.Clock.Now)}
	if err := e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) (err error) {
		x.ch, err = ledger.EnsureChart(ctx, tx, e.OrgID, e.SiteID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	x.cash = id.New()
	e.Exec(`INSERT INTO account (id, org_id, site_id, code, name, type, subtype, fund_id, is_system) VALUES ($1,$2,$3,'1001','Cash box','asset','cash_box',$4,true)`, x.cash, e.OrgID, e.SiteID, x.ch.OperatingFund)
	return x
}

func (x *tw) bal(t *testing.T, account uuid.UUID) int64 {
	t.Helper()
	var v int64
	if err := x.e.DB.InTenantTx(x.e.Ctx, x.e.OrgID, func(tx pgx.Tx) error {
		d, c, err := ledger.Balance(x.e.Ctx, tx, x.e.OrgID, x.e.SiteID, account, ledger.BalanceFilter{})
		v = d - c
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return v
}

func TRY(n int64) money.Money { return money.New(n, "TRY") }

func TestIntegrationTransfers(t *testing.T) {
	x := newTW(t)
	e, ctx := x.e, x.e.Ctx
	e.Clock.Set("2026-02-10")
	a1 := billing.Debtor{UnitID: &x.a1, Kind: "owner"}
	// cash paid to the collector's box
	if _, err := x.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "cash", Amount: TRY(80000), ReceivedOn: "2026-02-09", AssetAccountID: &x.cash}); err != nil {
		t.Fatal(err)
	}
	bank := x.ch.Account(ledger.SubBank)
	tr, err := x.tr.CreateTransfer(ctx, e.OrgID, treasury.TransferInput{SiteID: e.SiteID, FromAccountID: x.cash, ToAccountID: bank, Amount: TRY(50000), TransferredOn: "2026-02-10"})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Kind != "bank_deposit" {
		t.Errorf("cash to bank is a deposit, got %s", tr.Kind)
	}
	if got := x.bal(t, x.cash); got != 30000 {
		t.Errorf("cash box %d", got)
	}
	if got := x.bal(t, bank); got != 50000 {
		t.Errorf("bank %d", got)
	}
	// into another cash box: a hand-over
	box2 := id.New()
	e.Exec(`INSERT INTO account (id, org_id, site_id, code, name, type, subtype, fund_id, is_system) VALUES ($1,$2,$3,'1002','Treasurer','asset','cash_box',$4,true)`, box2, e.OrgID, e.SiteID, x.ch.OperatingFund)
	if h, err := x.tr.CreateTransfer(ctx, e.OrgID, treasury.TransferInput{SiteID: e.SiteID, FromAccountID: x.cash, ToAccountID: box2, Amount: TRY(30000), TransferredOn: "2026-02-10"}); err != nil || h.Kind != "cash_handover" {
		t.Fatalf("hand-over %+v %v", h, err)
	}
	// refusals: same account, a receivable, a future date, another fund, a locked day
	bad := []treasury.TransferInput{
		{SiteID: e.SiteID, FromAccountID: bank, ToAccountID: bank, Amount: TRY(1), TransferredOn: "2026-02-10"},
		{SiteID: e.SiteID, FromAccountID: x.cash, ToAccountID: x.ch.Account(ledger.SubReceivableOwner), Amount: TRY(1), TransferredOn: "2026-02-10"},
		{SiteID: e.SiteID, FromAccountID: x.cash, ToAccountID: bank, Amount: TRY(1), TransferredOn: "2026-03-10"},
		{SiteID: e.SiteID, FromAccountID: x.cash, ToAccountID: bank, Amount: money.New(1, "USD"), TransferredOn: "2026-02-10"},
	}
	for i, in := range bad {
		if _, err := x.tr.CreateTransfer(ctx, e.OrgID, in); problem.From(err).Status != 422 {
			t.Errorf("refusal %d: %v", i, err)
		}
	}
	ld, _ := x.led.GetLockDate(ctx, e.OrgID, e.SiteID)
	if _, err := x.led.SetLockDate(ctx, e.OrgID, e.SiteID, ld.Version, ledger.Ptr("2026-02-09")); err != nil {
		t.Fatal(err)
	}
	if _, err := x.tr.CreateTransfer(ctx, e.OrgID, treasury.TransferInput{SiteID: e.SiteID, FromAccountID: x.cash, ToAccountID: bank, Amount: TRY(1), TransferredOn: "2026-02-09"}); problem.From(err).Code != problem.CodeLockedPeriod {
		t.Errorf("transfer inside the lock: %v", err)
	}
	if items, _, err := x.tr.ListTransfers(ctx, e.OrgID, e.SiteID, treasury.TransferFilter{AccountID: &x.cash}, ledger.PageParams{}); err != nil || len(items) != 2 {
		t.Errorf("listing %d %v", len(items), err)
	}
	x.invariants(t)
}

func TestIntegrationRefunds(t *testing.T) {
	x := newTW(t)
	e, ctx := x.e, x.e.Ctx
	e.Clock.Set("2026-02-10")
	a2 := billing.Debtor{UnitID: &x.a2, Kind: "owner"}
	bank := x.ch.Account(ledger.SubBank)
	// 60000 paid with nothing owed is credit
	if _, err := x.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a2, Method: "bank_transfer", Amount: TRY(60000), ReceivedOn: "2026-02-09"}); err != nil {
		t.Fatal(err)
	}
	in := treasury.RefundInput{SiteID: e.SiteID, UnitID: &x.a2, DebtorKind: "owner", Amount: TRY(25000), Reason: "paid twice", Kind: "overpayment", PaidFromAccount: bank, RefundedOn: "2026-02-10"}
	r, err := x.tr.CreateRefund(ctx, e.OrgID, in)
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := x.credit(t, a2); c != 35000 {
		t.Errorf("credit after the refund %d", c)
	}
	if got := x.bal(t, bank); got != 35000 {
		t.Errorf("bank after the refund %d", got)
	}
	in.Amount = TRY(35001)
	if _, err := x.tr.CreateRefund(ctx, e.OrgID, in); problem.From(err).Status != 409 {
		t.Errorf("a refund above the credit: %v", err)
	}
	// a person's credit, and another refund kind
	if _, err := x.tr.CreateRefund(ctx, e.OrgID, treasury.RefundInput{SiteID: e.SiteID, PersonID: &x.o1, Amount: TRY(1), Reason: "x", Kind: "overpayment", PaidFromAccount: bank, RefundedOn: "2026-02-10"}); problem.From(err).Status != 409 {
		t.Errorf("a person without credit: %v", err)
	}
	if _, err := x.tr.CreateRefund(ctx, e.OrgID, treasury.RefundInput{SiteID: e.SiteID, Amount: TRY(500), Reason: "goodwill", Kind: "other", PaidFromAccount: bank, RefundedOn: "2026-02-10"}); err != nil {
		t.Errorf("other refund: %v", err)
	}
	if got, err := x.tr.GetRefund(ctx, e.OrgID, r.ID); err != nil || got.Amount.Amount != 25000 {
		t.Errorf("get %+v %v", got, err)
	}
	x.invariants(t)
}

func TestIntegrationExpensesAndRecurrence(t *testing.T) {
	x := newTW(t)
	e, ctx := x.e, x.e.Ctx
	e.Clock.Set("2026-01-20")
	bank := x.ch.Account(ledger.SubBank)
	in := treasury.ExpenseInput{SiteID: e.SiteID, CategoryAccountID: x.ch.AccountCode("5010"), FundID: x.ch.OperatingFund, Amount: TRY(12000), PaidOn: "2026-01-15", PaidFromAccountID: bank,
		Description: "Electricity", Recurring: &treasury.Recurrence{Frequency: "monthly", Day: 15}}
	first, err := x.tr.CreateExpense(ctx, e.OrgID, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.RecurringExpenseID == nil || first.Status != "posted" {
		t.Fatalf("first %+v", first)
	}
	// catch-up: three months pass, the job posts the 15th of February, March and April once each
	e.Clock.Set("2026-04-20")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if n, err := x.tr.RunRecurring(ctx, e.OrgID, e.SiteID, log); err != nil || n != 3 {
		t.Fatalf("recurring posted %d, %v", n, err)
	}
	if n, _ := x.tr.RunRecurring(ctx, e.OrgID, e.SiteID, log); n != 0 {
		t.Errorf("second run posted %d", n)
	}
	items, _, err := x.tr.ListExpenses(ctx, e.OrgID, e.SiteID, treasury.ExpenseFilter{Q: "electric"}, ledger.PageParams{})
	if err != nil || len(items) != 4 {
		t.Fatalf("list %d %v", len(items), err)
	}
	// a locked month is skipped, the recurrence goes on
	e.Clock.Set("2026-07-20")
	ld, _ := x.led.GetLockDate(ctx, e.OrgID, e.SiteID)
	if _, err := x.led.SetLockDate(ctx, e.OrgID, e.SiteID, ld.Version, ledger.Ptr("2026-05-31")); err != nil {
		t.Fatal(err)
	}
	if n, err := x.tr.RunRecurring(ctx, e.OrgID, e.SiteID, log); err != nil || n != 2 {
		t.Fatalf("after the lock: posted %d, %v (June and July)", n, err)
	}
	// void: the entry is reversed, a second void is a 409
	v, err := x.tr.VoidExpense(ctx, e.OrgID, first.ID, "entered twice")
	if err != nil || v.Status != "voided" {
		t.Fatalf("void %+v %v", v, err)
	}
	if _, err := x.tr.VoidExpense(ctx, e.OrgID, first.ID, "again"); problem.From(err).Status != 409 {
		t.Errorf("second void: %v", err)
	}
	// refusals: income account, future date
	bad := in
	bad.Recurring, bad.CategoryAccountID = nil, x.ch.AccountCode("4000")
	if _, err := x.tr.CreateExpense(ctx, e.OrgID, bad); problem.From(err).Status != 422 {
		t.Errorf("income account as category: %v", err)
	}
	bad = in
	bad.Recurring, bad.PaidOn = nil, "2026-08-01"
	if _, err := x.tr.CreateExpense(ctx, e.OrgID, bad); problem.From(err).Status != 422 {
		t.Errorf("future expense: %v", err)
	}
	x.invariants(t)
}

func TestIntegrationBills(t *testing.T) {
	x := newTW(t)
	e, ctx := x.e, x.e.Ctx
	e.Clock.Set("2026-02-10")
	vendor := id.New()
	e.Exec(`INSERT INTO vendor (id, org_id, site_id, name) VALUES ($1,$2,$3,'Lift Co')`, vendor, e.OrgID, e.SiteID)
	in := treasury.BillInput{SiteID: e.SiteID, VendorID: vendor, IssueDate: "2026-02-01", DueDate: "2026-03-01", Amount: TRY(90000), CategoryAccountID: x.ch.AccountCode("5020"), FundID: x.ch.OperatingFund}
	if _, err := x.tr.CreateBill(ctx, e.OrgID, in); problem.From(err).Status != 409 {
		t.Fatalf("bills are off by default: %v", err)
	}
	e.Exec(`UPDATE site SET settings = settings || '{"bills_enabled": true}'::jsonb WHERE org_id = $1 AND id = $2`, e.OrgID, e.SiteID)
	b, err := x.tr.CreateBill(ctx, e.OrgID, in)
	if err != nil {
		t.Fatal(err)
	}
	if got := x.bal(t, x.ch.Account(ledger.SubAccountsPayable)); got != -90000 {
		t.Errorf("payables %d", got)
	}
	bank := x.ch.Account(ledger.SubBank)
	if _, err := x.tr.CreateBillPayment(ctx, e.OrgID, b.ID, treasury.BillPaymentInput{Amount: TRY(40000), PaidOn: "2026-02-10", PaidFromAccountID: bank}); err != nil {
		t.Fatal(err)
	}
	if got, _ := x.tr.GetBill(ctx, e.OrgID, b.ID); got.Status != "partially_paid" || got.Balance.Amount != 50000 || len(got.Payments) != 1 {
		t.Errorf("after a part payment %+v", got)
	}
	if _, err := x.tr.CreateBillPayment(ctx, e.OrgID, b.ID, treasury.BillPaymentInput{Amount: TRY(50001), PaidOn: "2026-02-10", PaidFromAccountID: bank}); problem.From(err).Status != 409 {
		t.Errorf("overpaying a bill: %v", err)
	}
	if _, err := x.tr.CreateBillPayment(ctx, e.OrgID, b.ID, treasury.BillPaymentInput{Amount: TRY(50000), PaidOn: "2026-02-10", PaidFromAccountID: bank}); err != nil {
		t.Fatal(err)
	}
	if got, _ := x.tr.GetBill(ctx, e.OrgID, b.ID); got.Status != "paid" || got.Balance.Amount != 0 {
		t.Errorf("after full payment %+v", got)
	}
	if got := x.bal(t, x.ch.Account(ledger.SubAccountsPayable)); got != 0 {
		t.Errorf("payables after payment %d", got)
	}
	x.invariants(t)
}

func TestIntegrationBudgetAndFinalizer(t *testing.T) {
	x := newTW(t)
	e, ctx := x.e, x.e.Ctx
	e.Clock.Set("2026-03-10")
	bank := x.ch.Account(ledger.SubBank)
	var fy uuid.UUID
	e.Exec(`INSERT INTO fiscal_year (id, org_id, site_id, start_on, end_on) VALUES ($1,$2,$3,'2026-01-01','2026-12-31')`, fy2(&fy), e.OrgID, e.SiteID)
	b, err := x.tr.CreateBudget(ctx, e.OrgID, e.SiteID, fy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.tr.CreateBudget(ctx, e.OrgID, e.SiteID, fy, nil); problem.From(err).Status != 409 {
		t.Errorf("a second budget for the year: %v", err)
	}
	lines := []treasury.BudgetLine{
		{ExpenseCategoryAccountID: x.ch.AccountCode("5010"), FundID: x.ch.OperatingFund, Amount: TRY(100000)},
		{ExpenseCategoryAccountID: x.ch.AccountCode("5020"), FundID: x.ch.OperatingFund, Amount: TRY(50000)},
	}
	if _, err := x.tr.PutBudgetLines(ctx, e.OrgID, b.ID, 7, lines); err == nil {
		t.Error("a stale version was accepted")
	}
	got, err := x.tr.PutBudgetLines(ctx, e.OrgID, b.ID, b.Version, lines)
	if err != nil || len(got.Lines) != 2 || got.Version != b.Version+1 {
		t.Fatalf("put %+v %v", got, err)
	}
	if _, err := x.tr.PutBudgetLines(ctx, e.OrgID, b.ID, got.Version, append(lines, lines[0])); problem.From(err).Status != 422 {
		t.Errorf("duplicate line: %v", err)
	}
	// spending: 30000 on electricity, 70000 on the lift, 5000 not budgeted
	for _, ex := range []struct {
		acc string
		amt int64
	}{{"5010", 30000}, {"5020", 70000}, {"5040", 5000}} {
		if _, err := x.tr.CreateExpense(ctx, e.OrgID, treasury.ExpenseInput{SiteID: e.SiteID, CategoryAccountID: x.ch.AccountCode(ex.acc), FundID: x.ch.OperatingFund, Amount: TRY(ex.amt), PaidOn: "2026-03-05",
			PaidFromAccountID: bank, Description: "spend"}); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := x.tr.BudgetVsActual(ctx, e.OrgID, e.SiteID, fy)
	if err != nil || len(rep.Rows) != 3 || rep.TotalBudget.Amount != 150000 || rep.TotalActual.Amount != 105000 {
		t.Fatalf("budget vs actual %+v %v", rep, err)
	}
	for _, r := range rep.Rows {
		want := r.Budget.Amount - r.Actual.Amount
		if r.Variance.Amount != want {
			t.Errorf("%s variance %d, want %d", r.Code, r.Variance.Amount, want)
		}
		if r.Code == "5020" && r.Variance.Amount != -20000 {
			t.Errorf("the lift is over budget by 20000, variance %d", r.Variance.Amount)
		}
	}
	if csv, err := rep.CSV(); err != nil || len(csv) == 0 {
		t.Errorf("csv %v", err)
	}
	// the decision effect makes the draft final once
	err = e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
		rec, err := x.tr.FinalizeBudget(ctx, tx, moneyDecision(e.SiteID), b.ID)
		if err != nil || rec.Kind != "budget" {
			t.Errorf("finalize %+v %v", rec, err)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := x.tr.GetBudget(ctx, e.OrgID, b.ID); f.Status != "final" || f.DecisionID == nil {
		t.Errorf("after the decision %+v", f)
	}
	if _, err := x.tr.PutBudgetLines(ctx, e.OrgID, b.ID, 99, lines); problem.From(err).Status == 0 {
		t.Error("a final budget accepted lines")
	}
	if err := x.tr.DeleteBudget(ctx, e.OrgID, b.ID, 99); err == nil {
		t.Error("a final budget was deleted")
	}
}

func fy2(p *uuid.UUID) uuid.UUID { *p = id.New(); return *p }

var _ = tenancy.NewService
