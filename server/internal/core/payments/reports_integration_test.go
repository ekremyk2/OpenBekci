package payments_test

import (
	"bytes"
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/reports"
)

func TestIntegrationStatementsAndReports(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx
	rep := reports.NewService(e.DB, e.Clock.Now)
	if _, err := w.bill.SetLateFeePolicy(ctx, e.OrgID, e.SiteID, billing.PolicyRow{From: "2026-01-01", RateBPSPerMonth: 500}); err != nil {
		t.Fatal(err)
	}
	amount := int64(100000)
	if _, err := w.bill.CreatePlan(ctx, e.OrgID, billing.PlanInput{SiteID: e.SiteID, Name: "Aidat", DueDay: 5, Start: "2026-01-01", AmountMode: "total_allocated", AllocationKey: ledger.Ptr("arsa_payi"), Amount: &amount}); err != nil {
		t.Fatal(err)
	}
	e.Clock.Set("2026-03-10")
	if _, err := w.bill.GenerateCharges(ctx, e.OrgID, e.SiteID); err != nil {
		t.Fatal(err)
	}
	a1 := billing.Debtor{UnitID: &w.a1, Kind: "owner"}
	if _, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "bank_transfer", Amount: money.New(60000, "TRY"), ReceivedOn: "2026-02-20"}); err != nil {
		t.Fatal(err)
	}

	st, err := rep.DebtorStatement(ctx, e.OrgID, e.SiteID, a1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// 3 charges of 600.00 less one payment of 600.00 = 1200.00 principal, plus the accrued unbooked fee
	if st.Closing.Principal.Amount != 120000 || st.AccruedFee.Amount <= 0 || st.Closing.Fee.Amount != st.AccruedFee.Amount {
		t.Errorf("statement closing %+v accrued %+v", st.Closing, st.AccruedFee)
	}
	if st.Closing.Total.Amount != st.Closing.Principal.Amount+st.Closing.Fee.Amount {
		t.Errorf("principal/fee split does not add up: %+v", st.Closing)
	}
	// a range statement folds earlier lines into the opening balance
	r, err := rep.DebtorStatement(ctx, e.OrgID, e.SiteID, a1, "2026-03-01", "2026-03-10")
	if err != nil || r.Opening.Principal.Amount != 60000 {
		t.Errorf("range statement opening %+v %v", r.Opening, err)
	}
	csvb, err := st.CSV()
	if err != nil || !bytes.HasPrefix(csvb, []byte("date,kind,part")) {
		t.Errorf("csv %q %v", csvb, err)
	}
	if pdfb := st.PDF(); !bytes.HasPrefix(pdfb, []byte("%PDF-1.4")) || !bytes.Contains(pdfb, []byte("%%EOF")) {
		t.Error("pdf is not a PDF")
	}

	debtors, err := rep.DebtorReport(ctx, e.OrgID, e.SiteID, "", 0)
	if err != nil || len(debtors) != 2 {
		t.Fatalf("debtors %+v %v", debtors, err)
	}
	if debtors[0].Total.Amount < debtors[1].Total.Amount || debtors[0].DaysOverdue == 0 {
		t.Errorf("debtor order or overdue days %+v", debtors)
	}
	aging, err := rep.ReceivablesAging(ctx, e.OrgID, e.SiteID, "")
	if err != nil || len(aging) != 5 {
		t.Fatalf("aging %+v %v", aging, err)
	}
	var sum int64
	for _, b := range aging {
		sum += b.Amount.Amount
	}
	var owed int64
	for _, d := range debtors {
		owed += d.Total.Amount
	}
	if sum != owed {
		t.Errorf("aging total %d != debtor total %d", sum, owed)
	}
	tb, err := rep.TrialBalance(ctx, e.OrgID, e.SiteID, "")
	if err != nil {
		t.Fatal(err)
	}
	var deb, cred int64
	for _, row := range tb {
		deb += row.Debit.Amount
		cred += row.Credit.Amount
	}
	if deb != cred || deb == 0 {
		t.Errorf("trial balance %d / %d", deb, cred)
	}
	if funds, err := rep.FundBalances(ctx, e.OrgID, e.SiteID, ""); err != nil || len(funds) != 2 {
		t.Errorf("funds %+v %v", funds, err)
	}
	ie, err := rep.IncomeExpenseReport(ctx, e.OrgID, e.SiteID, "2026-01-01", "2026-12-31")
	if err != nil || ie.TotalIncome.Amount != 60000 {
		t.Errorf("income-expense %+v %v", ie, err)
	}
}
