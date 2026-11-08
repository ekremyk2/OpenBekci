package bankimport_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/payments/bankimport"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/moneytest"
	"github.com/openbekci/openbekci/server/internal/problem"
)

type world struct {
	e      *moneytest.Env
	pay    *payments.Service
	bank   *bankimport.Service
	a1, a2 uuid.UUID
	acct   uuid.UUID
}

func newWorld(t *testing.T) *world {
	e := moneytest.New(t)
	w := &world{e: e}
	bill := billing.NewService(e.DB, tenancy.NewService(e.DB), e.Clock.Now)
	w.pay = payments.NewService(e.DB, e.Clock.Now, nil)
	w.bank = bankimport.NewService(e.DB, w.pay, e.Clock.Now)
	w.a1, w.a2 = e.Unit("A-1", 600), e.Unit("A-2", 400)
	o1, o2 := e.Person("Ayşe Yılmaz"), e.Person("Mehmet Kaya")
	e.Occupy(w.a1, o1, "owner", "2025-01-01", "")
	e.Occupy(w.a2, o2, "owner", "2025-01-01", "")
	amount := int64(100000)
	if _, err := bill.CreatePlan(e.Ctx, e.OrgID, billing.PlanInput{SiteID: e.SiteID, Name: "Aidat", DueDay: 5, Start: "2026-01-01", AmountMode: "total_allocated", AllocationKey: ledger.Ptr("arsa_payi"), Amount: &amount}); err != nil {
		t.Fatal(err)
	}
	e.Clock.Set("2026-03-12")
	if n, err := bill.GenerateCharges(e.Ctx, e.OrgID, e.SiteID); err != nil || n != 6 {
		t.Fatalf("generated %d %v", n, err)
	}
	err := e.DB.InTenantTx(e.Ctx, e.OrgID, func(tx pgx.Tx) error {
		ch, err := ledger.EnsureChart(e.Ctx, tx, e.OrgID, e.SiteID)
		w.acct = ch.Account(ledger.SubBank)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

const statement = "booked_on,amount,counterparty,description,reference\n" +
	"10.03.2026,\"600,00\",Ayşe Yılmaz,Aidat ocak,A-1-2026-01\n" +
	"11.03.2026,\"400,00\",Mehmet Kaya,Havale,\n" +
	"12.03.2026,\"-50,00\",Banka,Hesap işletim ücreti,\n"

func (w *world) upload(t *testing.T, data string) bankimport.Import {
	t.Helper()
	im, err := w.bank.Create(w.e.Ctx, w.e.OrgID, bankimport.CreateInput{SiteID: w.e.SiteID, Importer: bankimport.CoreCSV, AssetAccountID: w.acct, Filename: "s.csv", Data: []byte(data),
		OpeningBalance: ledger.Ptr(int64(100000)), ClosingBalance: ledger.Ptr(int64(195000))})
	if err != nil {
		t.Fatal(err)
	}
	return im
}

func (w *world) lines(t *testing.T, im bankimport.Import) []bankimport.Transaction {
	t.Helper()
	items, _, err := w.bank.ListTransactions(w.e.Ctx, w.e.OrgID, w.e.SiteID, bankimport.TxFilter{ImportID: &im.ID}, ledger.PageParams{})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func chargeStatus(w *world, unit uuid.UUID) map[string]int64 {
	out := map[string]int64{}
	_ = w.e.DB.InTenantTx(w.e.Ctx, w.e.OrgID, func(tx pgx.Tx) error {
		r, err := tx.Query(context.Background(), `SELECT period, status FROM charge WHERE org_id=$1 AND unit_id=$2 AND debtor_kind='owner' ORDER BY period`, w.e.OrgID, unit)
		if err != nil {
			return err
		}
		defer r.Close()
		for r.Next() {
			var p, s string
			if err := r.Scan(&p, &s); err != nil {
				return err
			}
			out[p+":"+s]++
		}
		return r.Err()
	})
	return out
}

func TestIntegrationImportMatchUnmatchReconcile(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx
	im := w.upload(t, statement)
	if im.Status != "ready" || im.TransactionCount != 3 || im.UnmatchedCount != 3 || *im.PeriodFrom != "2026-03-10" || *im.PeriodTo != "2026-03-12" {
		t.Fatalf("import %+v", im)
	}
	// the same file twice is a conflict
	_, err := w.bank.Create(ctx, e.OrgID, bankimport.CreateInput{SiteID: e.SiteID, Importer: bankimport.CoreCSV, AssetAccountID: w.acct, Filename: "again.csv", Data: []byte(statement)})
	var pe *problem.Error
	if !errors.As(err, &pe) || pe.Status != 409 {
		t.Fatalf("duplicate upload: %v", err)
	}

	lines := w.lines(t, im)
	l1, l2, l3 := lines[0], lines[1], lines[2]
	if l1.BookedOn != "2026-03-10" || l1.Amount.Amount != 60000 || l1.MatchStatus != "suggested" || len(l1.Suggestions) == 0 {
		t.Fatalf("line 1 %+v", l1)
	}
	if s := l1.Suggestions[0]; s.UnitID != w.a1 || s.DebtorKind != "owner" || s.Score < 0.95 {
		t.Errorf("suggestion %+v", s)
	}
	if l2.MatchStatus != "suggested" || l2.Suggestions[0].UnitID != w.a2 {
		t.Errorf("line 2 (name and amount) %+v", l2)
	}
	if l3.MatchStatus != "unmatched" || len(l3.Suggestions) != 0 {
		t.Errorf("outgoing line %+v", l3)
	}

	// match line 1 to A-1 owner: a confirmed bank payment, allocated oldest first
	a1 := billing.Debtor{UnitID: &w.a1, Kind: "owner"}
	m1, err := w.bank.Match(ctx, e.OrgID, l1.ID, bankimport.MatchInput{Debtor: &a1})
	if err != nil || m1.MatchStatus != "matched" || m1.PaymentID == nil {
		t.Fatalf("match %+v %v", m1, err)
	}
	p, err := w.pay.Get(ctx, e.OrgID, *m1.PaymentID)
	if err != nil || p.Status != "confirmed" || p.Method != "bank_transfer" || p.Net.Amount != 60000 || len(p.Allocations) != 1 || p.AssetAccountID != w.acct || p.ReceivedOn != "2026-03-10" {
		t.Fatalf("payment %+v %v", p, err)
	}
	if st := chargeStatus(w, w.a1); st["2026-01:paid"] != 1 || st["2026-02:open"] != 1 {
		t.Errorf("charges after match %v", st)
	}
	if _, err := w.bank.Match(ctx, e.OrgID, l1.ID, bankimport.MatchInput{Debtor: &a1}); err == nil {
		t.Error("matching a matched line must conflict")
	}

	// line 2 confirms a declared payment instead of creating one
	a2 := billing.Debtor{UnitID: &w.a2, Kind: "owner"}
	dp, err := w.pay.Declare(ctx, e.OrgID, payments.DeclareInput{SiteID: e.SiteID, Debtor: a2, Amount: money.New(40000, "TRY"), PaidOn: "2026-03-11"})
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := w.pay.Declare(ctx, e.OrgID, payments.DeclareInput{SiteID: e.SiteID, Debtor: a2, Amount: money.New(123, "TRY"), PaidOn: "2026-03-11"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.bank.Match(ctx, e.OrgID, l2.ID, bankimport.MatchInput{PaymentID: &wrong.ID}); err == nil {
		t.Error("amount mismatch must be refused")
	}
	m2, err := w.bank.Match(ctx, e.OrgID, l2.ID, bankimport.MatchInput{PaymentID: &dp.ID})
	if err != nil || m2.PaymentID == nil || *m2.PaymentID != dp.ID {
		t.Fatalf("link %+v %v", m2, err)
	}
	if got, _ := w.pay.Get(ctx, e.OrgID, dp.ID); got.Status != "confirmed" || got.AssetAccountID != w.acct || got.ReceiptNo == nil {
		t.Errorf("declared payment %+v", got)
	}

	// reconciliation: line 3 (-50,00) is the only unmatched amount and equals the difference
	rc, err := w.bank.Reconciliation(ctx, e.OrgID, im.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rc.MatchedTotal.Amount != 100000 || rc.UnmatchedTotal.Amount != -5000 || rc.LinesTotal.Amount != 95000 || rc.StatementCheck == nil || rc.StatementCheck.Amount != 0 ||
		rc.LedgerMovement.Amount != 100000 || rc.Difference.Amount != -5000 || rc.MatchedCount != 2 || rc.UnmatchedCount != 1 {
		t.Errorf("reconciliation %+v", rc)
	}
	// a bank fee is not income: ignore it
	ig, err := w.bank.Ignore(ctx, e.OrgID, l3.ID, "bank fee")
	if err != nil || ig.MatchStatus != "ignored" {
		t.Fatalf("ignore %+v %v", ig, err)
	}
	if got, _ := w.bank.GetImport(ctx, e.OrgID, im.ID); got.UnmatchedCount != 0 {
		t.Errorf("unmatched count %d", got.UnmatchedCount)
	}
	if err := w.bank.DeleteImport(ctx, e.OrgID, im.ID); err == nil {
		t.Error("an import with matched lines cannot be discarded")
	}

	// unmatch line 1: the payment is voided, the charge is open again, the line is back
	u1, err := w.bank.Unmatch(ctx, e.OrgID, l1.ID, "wrong unit")
	if err != nil || u1.MatchStatus != "suggested" || u1.PaymentID != nil {
		t.Fatalf("unmatch %+v %v", u1, err)
	}
	if got, _ := w.pay.Get(ctx, e.OrgID, *m1.PaymentID); got.Status != "voided" {
		t.Errorf("payment after unmatch %+v", got)
	}
	if st := chargeStatus(w, w.a1); st["2026-01:open"] != 1 {
		t.Errorf("charges after unmatch %v", st)
	}
	rc, _ = w.bank.Reconciliation(ctx, e.OrgID, im.ID)
	if rc.MatchedTotal.Amount != 40000 || rc.UnmatchedTotal.Amount != 60000 || rc.LedgerMovement.Amount != 40000 {
		t.Errorf("reconciliation after unmatch %+v", rc)
	}
	// and it can be matched again (to a person-less side is not allowed, to A-1 is)
	if _, err := w.bank.Match(ctx, e.OrgID, l1.ID, bankimport.MatchInput{Debtor: &a1}); err != nil {
		t.Fatal(err)
	}

	// locked period: the unmatch of a line inside a locked period is refused and leaves the match intact
	e.Exec(`UPDATE site SET lock_date = '2026-03-31' WHERE org_id=$1 AND id=$2`, e.OrgID, e.SiteID)
	e.Clock.Set("2026-03-13")
	if _, err := w.bank.Unmatch(ctx, e.OrgID, l2.ID, "late"); err == nil {
		t.Error("unmatch in a locked period must fail")
	}
	if got, _ := w.bank.GetTransaction(ctx, e.OrgID, l2.ID); got.MatchStatus != "matched" {
		t.Errorf("failed unmatch left %+v", got)
	}
}

func TestIntegrationUploadValidation(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx
	cases := []bankimport.CreateInput{
		{SiteID: e.SiteID, Importer: "nope", AssetAccountID: w.acct, Data: []byte(statement)},
		{SiteID: e.SiteID, Importer: bankimport.CoreCSV, AssetAccountID: uuid.New(), Data: []byte(statement)},
		{SiteID: e.SiteID, Importer: bankimport.CoreCSV, AssetAccountID: w.acct, Data: []byte("a,b\n1,2\n")},
	}
	for i, in := range cases {
		_, err := w.bank.Create(ctx, e.OrgID, in)
		var pe *problem.Error
		if !errors.As(err, &pe) || pe.Status != 422 {
			t.Errorf("case %d: %v", i, err)
		}
	}
	big := make([]byte, bankimport.MaxFileBytes+1)
	_, err := w.bank.Create(ctx, e.OrgID, bankimport.CreateInput{SiteID: e.SiteID, Importer: bankimport.CoreCSV, AssetAccountID: w.acct, Data: big})
	var pe *problem.Error
	if !errors.As(err, &pe) || pe.Status != 413 {
		t.Errorf("too large: %v", err)
	}
	// a partial parse is reported
	im := w.upload(t, statement+"garbage,1,2,3,4\n")
	if im.Status != "partial" || im.Error == nil || im.TransactionCount != 3 {
		t.Errorf("partial %+v", im)
	}
}

func TestIntegrationPruneRaw(t *testing.T) {
	w := newWorld(t)
	e := w.e
	im := w.upload(t, statement)
	if n := e.Int(`SELECT count(*) FROM bank_transaction WHERE import_id=$1 AND raw IS NOT NULL`, im.ID); n != 3 {
		t.Fatalf("raw rows %d", n)
	}
	e.Exec(`UPDATE bank_transaction SET created_at = now() - interval '91 days' WHERE import_id=$1 AND line_no = 1`, im.ID)
	n, err := bankimport.PruneRaw(context.Background(), e.DB, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("pruned %d %v", n, err)
	}
	if got := e.Int(`SELECT count(*) FROM bank_transaction WHERE import_id=$1 AND raw IS NOT NULL`, im.ID); got != 2 {
		t.Errorf("raw rows after prune %d", got)
	}
}
