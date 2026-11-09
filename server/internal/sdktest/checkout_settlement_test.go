package sdktest_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/checkout"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
)

// The example provider's payouts have stable refs: the sync job asks for overlapping periods every few hours and
// must never post a payout twice, and a transaction paid after a payout joins the next one.
func TestSettlementSyncJobIsSafeToRunRepeatedlyWithOverlappingWindows(t *testing.T) {
	e := newCheckoutEnv(t)
	e.charge("owner", 40000, "c")
	e.paid(e.create("owner"), 40000, 1200)
	ctx := context.Background()
	wide := time.Now().UTC().AddDate(0, 0, -14).Format("2006-01-02")

	if posted, failed, err := e.chk.SyncSettlements(ctx, wide, today()); err != nil || failed != 0 || posted != 1 {
		t.Fatalf("first run: %d posted, %d failed, %v", posted, failed, err)
	}
	for _, from := range []string{wide, time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02"), today()} {
		if posted, _, err := e.chk.SyncSettlements(ctx, from, today()); err != nil || posted != 0 {
			t.Fatalf("a run over %s..%s posted %d, %v", from, today(), posted, err)
		}
	}
	if bank := e.bal(e.Env.SiteID, "1100"); bank != 38800 {
		t.Fatalf("bank = %d after repeated runs, want 38800 once", bank)
	}
	// a payment after the first payout makes the next one
	e.charge("owner", 10000, "d")
	e.paid(e.create("owner"), 10000, 100)
	if posted, _, err := e.chk.SyncSettlements(ctx, wide, today()); err != nil || posted != 1 {
		t.Fatalf("run after a new payment posted %d, %v", posted, err)
	}
	if posted, _, _ := e.chk.SyncSettlements(ctx, wide, today()); posted != 0 {
		t.Errorf("a repeat posted %d", posted)
	}
	if bank := e.bal(e.Env.SiteID, "1100"); bank != 38800+9900 {
		t.Errorf("bank = %d, want %d", bank, 38800+9900)
	}
	e.AssertLedgerInvariants()
}

// Two sites that collect into their own accounts each get their own payouts, even when the provider's refs coincide.
func TestSettlementSiteModeIsIdempotentPerSite(t *testing.T) {
	e := newCheckoutEnv(t)
	site2, unit2 := e.secondSite()
	e.charge("owner", 40000, "c")
	e.paid(e.create("owner"), 40000, 1200)
	amt := money.New(25000, "TRY")
	c2, err := e.chk.Create(e.Ctx(), e.Env.OrgID, checkout.CreateInput{UnitID: unit2, DebtorKind: "owner", Amount: &amt})
	if err != nil {
		t.Fatal(err)
	}
	e.paid(c2, 25000, 750)

	posted, failed, err := e.chk.SyncSettlements(context.Background(), today(), today())
	if err != nil || failed != 0 || posted != 2 {
		t.Fatalf("SyncSettlements = %d posted, %d failed, %v", posted, failed, err)
	}
	// each site's own account paid out its own transactions only
	if a, b := e.bal(e.Env.SiteID, "1100"), e.bal(site2, "1100"); a != 38800 || b != 24250 {
		t.Errorf("banks = %d and %d, want 38800 and 24250", a, b)
	}
	if posted, _, _ := e.chk.SyncSettlements(context.Background(), today(), today()); posted != 0 {
		t.Errorf("a second run posted %d", posted)
	}

	// the same ref on two sites is two payouts (two provider accounts), the same ref on one site a replay
	sys := ledger.SystemContext(e.Ctx(), e.Env.OrgID)
	post := func(site uuid.UUID) payments.SettlementResult {
		res, err := e.Payments.RecordProviderSettlement(sys, e.Env.OrgID, payments.SettlementInput{Provider: "example", SiteID: site, Ref: "po_shared", SettledOn: today(), Net: money.New(100, "TRY")})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if post(e.Env.SiteID).Duplicate || post(site2).Duplicate {
		t.Error("the same ref of another site was taken for a replay")
	}
	if !post(e.Env.SiteID).Duplicate || !post(site2).Duplicate {
		t.Error("a replay of a payout was posted again")
	}
	e.AssertLedgerInvariants()
}

// An organization payout covering several sites splits by the payments it covers, exactly: the postings sum to
// the payout whatever the remainder, for a partial payout (scaled down) and for one with more than the payments.
func TestSettlementOrganizationSplitSumsExactly(t *testing.T) {
	e := newCheckoutEnv(t)
	e.Env.Exec(`UPDATE organization SET settings = settings || '{"payments.merchant_of_record":"organization"}'::jsonb WHERE id = $1`, e.Env.OrgID)
	site2, unit2 := e.secondSite()
	site3, unit3 := e.secondSite()
	type pay struct {
		site  uuid.UUID
		unit  uuid.UUID
		gross int64
	}
	var txns []string
	for _, p := range []pay{{e.Env.SiteID, e.unit, 3334}, {site2, unit2, 3333}, {site3, unit3, 3333}} {
		amt := money.New(p.gross, "TRY")
		c, err := e.chk.Create(e.Ctx(), e.Env.OrgID, checkout.CreateInput{UnitID: p.unit, DebtorKind: "owner", Amount: &amt})
		if err != nil {
			t.Fatal(err)
		}
		e.paid(c, p.gross, 0)
		txns = append(txns, e.txnOf(c))
	}
	sys := ledger.SystemContext(e.Ctx(), e.Env.OrgID)
	settle := func(ref string, net int64) []payments.SettlementPosting {
		t.Helper()
		res, err := e.Payments.RecordProviderSettlement(sys, e.Env.OrgID, payments.SettlementInput{Provider: "example", SiteID: e.Env.SiteID, Ref: ref, SettledOn: today(), Net: money.New(net, "TRY"), TxnIDs: txns})
		if err != nil {
			t.Fatal(err)
		}
		var sum int64
		for _, p := range res.Postings {
			sum += p.Net
		}
		if sum != net {
			t.Fatalf("payout %s of %d was split into %+v (sum %d)", ref, net, res.Postings, sum)
		}
		return res.Postings
	}
	// less than the payments (an adjustment at the provider): scaled, remainder to one site, nothing lost
	settle("po_partial", 9999)
	// exactly the payments: each site gets its own
	exact := settle("po_exact", 10000)
	for _, p := range exact {
		want := map[uuid.UUID]int64{e.Env.SiteID: 3334, site2: 3333, site3: 3333}[p.SiteID]
		if p.Net != want {
			t.Errorf("site %s got %d of an exact payout, want %d", p.SiteID, p.Net, want)
		}
	}
	// more than the payments: the surplus stays with the site the payout was reported for
	more := settle("po_more", 10005)
	for _, p := range more {
		want := map[uuid.UUID]int64{e.Env.SiteID: 3334 + 5, site2: 3333, site3: 3333}[p.SiteID]
		if p.Net != want {
			t.Errorf("site %s got %d of the larger payout, want %d", p.SiteID, p.Net, want)
		}
	}
	e.AssertLedgerInvariants()
}
