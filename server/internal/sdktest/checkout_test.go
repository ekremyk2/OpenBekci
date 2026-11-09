package sdktest_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/packs/example"
	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/checkout"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
	"github.com/openbekci/openbekci/server/internal/sdktest"
)

// The provider-agnostic checkout, end to end against the example pack's fake provider: create, webhook, status,
// replay, failure, expiry, refund and settlement under both merchant-of-record modes.

type notes struct {
	mu   sync.Mutex
	list []ledger.Notification
}

func (n *notes) Enqueue(_ context.Context, _ pgxTx, nt ledger.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.list = append(n.list, nt)
	return nil
}

func (n *notes) count(typ string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, x := range n.list {
		if x.Type == typ {
			c++
		}
	}
	return c
}

type receipts struct {
	mu  sync.Mutex
	ids []uuid.UUID
}

func (r *receipts) FileReceipt(_ context.Context, _, payment uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, payment)
	return nil
}

type cenv struct {
	*sdktest.Harness
	chk  *checkout.Service
	note *notes
	rcpt *receipts
	unit uuid.UUID
}

func newCheckoutEnv(t *testing.T) *cenv {
	t.Helper()
	h := sdktest.New(t, example.New(), sdktest.WithSettings(settings))
	e := &cenv{Harness: h, note: &notes{}, rcpt: &receipts{}}
	h.Payments.Notifier = e.note
	e.chk = checkout.NewService(checkout.Config{DB: h.Env.DB, Platform: h.Env.DB, Payments: h.Payments, Providers: h.Host, Receipts: e.rcpt, BaseURL: "https://app.example.test"})
	h.Host.AttachCheckouts(e.chk)
	e.unit = uuid.MustParse(string(h.Unit("A-1")))
	return e
}

func (e *cenv) charge(side string, amount int64, desc string) uuid.UUID {
	e.T.Helper()
	c, err := e.Billing.CreateCharge(ledger.SystemContext(e.Ctx(), e.Env.OrgID), e.Env.OrgID, billing.ChargeInput{SiteID: e.Env.SiteID, UnitID: e.unit, ChargeType: "special_assessment",
		DebtorKind: side, Amount: money.New(amount, "TRY"), DueDate: "2026-01-01", Description: desc})
	if err != nil {
		e.T.Fatal(err)
	}
	return c.ID
}

func (e *cenv) create(side string, charges ...uuid.UUID) checkout.Checkout {
	e.T.Helper()
	c, err := e.chk.Create(e.Ctx(), e.Env.OrgID, checkout.CreateInput{UnitID: e.unit, DebtorKind: side, ChargeIDs: charges})
	if err != nil {
		e.T.Fatalf("create checkout: %v", err)
	}
	return c
}

func (e *cenv) txnOf(c checkout.Checkout) string {
	var txn string
	e.Env.Query([]any{&txn}, `SELECT provider_txn_id FROM checkout WHERE id = $1`, c.ID)
	return txn
}

func (e *cenv) hook(body map[string]any) int {
	e.T.Helper()
	raw, _ := json.Marshal(body)
	return e.Webhook("example", raw, sdktest.Header(example.SignatureHeader, example.Sign(whSec, raw))).Code
}

func (e *cenv) paid(c checkout.Checkout, gross, fee int64) int {
	// the event names another unit side on purpose: a checkout is recorded for what it was created for
	return e.hook(map[string]any{"event": "paid", "txn": e.txnOf(c), "gross": gross, "fee": fee, "currency": "TRY",
		"site": e.Env.SiteID.String(), "unit": e.unit.String(), "debtor_kind": "owner"})
}

func (e *cenv) get(c checkout.Checkout) checkout.Checkout {
	e.T.Helper()
	got, err := e.chk.Get(e.Ctx(), e.Env.OrgID, c.ID)
	if err != nil {
		e.T.Fatalf("get checkout: %v", err)
	}
	return got
}

func TestCheckoutPaidWebhookRecordsAllocatesAndReplaysIdempotently(t *testing.T) {
	e := newCheckoutEnv(t)
	owner := e.charge("owner", 50000, "owner charge")
	tenant := e.charge("tenant", 30000, "tenant charge")

	c := e.create("tenant", tenant)
	if c.Status != checkout.StatusPending || c.Amount.Amount != 30000 || c.CheckoutURL == nil || *c.CheckoutURL == "" || c.Provider != "example" || c.MerchantOfRecord != "site" {
		t.Fatalf("checkout = %+v", c)
	}
	if want := "https://app.example.test/orgs/" + e.Env.OrgID.String() + "/pay/return?checkout_id=" + c.ID.String(); c.ReturnURL != want {
		t.Errorf("return_url = %s, want %s", c.ReturnURL, want)
	}
	if e.get(c).Status != checkout.StatusPending {
		t.Fatal("a fresh checkout is not pending")
	}

	if code := e.paid(c, 30000, 900); code != 200 {
		t.Fatalf("webhook: %d", code)
	}
	got := e.get(c)
	if got.Status != checkout.StatusPaid || got.PaymentID == nil {
		t.Fatalf("after the webhook: %+v", got)
	}
	pid := *got.PaymentID
	if n := e.Env.Int(`SELECT count(*) FROM payment WHERE id = $1 AND method = 'card:example' AND status = 'confirmed' AND gross = 30000 AND fee = 900 AND net = 29100 AND receipt_no IS NOT NULL AND debtor_kind = 'tenant' AND unit_id = $2`, pid, e.unit); n != 1 {
		t.Fatalf("the payment is not the tenant's card payment of the checkout (%d)", n)
	}
	// allocated to the selected charge only; the owner's charge is untouched
	if n := e.Env.Int(`SELECT count(*) FROM payment_allocation WHERE payment_id = $1 AND charge_id = $2 AND amount = 30000 AND NOT voided`, pid, tenant); n != 1 {
		t.Errorf("the tenant charge was not paid by the payment (%d)", n)
	}
	if n := e.Env.Int(`SELECT count(*) FROM payment_allocation WHERE charge_id = $1`, owner); n != 0 {
		t.Errorf("the owner's charge was allocated (%d)", n)
	}
	if e.note.count("payment.confirmed") != 1 || len(e.rcpt.ids) != 1 || e.rcpt.ids[0] != pid {
		t.Errorf("notifications %d, receipts filed %v", e.note.count("payment.confirmed"), e.rcpt.ids)
	}
	if n := e.Env.Int(`SELECT count(*) FROM audit_event WHERE org_id = $1 AND action = 'payment.confirm' AND target_id = $2`, e.Env.OrgID, pid); n != 1 {
		t.Errorf("payment.confirm audit events (the webhook event source) = %d", n)
	}

	// replays: acknowledged, nothing recorded or sent twice
	for i := 0; i < 2; i++ {
		if code := e.paid(c, 30000, 900); code != 200 {
			t.Fatalf("replay: %d", code)
		}
	}
	if n := e.Env.Int(`SELECT count(*) FROM payment WHERE org_id = $1 AND provider_txn_id = $2`, e.Env.OrgID, e.txnOf(c)); n != 1 {
		t.Errorf("%d payments after replays", n)
	}
	if e.note.count("payment.confirmed") != 1 || len(e.rcpt.ids) != 1 {
		t.Errorf("replays notified %d times and filed %d receipts", e.note.count("payment.confirmed"), len(e.rcpt.ids))
	}
	if e.get(c).Status != checkout.StatusPaid {
		t.Error("a replay changed the status")
	}
	e.AssertLedgerInvariants()
}

func TestCheckoutAmountAndChargeValidation(t *testing.T) {
	e := newCheckoutEnv(t)
	owner := e.charge("owner", 50000, "owner charge")

	// no selection: everything open on the side
	if c := e.create("owner"); c.Amount.Amount != 50000 {
		t.Errorf("default amount = %d", c.Amount.Amount)
	}
	// a charge of the other side is not open on this one
	tenant := e.charge("tenant", 100, "tenant")
	if _, err := e.chk.Create(e.Ctx(), e.Env.OrgID, checkout.CreateInput{UnitID: e.unit, DebtorKind: "owner", ChargeIDs: []uuid.UUID{tenant}}); err == nil {
		t.Error("a charge of the other side was accepted")
	}
	// nothing to pay
	if _, err := e.chk.Create(e.Ctx(), e.Env.OrgID, checkout.CreateInput{UnitID: e.unit, DebtorKind: "tenant", ChargeIDs: nil, Amount: &money.Money{Amount: -1, Currency: "TRY"}}); err == nil {
		t.Error("a negative amount was accepted")
	}
	// an explicit amount with a charge wins
	amt := money.New(20000, "TRY")
	c, err := e.chk.Create(e.Ctx(), e.Env.OrgID, checkout.CreateInput{UnitID: e.unit, DebtorKind: "owner", ChargeIDs: []uuid.UUID{owner}, Amount: &amt, Client: checkout.ClientMobile})
	if err != nil || c.Amount.Amount != 20000 {
		t.Fatalf("explicit amount: %+v, %v", c, err)
	}
	if want := "openbekci://pay/return?org_id=" + e.Env.OrgID.String() + "&checkout_id=" + c.ID.String(); c.ReturnURL != want {
		t.Errorf("mobile return_url = %s", c.ReturnURL)
	}
	// wrong currency
	if _, err := e.chk.Create(e.Ctx(), e.Env.OrgID, checkout.CreateInput{UnitID: e.unit, DebtorKind: "owner", Amount: &money.Money{Amount: 100, Currency: "EUR"}}); err == nil {
		t.Error("a foreign currency was accepted")
	}
}

func TestCheckoutNoProviderEnabled(t *testing.T) {
	h := sdktest.New(t, example.New(), sdktest.WithoutEnable())
	chk := checkout.NewService(checkout.Config{DB: h.Env.DB, Platform: h.Env.DB, Payments: h.Payments, Providers: h.Host, BaseURL: "https://app.example.test"})
	unit := uuid.MustParse(string(h.Unit("A-1")))
	_, err := chk.Create(h.Ctx(), h.Env.OrgID, checkout.CreateInput{UnitID: unit, DebtorKind: "owner", Amount: &money.Money{Amount: 100, Currency: "TRY"}})
	assertProvider409(t, err)
	// the availability the site settings report
	v, err := chk.SiteViewOf(h.Ctx(), h.Env.OrgID, h.Env.SiteID)
	if err != nil || v.CardAvailable || v.MerchantOfRecord != "site" {
		t.Errorf("site view without a provider = %+v, %v", v, err)
	}
	h.Enable(settings)
	if v, err = chk.SiteViewOf(h.Ctx(), h.Env.OrgID, h.Env.SiteID); err != nil || !v.CardAvailable || v.Provider != "example" {
		t.Errorf("site view with the pack = %+v, %v", v, err)
	}
}

func TestCheckoutFailedThenLatePaymentAndStatusPollFindsLostWebhook(t *testing.T) {
	e := newCheckoutEnv(t)
	e.charge("owner", 40000, "c")
	c := e.create("owner")
	if code := e.hook(map[string]any{"event": "failed", "txn": e.txnOf(c)}); code != 200 {
		t.Fatalf("failed webhook: %d", code)
	}
	got := e.get(c)
	if got.Status != checkout.StatusFailed || got.FailureCode == nil || *got.FailureCode != "provider_declined" || got.PaymentID != nil {
		t.Fatalf("after failure: %+v", got)
	}
	if e.Env.Int(`SELECT count(*) FROM payment WHERE org_id = $1`, e.Env.OrgID) != 0 {
		t.Error("a failed checkout recorded a payment")
	}
	// the money moves after all (a retry at the provider): paid wins over failed
	if code := e.paid(c, 40000, 0); code != 200 {
		t.Fatalf("late paid: %d", code)
	}
	if got := e.get(c); got.Status != checkout.StatusPaid || got.PaymentID == nil {
		t.Fatalf("after the late payment: %+v", got)
	}

	// a payment whose webhook is lost: the poll asks the provider and records it
	c2 := e.create("owner", e.charge("owner", 10000, "d"))
	e.Env.Exec(`UPDATE pack_example_txn SET state = 'paid', gross = 10000, fee = 100 WHERE txn = $1`, e.txnOf(c2))
	got2 := e.get(c2)
	if got2.Status != checkout.StatusPaid || got2.PaymentID == nil {
		t.Fatalf("poll after a lost webhook: %+v", got2)
	}
	if n := e.Env.Int(`SELECT count(*) FROM payment WHERE id = $1 AND gross = 10000 AND fee = 100`, *got2.PaymentID); n != 1 {
		t.Error("the polled payment has not the provider's gross and fee")
	}
	e.AssertLedgerInvariants()
}

func TestCheckoutExpiredByTheJobAndLatePayment(t *testing.T) {
	e := newCheckoutEnv(t)
	e.charge("owner", 40000, "c")
	fresh := e.create("owner")
	stale := e.create("owner")
	e.Env.Exec(`UPDATE checkout SET expires_at = now() - interval '2 minutes' WHERE id = $1`, stale.ID)
	n, err := e.chk.ExpireStale(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("ExpireStale = %d, %v", n, err)
	}
	if e.get(fresh).Status != checkout.StatusPending {
		t.Error("a checkout inside its expiry was expired")
	}
	if got := e.get(stale); got.Status != checkout.StatusExpired {
		t.Fatalf("stale checkout = %+v", got)
	}
	// an expired checkout that gets paid anyway is recorded
	if code := e.paid(stale, 40000, 400); code != 200 {
		t.Fatalf("late paid: %d", code)
	}
	if got := e.get(stale); got.Status != checkout.StatusPaid || got.PaymentID == nil {
		t.Fatalf("after the late payment: %+v", got)
	}
	// the poll expires a pending one past its time without waiting for the job
	e.Env.Exec(`UPDATE checkout SET expires_at = now() - interval '1 minute' WHERE id = $1`, fresh.ID)
	if got := e.get(fresh); got.Status != checkout.StatusExpired {
		t.Errorf("poll of an overdue checkout = %s", got.Status)
	}
	e.AssertLedgerInvariants()
}

func TestCheckoutRefund(t *testing.T) {
	e := newCheckoutEnv(t)
	charge := e.charge("owner", 40000, "c")
	c := e.create("owner", charge)
	e.paid(c, 40000, 1200)
	pid := *e.get(c).PaymentID
	var version int
	e.Env.Query([]any{&version}, `SELECT version FROM payment WHERE id = $1`, pid)

	if _, err := e.chk.Refund(e.Ctx(), e.Env.OrgID, pid, version+5, "stale"); err == nil {
		t.Fatal("a stale If-Match was accepted")
	} else if pe := (*problem.Error)(nil); !asProblem(err, &pe) || pe.Code != problem.CodeVersionConflict {
		t.Fatalf("stale If-Match: %v", err)
	}
	p, err := e.chk.Refund(e.Ctx(), e.Env.OrgID, pid, version, "customer asked")
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if p.Status != "refunded" || p.Version != version+1 {
		t.Fatalf("refunded payment = %+v", p)
	}
	if got := e.get(c); got.Status != checkout.StatusRefunded {
		t.Errorf("checkout after the refund = %s", got.Status)
	}
	// reversal posted: gross out of clearing, fee reversed, allocations released
	if d, cr := e.Balance("1190.example"); d-cr != 0 {
		t.Errorf("clearing balance after the refund = %d", d-cr)
	}
	if n := e.Env.Int(`SELECT count(*) FROM payment_allocation WHERE payment_id = $1 AND NOT voided`, pid); n != 0 {
		t.Errorf("%d allocations still live", n)
	}
	// a replay returns the refunded payment without a second reversal
	entries := e.Env.Int(`SELECT count(*) FROM journal_entry WHERE org_id = $1`, e.Env.OrgID)
	if again, err := e.chk.Refund(e.Ctx(), e.Env.OrgID, pid, version, ""); err != nil || again.Status != "refunded" {
		t.Fatalf("replay: %+v, %v", again, err)
	}
	if e.Env.Int(`SELECT count(*) FROM journal_entry WHERE org_id = $1`, e.Env.OrgID) != entries {
		t.Error("the replay posted another reversal")
	}
	// the manager's name is in the audit
	if n := e.Env.Int(`SELECT count(*) FROM audit_event WHERE org_id = $1 AND action = 'payment.refund_issue' AND target_id = $2 AND actor_user_id IS NOT NULL`, e.Env.OrgID, pid); n != 1 {
		t.Errorf("payment.refund_issue audit events with an actor = %d", n)
	}
	// a cash payment is not refunded through a provider
	cash := e.RecordPayment(sdk.ID(e.unit.String()), 500)
	if _, err := e.chk.Refund(e.Ctx(), e.Env.OrgID, uuid.MustParse(string(cash)), 1, ""); err == nil {
		t.Error("a cash payment was refunded through the provider")
	}
	e.AssertLedgerInvariants()
}

func TestCheckoutProviderRefundWebhookMarksCheckoutRefunded(t *testing.T) {
	e := newCheckoutEnv(t)
	c := e.create("owner", e.charge("owner", 40000, "c"))
	e.paid(c, 40000, 0)
	if code := e.hook(map[string]any{"event": "refunded", "txn": e.txnOf(c), "refund_id": "rf_1"}); code != 200 {
		t.Fatalf("refund webhook: %d", code)
	}
	if got := e.get(c); got.Status != checkout.StatusRefunded {
		t.Fatalf("checkout = %s", got.Status)
	}
	e.AssertLedgerInvariants()
}

// secondSite adds a site (with a unit) to the fixture organization.
func (e *cenv) secondSite() (site, unit uuid.UUID) {
	site, unit = id.New(), id.New()
	block := id.New()
	e.Env.Exec(`INSERT INTO site (id, org_id, name) VALUES ($1, $2, 'Second')`, site, e.Env.OrgID)
	e.Env.Exec(`INSERT INTO block (id, org_id, site_id, name, hidden) VALUES ($1, $2, $3, 'Main', true)`, block, e.Env.OrgID, site)
	e.Env.Exec(`INSERT INTO unit (id, org_id, site_id, block_id, door_number) VALUES ($1, $2, $3, $4, 'B-1')`, unit, e.Env.OrgID, site, block)
	return site, unit
}

func (e *cenv) bal(site uuid.UUID, code string) int64 {
	e.T.Helper()
	d, c, err := e.Host.Services(e.PackID).Ledger().Balance(e.PackCtx(), sdk.ID(site.String()), code)
	if err != nil {
		return 0 // the account does not exist: nothing was posted there
	}
	return d - c
}

func today() string { return time.Now().UTC().Format("2006-01-02") }

func assertProvider409(t *testing.T, err error) {
	t.Helper()
	var pe *problem.Error
	if !asProblem(err, &pe) || pe.Status != 409 || pe.Code != problem.CodePaymentProviderUnavailable {
		t.Fatalf("error = %v, want 409 payment_provider_unavailable", err)
	}
}

func (e *cenv) sync(site uuid.UUID, mor string) int {
	e.T.Helper()
	req := sdk.SettlementRequest{From: today(), To: today(), MerchantOfRecord: mor}
	if mor == sdk.MerchantSite {
		req.Site = sdk.ID(site.String())
	}
	n, err := e.Host.SyncSettlements(e.Ctx(), e.Env.OrgID, e.PackID, "example", sdk.ID(site.String()), req)
	if err != nil {
		e.T.Fatalf("SyncSettlements: %v", err)
	}
	return n
}

func TestSettlementPostsBankAgainstClearingWhenTheSiteIsMerchant(t *testing.T) {
	e := newCheckoutEnv(t)
	e.charge("owner", 40000, "c")
	c := e.create("owner")
	e.paid(c, 40000, 1200) // net 38800

	cl, err := e.Payments.ClearingBalancesOf(e.Ctx(), e.Env.OrgID, e.Env.SiteID)
	if err != nil || cl.MerchantOfRecord != "site" || len(cl.Providers) != 1 || cl.Providers[0].Provider != "example" || cl.Providers[0].Balance.Amount != 38800 || cl.ClientMoneyOwed.Amount != 0 {
		t.Fatalf("clearing before the payout = %+v, %v", cl, err)
	}
	if n := e.sync(e.Env.SiteID, sdk.MerchantSite); n != 1 {
		t.Fatalf("posted %d settlements", n)
	}
	if bank := e.bal(e.Env.SiteID, "1100"); bank != 38800 {
		t.Errorf("bank = %d, want the net 38800", bank)
	}
	if clr := e.bal(e.Env.SiteID, "1190.example"); clr != 0 {
		t.Errorf("clearing = %d after the payout", clr)
	}
	if cm := e.bal(e.Env.SiteID, ledger.ClientMoneyCode); cm != 0 {
		t.Errorf("client money = %d with the site as merchant", cm)
	}
	if n := e.sync(e.Env.SiteID, sdk.MerchantSite); n != 0 {
		t.Errorf("a second sync posted %d settlements", n)
	}
	rows, err := e.Payments.ListSettlements(e.Ctx(), e.Env.OrgID, e.Env.SiteID, "", 10, nil)
	if err != nil || len(rows) != 1 || rows[0].Net.Amount != 38800 || rows[0].MerchantOfRecord != "site" {
		t.Fatalf("settlements = %+v, %v", rows, err)
	}
	e.AssertLedgerInvariants()
}

// The periodic job (checkout.SyncSettlements): each site that collects into its own account is asked on its own.
func TestSettlementSyncJobSiteMode(t *testing.T) {
	e := newCheckoutEnv(t)
	e.charge("owner", 40000, "c")
	e.paid(e.create("owner"), 40000, 1200)
	posted, failed, err := e.chk.SyncSettlements(context.Background(), today(), today())
	if err != nil || failed != 0 || posted != 1 {
		t.Fatalf("SyncSettlements = %d posted, %d failed, %v", posted, failed, err)
	}
	if bank := e.bal(e.Env.SiteID, "1100"); bank != 38800 {
		t.Errorf("bank = %d", bank)
	}
	if posted, _, _ := e.chk.SyncSettlements(context.Background(), today(), today()); posted != 0 {
		t.Errorf("the second run posted %d", posted)
	}
	e.AssertLedgerInvariants()
}

// ... and the organization's account is asked once for all the sites that collect centrally.
func TestSettlementSyncJobOrganizationMode(t *testing.T) {
	e := newCheckoutEnv(t)
	e.Env.Exec(`UPDATE organization SET settings = settings || '{"payments.merchant_of_record":"organization"}'::jsonb WHERE id = $1`, e.Env.OrgID)
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
	if err != nil || failed != 0 || posted != 1 {
		t.Fatalf("SyncSettlements = %d posted, %d failed, %v", posted, failed, err)
	}
	if a, b := e.bal(e.Env.SiteID, ledger.ClientMoneyCode), e.bal(site2, ledger.ClientMoneyCode); a != 38800 || b != 24250 {
		t.Errorf("client money = %d and %d", a, b)
	}
	if posted, _, _ := e.chk.SyncSettlements(context.Background(), today(), today()); posted != 0 {
		t.Errorf("the second run posted %d", posted)
	}
	e.AssertLedgerInvariants()
}

func TestSettlementPostsClientMoneyPerSiteWhenTheOrganizationIsMerchant(t *testing.T) {
	e := newCheckoutEnv(t)
	e.Env.Exec(`UPDATE organization SET settings = settings || '{"payments.merchant_of_record":"organization"}'::jsonb WHERE id = $1`, e.Env.OrgID)
	site2, unit2 := e.secondSite()

	e.charge("owner", 40000, "c")
	c1 := e.create("owner")
	if c1.MerchantOfRecord != "organization" {
		t.Fatalf("checkout merchant = %s", c1.MerchantOfRecord)
	}
	e.paid(c1, 40000, 1200) // site 1 net 38800
	amt := money.New(25000, "TRY")
	c2, err := e.chk.Create(e.Ctx(), e.Env.OrgID, checkout.CreateInput{UnitID: unit2, DebtorKind: "owner", Amount: &amt})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"event": "paid", "txn": e.txnOf(c2), "gross": 25000, "fee": 750, "currency": "TRY"}) // site 2 net 24250
	if code := e.Webhook("example", raw, sdktest.Header(example.SignatureHeader, example.Sign(whSec, raw))).Code; code != 200 {
		t.Fatalf("webhook: %d", code)
	}

	// one payout of the organization's account covers both sites
	if n := e.sync(e.Env.SiteID, sdk.MerchantOrganization); n != 1 {
		t.Fatalf("posted %d settlements", n)
	}
	for _, s := range []struct {
		site uuid.UUID
		net  int64
	}{{e.Env.SiteID, 38800}, {site2, 24250}} {
		if cm := e.bal(s.site, ledger.ClientMoneyCode); cm != s.net {
			t.Errorf("client money of %s = %d, want %d", s.site, cm, s.net)
		}
		if clr := e.bal(s.site, "1190.example"); clr != 0 {
			t.Errorf("clearing of %s = %d after the payout", s.site, clr)
		}
		if bank := e.bal(s.site, "1100"); bank != 0 {
			t.Errorf("bank of %s = %d: the money is in the organization's account, not the site's", s.site, bank)
		}
	}
	cl, err := e.Payments.ClearingBalancesOf(e.Ctx(), e.Env.OrgID, site2)
	if err != nil || cl.MerchantOfRecord != "organization" || cl.ClientMoneyOwed.Amount != 24250 {
		t.Fatalf("clearing view of site 2 = %+v, %v", cl, err)
	}
	if n := e.Env.Int(`SELECT count(*) FROM provider_settlement WHERE org_id = $1 AND merchant_of_record = 'organization'`, e.Env.OrgID); n != 2 {
		t.Errorf("settlement rows = %d, want one per site", n)
	}
	if n := e.sync(e.Env.SiteID, sdk.MerchantOrganization); n != 0 {
		t.Errorf("a second sync posted %d settlements", n)
	}

	// a site that overrides the organization collects into its own account
	e.Env.Exec(`UPDATE site SET settings = settings || '{"payments.merchant_of_record":"site"}'::jsonb WHERE id = $1`, site2)
	if m, err := e.Payments.MerchantOfRecord(e.Ctx(), e.Env.OrgID, site2); err != nil || m != "site" {
		t.Errorf("merchant of site 2 = %s, %v", m, err)
	}
	if m, err := e.Payments.MerchantOfRecord(e.Ctx(), e.Env.OrgID, e.Env.SiteID); err != nil || m != "organization" {
		t.Errorf("merchant of site 1 = %s, %v", m, err)
	}
	e.AssertLedgerInvariants()
}
