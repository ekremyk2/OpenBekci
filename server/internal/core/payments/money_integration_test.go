package payments_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/moneytest"
	"github.com/openbekci/openbekci/server/internal/problem"
)

type world struct {
	e      *moneytest.Env
	led    *ledger.Service
	bill   *billing.Service
	pay    *payments.Service
	a1, a2 uuid.UUID
	o1, o2 uuid.UUID
	t2     uuid.UUID
}

func newWorld(t *testing.T) *world {
	e := moneytest.New(t)
	w := &world{e: e}
	w.led = ledger.NewService(e.DB, e.Clock.Now)
	w.bill = billing.NewService(e.DB, tenancy.NewService(e.DB), e.Clock.Now)
	w.pay = payments.NewService(e.DB, e.Clock.Now, nil)
	w.a1, w.a2 = e.Unit("A-1", 600), e.Unit("A-2", 400)
	w.o1, w.o2, w.t2 = e.Person("Owner One"), e.Person("Owner Two"), e.Person("Tenant Two")
	e.Occupy(w.a1, w.o1, "owner", "2025-01-01", "")
	e.Occupy(w.a2, w.o2, "owner", "2025-01-01", "")
	e.Occupy(w.a2, w.t2, "tenant", "2026-01-01", "")
	return w
}

// invariants asserts rule 13 on every debtor of the site.
func (w *world) invariants(t *testing.T) { t.Helper(); w.invariantsFor(t) }

func (w *world) invariantsFor(t *testing.T, extra ...uuid.UUID) {
	t.Helper()
	ctx, e := w.e.Ctx, w.e
	err := e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
		ch, err := ledger.EnsureChart(ctx, tx, e.OrgID, e.SiteID)
		if err != nil {
			return err
		}
		for _, u := range append([]uuid.UUID{w.a1, w.a2}, extra...) {
			for _, side := range []string{"owner", "tenant"} {
				u := u
				d := billing.Debtor{UnitID: &u, Kind: side}
				charges, err := billing.LoadCharges(ctx, tx, e.OrgID, e.SiteID, d)
				if err != nil {
					return err
				}
				p, f := billing.OpenTotal(charges)
				rec, err := billing.ReceivableOf(ctx, tx, e.OrgID, e.SiteID, ch, d)
				if err != nil {
					return err
				}
				if rec != p+f {
					t.Errorf("unit %s side %s: receivable %d != open charges %d", u, side, rec, p+f)
				}
			}
		}
		prows, err := tx.Query(ctx, `SELECT DISTINCT debtor_person_id FROM charge WHERE org_id=$1 AND debtor_kind='person'`, e.OrgID)
		if err != nil {
			return err
		}
		var persons []uuid.UUID
		for prows.Next() {
			var p uuid.UUID
			if err := prows.Scan(&p); err != nil {
				return err
			}
			persons = append(persons, p)
		}
		prows.Close()
		for _, p := range persons {
			p := p
			d := billing.Debtor{PersonID: &p}
			charges, err := billing.LoadCharges(ctx, tx, e.OrgID, e.SiteID, d)
			if err != nil {
				return err
			}
			pr, f := billing.OpenTotal(charges)
			rec, err := billing.ReceivableOf(ctx, tx, e.OrgID, e.SiteID, ch, d)
			if err != nil {
				return err
			}
			if rec != pr+f {
				t.Errorf("person %s: receivable %d != open charges %d", p, rec, pr+f)
			}
		}
		// every fund balances
		rows, err := tx.Query(ctx, `SELECT fund_id, SUM(debit) - SUM(credit) FROM journal_line WHERE org_id=$1 GROUP BY fund_id`, e.OrgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var f uuid.UUID
			var bal int64
			if err := rows.Scan(&f, &bal); err != nil {
				return err
			}
			if bal != 0 {
				t.Errorf("fund %s does not balance: %d", f, bal)
			}
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	// Σ allocations per payment <= net
	if n := e.Int(`SELECT count(*) FROM payment p WHERE org_id=$1 AND (SELECT COALESCE(SUM(amount),0) FROM payment_allocation a WHERE a.org_id=p.org_id AND a.payment_id=p.id AND NOT a.voided) > p.net`, e.OrgID); n != 0 {
		t.Errorf("%d payments are over-allocated", n)
	}
}

func TestIntegrationBillingAndPayments(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx

	if _, err := w.bill.SetLateFeePolicy(ctx, e.OrgID, e.SiteID, billing.PolicyRow{From: "2026-01-01", RateBPSPerMonth: 500, GraceDays: 0}); err != nil {
		t.Fatal(err)
	}
	amount := int64(100000)
	plan, err := w.bill.CreatePlan(ctx, e.OrgID, billing.PlanInput{SiteID: e.SiteID, Name: "Aidat", DueDay: 5, Start: "2026-01-01", AmountMode: "total_allocated", AllocationKey: ledger.Ptr("arsa_payi"), Amount: &amount})
	if err != nil {
		t.Fatal(err)
	}

	e.Clock.Set("2026-03-10")
	if pv, err := w.bill.PreviewPlan(ctx, e.OrgID, plan.ID, ""); err != nil || pv.Total.Amount != 100000 || len(pv.Items) != 2 {
		t.Fatalf("preview %+v %v", pv, err)
	} else {
		for _, it := range pv.Items {
			if it.DoorNumber == "A-2" && it.EffectiveDebtor.DebtorKind != "owner" {
				t.Errorf("A-2 default debtor %+v", it.EffectiveDebtor)
			}
		}
	}
	n, err := w.bill.GenerateCharges(ctx, e.OrgID, e.SiteID)
	if err != nil || n != 6 {
		t.Fatalf("generated %d, %v", n, err)
	}
	if n, _ := w.bill.GenerateCharges(ctx, e.OrgID, e.SiteID); n != 0 {
		t.Errorf("second run generated %d", n)
	}
	if st, _ := w.bill.ChargeGenerationStatus(ctx, e.OrgID, e.SiteID); len(st) != 1 || len(st[0].Missing) != 0 {
		t.Errorf("generation status %+v", st)
	}
	w.invariants(t)

	a1 := billing.Debtor{UnitID: &w.a1, Kind: "owner"}
	// 60000 on A-1 pays the January charge (oldest first), receipt numbers are gap-free per year
	p1, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "bank_transfer", Amount: money.New(60000, "TRY"), ReceivedOn: "2026-03-10"})
	if err != nil {
		t.Fatal(err)
	}
	if *p1.ReceiptNo != "2026-000001" || len(p1.Allocations) != 1 || p1.Unallocated.Amount != 0 {
		t.Fatalf("payment %+v", p1)
	}
	// overpayment becomes credit
	a2 := billing.Debtor{UnitID: &w.a2, Kind: "owner"}
	p2, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a2, Method: "bank_transfer", Amount: money.New(130000, "TRY"), ReceivedOn: "2026-03-10"})
	if err != nil || *p2.ReceiptNo != "2026-000002" {
		t.Fatalf("payment 2 %+v %v", p2, err)
	}
	var credit int64
	_ = e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
		ch, _ := ledger.EnsureChart(ctx, tx, e.OrgID, e.SiteID)
		credit, _ = billing.CreditOf(ctx, tx, e.OrgID, e.SiteID, ch, a2)
		return nil
	})
	if credit != 130000-3*40000 {
		t.Errorf("credit %d", credit)
	}
	w.invariants(t)

	// late fees: accrual on demand, crystallization books it, a second run is a no-op
	// fees are booked monthly, up to the end of the last closed month
	const closed = "2026-02-28"
	acc, err := w.bill.AccrueLateFee(ctx, e.OrgID, e.SiteID, a1, "")
	if err != nil || acc.Accrued.Amount <= 0 {
		t.Fatalf("accrual %+v %v", acc, err)
	}
	if got, err := w.bill.CrystallizeLateFees(ctx, e.OrgID, e.SiteID); err != nil || got == 0 {
		t.Fatalf("crystallize %d %v", got, err)
	}
	if got, _ := w.bill.CrystallizeLateFees(ctx, e.OrgID, e.SiteID); got != 0 {
		t.Errorf("second crystallize posted %d", got)
	}
	acc, _ = w.bill.AccrueLateFee(ctx, e.OrgID, e.SiteID, a1, closed)
	if acc.Unbooked.Amount != 0 || acc.Booked.Amount != acc.Accrued.Amount {
		t.Errorf("after crystallization %+v", acc)
	}
	w.invariants(t)
	feeBefore := acc.Booked.Amount

	// a payment backdated to before the February due date removes the February fee: a reversal is posted
	if _, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "bank_transfer", Amount: money.New(60000, "TRY"), ReceivedOn: "2026-02-04"}); err != nil {
		t.Fatal(err)
	}
	if got, err := w.bill.CrystallizeLateFees(ctx, e.OrgID, e.SiteID); err != nil || got != 1 {
		t.Fatalf("backdated crystallize %d %v", got, err)
	}
	acc, _ = w.bill.AccrueLateFee(ctx, e.OrgID, e.SiteID, a1, closed)
	if acc.Booked.Amount >= feeBefore || acc.Unbooked.Amount != 0 {
		t.Errorf("fee after the backdated payment %+v, was %d", acc, feeBefore)
	}
	w.invariants(t)

	// waiver of part of the remaining fee
	if acc.Booked.Amount > 1 {
		if _, err := w.bill.WaiveLateFee(ctx, e.OrgID, billing.WaiveInput{SiteID: e.SiteID, UnitID: w.a1, DebtorKind: "owner", Amount: 1, Reason: "goodwill"}); err != nil {
			t.Errorf("waiver: %v", err)
		}
		w.invariants(t)
	}

	// void a payment: allocations voided, statuses rebuilt
	if _, err := w.pay.Void(ctx, e.OrgID, p1.ID, "wrong unit"); err != nil {
		t.Fatal(err)
	}
	w.invariants(t)
	if n := e.Int(`SELECT count(*) FROM charge WHERE org_id=$1 AND status='open' AND kind='principal'`, e.OrgID); n == 0 {
		t.Error("voiding left no open charge")
	}
	// rebuild-charge-status reproduces the statuses
	snapshot := e.Int(`SELECT count(*) FROM charge WHERE org_id=$1 AND status='paid'`, e.OrgID)
	e.Exec(`UPDATE charge SET status = 'open' WHERE org_id = $1`, e.OrgID)
	if ch, err := w.bill.RebuildStatus(ctx, e.OrgID, e.SiteID); err != nil || ch == 0 {
		t.Fatalf("rebuild %d %v", ch, err)
	}
	if got := e.Int(`SELECT count(*) FROM charge WHERE org_id=$1 AND status='paid'`, e.OrgID); got != snapshot {
		t.Errorf("paid charges after rebuild %d, before %d", got, snapshot)
	}

	// the lock date refuses new postings
	ld, _ := w.led.GetLockDate(ctx, e.OrgID, e.SiteID)
	if _, err := w.led.SetLockDate(ctx, e.OrgID, e.SiteID, ld.Version, ledger.Ptr("2026-03-01")); err != nil {
		t.Fatal(err)
	}
	_, err = w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "bank_transfer", Amount: money.New(100, "TRY"), ReceivedOn: "2026-02-20"})
	if problem.From(err).Code != problem.CodeLockedPeriod {
		t.Errorf("payment inside the lock: %v", err)
	}
}

func TestIntegrationOpeningBalancesAndCancel(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx
	ob, err := w.bill.CreateOpeningBalance(ctx, e.OrgID, billing.OpeningInput{SiteID: e.SiteID, AsOf: "2025-12-31", Kind: "unit_receivable", UnitID: &w.a1, DebtorKind: "owner", Principal: 25000, LateFee: 500}, false)
	if err != nil {
		t.Fatal(err)
	}
	if dry, err := w.bill.CreateOpeningBalance(moneytest.WithoutReauth(ctx), e.OrgID, billing.OpeningInput{SiteID: e.SiteID, AsOf: "2025-12-31", Kind: "unit_receivable", UnitID: &w.a2, DebtorKind: "owner", Principal: 100}, true); err != nil || dry.JournalEntryID != uuid.Nil {
		t.Errorf("dry run %+v %v", dry, err)
	}
	if _, err := w.bill.CreateOpeningBalance(moneytest.WithoutReauth(ctx), e.OrgID, billing.OpeningInput{SiteID: e.SiteID, AsOf: "2025-12-31", Kind: "unit_receivable", UnitID: &w.a2, DebtorKind: "owner", Principal: 100}, false); problem.From(err).Code != problem.CodeReauthRequired {
		t.Errorf("real posting without re-auth: %v", err)
	}
	w.invariants(t)
	if bal := e.Int(`SELECT COALESCE(SUM(debit - credit), 0) FROM journal_line l JOIN account a ON a.org_id=l.org_id AND a.id=l.account_id WHERE l.org_id=$1 AND a.subtype='opening_equity'`, e.OrgID); bal != -25500 {
		t.Errorf("opening equity %d", bal)
	}
	// a payment settles part of it; voiding then refuses
	a1 := billing.Debtor{UnitID: &w.a1, Kind: "owner"}
	e.Clock.Set("2026-01-05")
	if _, err := w.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "bank_transfer", Amount: money.New(1000, "TRY"), ReceivedOn: "2026-01-02"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.bill.VoidOpeningBalance(ctx, e.OrgID, ob.ID, "test"); problem.From(err).Status != 409 {
		t.Errorf("void with payments: %v", err)
	}
	// a fresh balance can be voided, twice (idempotent)
	ob2, _ := w.bill.CreateOpeningBalance(ctx, e.OrgID, billing.OpeningInput{SiteID: e.SiteID, AsOf: "2025-12-31", Kind: "unit_receivable", UnitID: &w.a2, DebtorKind: "owner", Principal: 700}, false)
	for i := 0; i < 2; i++ {
		if v, err := w.bill.VoidOpeningBalance(ctx, e.OrgID, ob2.ID, "test"); err != nil || !v.Voided {
			t.Fatalf("void %d: %+v %v", i, v, err)
		}
	}
	w.invariants(t)
	// cancel an unpaid charge by reversal
	cs, _, _ := w.bill.ListCharges(ctx, e.OrgID, e.SiteID, billing.ChargeFilter{Status: "open"}, ledger.PageParams{})
	if len(cs) == 0 {
		t.Fatal("no open charge")
	}
	if c, err := w.bill.CancelCharge(ctx, e.OrgID, cs[len(cs)-1].ID, "test"); err != nil || c.Status != "cancelled" {
		t.Errorf("cancel %+v %v", c, err)
	}
	w.invariants(t)
}
