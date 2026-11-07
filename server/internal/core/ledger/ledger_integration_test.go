package ledger_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/moneytest"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func post(t *testing.T, e *moneytest.Env, ch ledger.Chart, date string, amount int64) (uuid.UUID, error) {
	t.Helper()
	var eid uuid.UUID
	err := e.DB.InTenantTx(e.Ctx, e.OrgID, func(tx pgx.Tx) (err error) {
		eid, err = ledger.PostEntry(e.Ctx, tx, ledger.Entry{OrgID: e.OrgID, SiteID: e.SiteID, EntryDate: date, SourceType: "test", Lines: []ledger.Line{
			{AccountID: ch.Account(ledger.SubBank), FundID: ch.OperatingFund, Debit: amount},
			{AccountID: ch.Account(ledger.SubOtherIncome), FundID: ch.OperatingFund, Credit: amount},
		}})
		return err
	})
	return eid, err
}

func TestIntegrationLedger(t *testing.T) {
	e := moneytest.New(t)
	svc := ledger.NewService(e.DB, e.Clock.Now)
	ctx := e.Ctx
	var ch ledger.Chart
	if err := e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) (err error) { ch, err = ledger.EnsureChart(ctx, tx, e.OrgID, e.SiteID); return }); err != nil {
		t.Fatal(err)
	}
	if ch.OperatingFund == uuid.Nil || ch.Account(ledger.SubReceivableOwner) == uuid.Nil {
		t.Fatal("default chart missing")
	}

	t.Run("balanced entries, balances, reversal dated today", func(t *testing.T) {
		eid, err := post(t, e, ch, "2026-01-10", 12500)
		if err != nil {
			t.Fatal(err)
		}
		_ = e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
			d, c, err := ledger.Balance(ctx, tx, e.OrgID, e.SiteID, ch.Account(ledger.SubBank), ledger.BalanceFilter{})
			if err != nil || d-c != 12500 {
				t.Errorf("bank balance %d %d %v", d, c, err)
			}
			return nil
		})
		e.Clock.Set("2026-02-01")
		rev, err := svc.ReverseJournalEntry(ctx, e.OrgID, eid, "")
		if err != nil {
			t.Fatal(err)
		}
		if rev.EntryDate != "2026-02-01" || rev.DocumentDate != "2026-01-10" || rev.ReversesEntryID == nil {
			t.Errorf("reversal %+v", rev)
		}
		if _, err := svc.ReverseJournalEntry(ctx, e.OrgID, eid, ""); problem.From(err).Status != 409 {
			t.Errorf("second reversal: %v", err)
		}
		if _, err := svc.ReverseJournalEntry(ctx, e.OrgID, rev.ID, ""); problem.From(err).Status != 409 {
			t.Errorf("reversing a reversal: %v", err)
		}
	})

	t.Run("unbalanced and one-sided lines are refused", func(t *testing.T) {
		err := e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
			_, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: e.OrgID, SiteID: e.SiteID, EntryDate: "2026-02-02", SourceType: "test", Lines: []ledger.Line{
				{AccountID: ch.Account(ledger.SubBank), FundID: ch.OperatingFund, Debit: 100},
				{AccountID: ch.Account(ledger.SubOtherIncome), FundID: ch.OperatingFund, Credit: 90},
			}})
			return err
		})
		if problem.From(err).Status != 422 {
			t.Errorf("unbalanced: %v", err)
		}
	})

	t.Run("the journal is append-only and the database balances at commit", func(t *testing.T) {
		if err := e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE journal_entry SET memo = 'x'`)
			return err
		}); err == nil {
			t.Error("UPDATE journal_entry succeeded")
		}
		if err := e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM journal_line`)
			return err
		}); err == nil {
			t.Error("DELETE journal_line succeeded")
		}
		// a raw unbalanced insert is refused by the deferred trigger at commit
		err := e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
			eid := uuid.New()
			if _, err := tx.Exec(ctx, `INSERT INTO journal_entry (id, org_id, site_id, entry_date, document_date, source_type) VALUES ($1,$2,$3,'2026-03-01','2026-03-01','raw')`, eid, e.OrgID, e.SiteID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO journal_line (id, org_id, site_id, entry_id, entry_date, account_id, fund_id, debit) VALUES ($1,$2,$3,$4,'2026-03-01',$5,$6,50)`,
				uuid.New(), e.OrgID, e.SiteID, eid, ch.Account(ledger.SubBank), ch.OperatingFund)
			return err
		})
		if err == nil {
			t.Error("unbalanced raw insert committed")
		}
	})

	t.Run("lock date: postings refused, moving back needs re-auth", func(t *testing.T) {
		ld, err := svc.GetLockDate(ctx, e.OrgID, e.SiteID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SetLockDate(ctx, e.OrgID, e.SiteID, ld.Version, ledger.Ptr("2026-01-31")); err != nil {
			t.Fatal(err)
		}
		if _, err := post(t, e, ch, "2026-01-31", 100); problem.From(err).Code != problem.CodeLockedPeriod {
			t.Errorf("posting on the lock date: %v", err)
		}
		if _, err := post(t, e, ch, "2026-02-03", 100); err != nil {
			t.Errorf("posting after the lock date: %v", err)
		}
		ld, _ = svc.GetLockDate(ctx, e.OrgID, e.SiteID)
		if ld.LockDate == nil || *ld.LockDate != "2026-01-31" || ld.ChangedBy == nil {
			t.Errorf("lock date %+v", ld)
		}
		// moving back without a recent re-authentication
		plain := noReauth(ctx)
		if _, err := svc.SetLockDate(plain, e.OrgID, e.SiteID, ld.Version, ledger.Ptr("2026-01-15")); problem.From(err).Code != problem.CodeReauthRequired {
			t.Errorf("moving back without re-auth: %v", err)
		}
		if _, err := svc.SetLockDate(ctx, e.OrgID, e.SiteID, ld.Version, ledger.Ptr("2026-01-15")); err != nil {
			t.Errorf("moving back with re-auth: %v", err)
		}
	})

	t.Run("funds, accounts, cash boxes, fiscal years", func(t *testing.T) {
		f, err := svc.CreateFund(ctx, e.OrgID, ledger.FundInput{SiteID: e.SiteID, Name: "Roof fund", Kind: "custom"})
		if err != nil || f.Kind != "custom" {
			t.Fatalf("fund %+v %v", f, err)
		}
		funds, _, _ := svc.ListFunds(ctx, e.OrgID, e.SiteID, ledger.PageParams{})
		if len(funds) != 3 {
			t.Errorf("%d funds", len(funds))
		}
		a, err := svc.CreateAccount(ctx, e.OrgID, ledger.AccountInput{SiteID: e.SiteID, Code: "5100", Name: "Lift service", Type: "expense", Subtype: ledger.Ptr("expense_category")})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.UpdateAccount(ctx, e.OrgID, a.ID, a.Version, ledger.Doc{"name": []byte(`"Lift maintenance"`)}); err != nil {
			t.Error(err)
		}
		box, err := svc.CreateCashBox(ctx, e.OrgID, ledger.CashBoxInput{SiteID: e.SiteID, Name: "Kapici"})
		if err != nil || box.Balance.Amount != 0 {
			t.Fatalf("cash box %+v %v", box, err)
		}
		fys, _ := svc.ListFiscalYears(ctx, e.OrgID, e.SiteID)
		if len(fys) == 0 || fys[0].Start != "2026-01-01" {
			t.Errorf("fiscal years %+v", fys)
		}
		if fy, err := svc.SetFiscalYearStatus(ctx, e.OrgID, fys[0].ID, "closed", nil); err != nil || fy.Status != "closed" {
			t.Errorf("close %+v %v", fy, err)
		}
	})
}

func noReauth(ctx context.Context) context.Context {
	return moneytest.WithoutReauth(ctx)
}
