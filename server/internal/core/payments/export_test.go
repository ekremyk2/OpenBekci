package payments_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/core/treasury"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// A site hand-over carries the whole ledger: every money table arrives in the other organization with the same
// identifiers, balances and lock date.
func TestIntegrationSiteHandOverCarriesTheLedger(t *testing.T) {
	x := newTW(t)
	e, ctx := x.e, x.e.Ctx
	x.plan(t, 100000)
	e.Clock.Set("2026-01-01")
	if _, err := x.bill.SetLateFeePolicy(ctx, e.OrgID, e.SiteID, billing.PolicyRow{From: "2026-01-01", RateBPSPerMonth: 500}); err != nil {
		t.Fatal(err)
	}
	e.Clock.Set("2026-04-12")
	if _, err := x.bill.GenerateCharges(ctx, e.OrgID, e.SiteID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.bill.CrystallizeLateFees(ctx, e.OrgID, e.SiteID); err != nil {
		t.Fatal(err)
	}
	a1 := billing.Debtor{UnitID: &x.a1, Kind: "owner"}
	if _, err := x.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "cash", Amount: TRY(70000), ReceivedOn: "2026-04-10", AssetAccountID: &x.cash}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.tr.CreateTransfer(ctx, e.OrgID, treasury.TransferInput{SiteID: e.SiteID, FromAccountID: x.cash, ToAccountID: x.ch.Account(ledger.SubBank), Amount: TRY(50000), TransferredOn: "2026-04-11"}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.tr.CreateExpense(ctx, e.OrgID, treasury.ExpenseInput{SiteID: e.SiteID, CategoryAccountID: x.ch.AccountCode("5010"), FundID: x.ch.OperatingFund, Amount: TRY(9000),
		PaidOn: "2026-04-05", PaidFromAccountID: x.ch.Account(ledger.SubBank), Description: "Electricity", Recurring: &treasury.Recurrence{Frequency: "monthly", Day: 5}}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.tr.CreateRefund(ctx, e.OrgID, treasury.RefundInput{SiteID: e.SiteID, Amount: TRY(100), Reason: "goodwill", Kind: "other", PaidFromAccount: x.ch.Account(ledger.SubBank), RefundedOn: "2026-04-11"}); err != nil {
		t.Fatal(err)
	}
	ld, _ := x.led.GetLockDate(ctx, e.OrgID, e.SiteID)
	if _, err := x.led.SetLockDate(ctx, e.OrgID, e.SiteID, ld.Version, ledger.Ptr("2026-03-31")); err != nil {
		t.Fatal(err)
	}
	tn := tenancy.NewService(e.DB)
	_, archive, err := tn.SystemExportSite(ctx, e.OrgID, e.SiteID)
	if err != nil {
		t.Fatal(err)
	}
	target := id.New()
	e.Exec(`INSERT INTO organization (id, org_id, slug, name) VALUES ($1, $1, 'target', 'Target')`, target)
	if _, err := tn.SystemImportSite(ctx, target, archive); err != nil {
		t.Fatalf("import: %v", err)
	}
	for _, table := range []string{"fund", "account", "journal_entry", "journal_line", "charge", "payment", "payment_allocation", "late_fee_policy", "late_fee_booking", "transfer", "refund", "expense", "recurring_expense", "cash_box", "fiscal_year", "sequence"} {
		var a, b int64
		e.Query([]any{&a}, `SELECT count(*) FROM `+table+` WHERE org_id = $1 AND site_id = $2`, e.OrgID, e.SiteID)
		e.Query([]any{&b}, `SELECT count(*) FROM `+table+` WHERE org_id = $1 AND site_id = $2`, target, e.SiteID)
		if a != b || a == 0 && table != "cash_box" {
			t.Errorf("%s: %d rows in the source, %d after the import", table, a, b)
		}
	}
	// the journal sums per account are the same, and the lock date is back
	var sa, sb int64
	e.Query([]any{&sa}, `SELECT COALESCE(SUM(debit * 7 + credit * 3), 0) FROM journal_line WHERE org_id = $1`, e.OrgID)
	e.Query([]any{&sb}, `SELECT COALESCE(SUM(debit * 7 + credit * 3), 0) FROM journal_line WHERE org_id = $1`, target)
	if sa != sb {
		t.Errorf("journal checksum %d != %d", sa, sb)
	}
	var lock string
	e.Query([]any{&lock}, `SELECT lock_date::text FROM site WHERE org_id = $1 AND id = $2`, target, e.SiteID)
	if lock != "2026-03-31" {
		t.Errorf("lock date after the import %q", lock)
	}
	// the receivable of every unit side in the target equals what it was
	if err := e.DB.InTenantTx(ctx, target, func(tx pgx.Tx) error {
		ch, err := ledger.EnsureChart(ctx, tx, target, e.SiteID)
		if err != nil {
			return err
		}
		for _, u := range []uuid.UUID{x.a1, x.a2} {
			u := u
			d := billing.Debtor{UnitID: &u, Kind: "owner"}
			charges, err := billing.LoadCharges(ctx, tx, target, e.SiteID, d)
			if err != nil {
				return err
			}
			p, f := billing.OpenTotal(charges)
			rec, err := billing.ReceivableOf(ctx, tx, target, e.SiteID, ch, d)
			if err != nil {
				return err
			}
			if rec != p+f {
				t.Errorf("unit %s in the target: receivable %d, open charges %d", u, rec, p+f)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
