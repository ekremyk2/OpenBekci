package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/packs/example"
	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/checkout"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/packs"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Card checkout over HTTP with the example pack's fake provider: who may pay what, the idempotent create, the
// status poll, the webhook, refunds, the merchant-of-record setting and the capability flag.

type coNotes struct {
	mu    sync.Mutex
	types []string
}

func (n *coNotes) Enqueue(_ context.Context, _ pgx.Tx, nt ledger.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.types = append(n.types, nt.Type)
	return nil
}

func (n *coNotes) count(typ string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, t := range n.types {
		if t == typ {
			c++
		}
	}
	return c
}

type coReceipts struct {
	mu  sync.Mutex
	ids []uuid.UUID
}

func (r *coReceipts) FileReceipt(_ context.Context, _, payment uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, payment)
	return nil
}

const coSecret = "whsec_checkout_http_1"

type coEnv struct {
	*pb
	f                                           authztest.SiteFix
	notes                                       *coNotes
	rcpt                                        *coReceipts
	owner, tenant, hhNo, hhOk, manager, siteMgr string // bearer tokens
	ownerCharge, tenantCharge                   uuid.UUID
}

func newCoEnv(t *testing.T) *coEnv {
	t.Helper()
	p := newPB(t)
	e := p.e
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	notes, rcpt := &coNotes{}, &coReceipts{}
	pay := payments.NewService(e.db, nil, notes)
	host, err := packs.New(packs.Config{DB: e.db, Log: log, EncryptionKey: []byte(testEncryptionKey), Payments: pay}, []sdk.Pack{example.New()})
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := host.MigrateUp(t.Context()); err != nil {
		t.Fatal(err)
	}
	host.AttachPayments(pay)
	chk := checkout.NewService(checkout.Config{DB: e.db, Platform: e.db, Payments: pay, Providers: host, Receipts: rcpt, BaseURL: "https://app.example.test"})
	host.AttachCheckouts(chk)
	e.deps.Packs = host
	e.deps.Money.Payments, e.deps.Money.Checkout = pay, chk
	e.h = NewRouter(e.deps, Options{})
	restore := audit.SetSink(e.integ.EmitFromAudit)
	t.Cleanup(restore)

	c := &coEnv{pb: p, notes: notes, rcpt: rcpt}
	c.f = p.w.NewSite(t, p.org)
	seedChart(p.w, t, p.org, c.f.Site)
	mkUser := func(person uuid.UUID) string {
		u, err := e.svc.CreateUser(context.Background(), identity.CreateUserParams{Email: id.New().String() + "@example.org"})
		if err != nil {
			t.Fatal(err)
		}
		p.w.Exec(t, `UPDATE person SET user_id = $2 WHERE id = $1`, person, u.ID)
		return tokenOf(t, e, u.ID)
	}
	owner, tenant, hhNo, hhOk := p.w.NewPerson(t, p.org), p.w.NewPerson(t, p.org), p.w.NewPerson(t, p.org), p.w.NewPerson(t, p.org)
	p.w.NewOccupancy(t, c.f, c.f.Unit, owner, "owner")
	p.w.NewOccupancy(t, c.f, c.f.Unit, tenant, "tenant")
	p.w.NewOccupancy(t, c.f, c.f.Unit, hhNo, "household")
	okOcc := p.w.NewOccupancy(t, c.f, c.f.Unit, hhOk, "household")
	p.w.Exec(t, `UPDATE occupancy SET may_see_money = true WHERE id = $1`, okOcc)
	c.owner, c.tenant, c.hhNo, c.hhOk = mkUser(owner), mkUser(tenant), mkUser(hhNo), mkUser(hhOk)
	c.manager = p.adm
	c.siteMgr = p.w.Token(t, p.w.Actor("site_manager"))

	bill := c2Billing(p.w)
	mk := func(side string, amount int64) uuid.UUID {
		ch, err := bill.CreateCharge(sysCtx(p.org), p.org.ID, billing.ChargeInput{SiteID: c.f.Site, UnitID: c.f.Unit, ChargeType: "special_assessment", DebtorKind: side,
			Amount: money.New(amount, "TRY"), DueDate: "2026-02-01"})
		if err != nil {
			t.Fatal(err)
		}
		return ch.ID
	}
	c.ownerCharge, c.tenantCharge = mk("owner", 50000), mk("tenant", 30000)
	return c
}

func (c *coEnv) enablePack() {
	c.t.Helper()
	c.must(c.call("POST", c.base()+"/packs/example/enable", `{"settings":{"example_api_key":"k_live_http_checkout","example_webhook_secret":"`+coSecret+`"}}`, c.adm), 200)
}

func (c *coEnv) create(token, body string, hdr ...string) resp {
	c.t.Helper()
	h := append([]string{"Idempotency-Key", "co-" + id.New().String()}, hdr...)
	return c.call("POST", c.base()+"/checkouts", body, token, h...)
}

func (c *coEnv) side(side string, extra string) string {
	return fmt.Sprintf(`{"unit_id":"%s","debtor_kind":"%s"%s}`, c.f.Unit, side, extra)
}

func (c *coEnv) txn(checkoutID string) string {
	var txn string
	err := c.e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT provider_txn_id FROM checkout WHERE id = $1`, checkoutID).Scan(&txn)
	})
	if err != nil {
		c.t.Fatal(err)
	}
	return txn
}

func (c *coEnv) webhook(body map[string]any, secret string) *httptest.ResponseRecorder {
	c.t.Helper()
	raw, _ := json.Marshal(body)
	return c.e.do("POST", c.base()+"/packs/example/payments/example/webhook", string(raw), false, example.SignatureHeader, example.Sign(secret, raw))
}

func (c *coEnv) paid(checkoutID string, gross, fee int64) *httptest.ResponseRecorder {
	return c.webhook(map[string]any{"event": "paid", "txn": c.txn(checkoutID), "gross": gross, "fee": fee, "currency": "TRY"}, coSecret)
}

func (c *coEnv) problemCode(r resp, status int, code string) {
	c.t.Helper()
	if r.Code != status || str(r.Body, "code") != code {
		c.t.Fatalf("got %d %s, want %d %s", r.Code, r.Raw, status, code)
	}
}

func (c *coEnv) invariants() {
	c.t.Helper()
	if n := c.qInt(`SELECT count(*) FROM (SELECT entry_id FROM journal_line WHERE org_id = $1 GROUP BY entry_id HAVING sum(debit) <> sum(credit)) x`, c.org.ID); n != 0 {
		c.t.Errorf("%d unbalanced journal entries", n)
	}
	if n := c.qInt(`SELECT count(*) FROM journal_entry e WHERE e.org_id = $1 AND e.reverses_entry_id IS NOT NULL AND
		(SELECT COALESCE(sum(debit), 0) FROM journal_line WHERE org_id = e.org_id AND entry_id = e.id) <>
		(SELECT COALESCE(sum(credit), 0) FROM journal_line WHERE org_id = e.org_id AND entry_id = e.reverses_entry_id)`, c.org.ID); n != 0 {
		c.t.Errorf("%d reversals that do not mirror their entry", n)
	}
}

func TestIntegrationCheckoutCapabilityAndNoProvider(t *testing.T) {
	c := newCoEnv(t)
	sitePath := c.base() + "/sites/" + c.f.Site.String() + "/settings"
	pay := func(r resp) map[string]any { return r.Body["payments"].(map[string]any) }

	// no pack: no card payment, and a stable code for the one who tries
	r := c.must(c.call("GET", sitePath, "", c.manager), 200)
	card := pay(r)["card"].(map[string]any)
	if card["available"] != false || card["provider"] != nil || pay(r)["merchant_of_record"] != "site" || pay(r)["merchant_of_record_override"] != nil {
		t.Fatalf("payments before enabling = %v", pay(r))
	}
	c.problemCode(c.create(c.tenant, c.side("tenant", "")), 409, "payment_provider_unavailable")

	c.enablePack()
	card = pay(c.must(c.call("GET", sitePath, "", c.manager), 200))["card"].(map[string]any)
	if card["available"] != true || card["provider"] != "example" {
		t.Fatalf("card after enabling = %v", card)
	}
	// residents read the site settings too: the mobile app keys "Pay by card" on it
	if r := c.must(c.call("GET", sitePath, "", c.tenant), 200); pay(r)["card"].(map[string]any)["available"] != true {
		t.Errorf("the tenant sees %v", pay(r))
	}
}

func TestIntegrationCheckoutWhoMayPayWhat(t *testing.T) {
	c := newCoEnv(t)
	c.enablePack()

	// the tenant pays the tenant side, not the owner side
	r := c.must(c.create(c.tenant, c.side("tenant", fmt.Sprintf(`,"charge_ids":["%s"]`, c.tenantCharge))), 201)
	if str(r.Body, "status") != "pending" || str(r.Body, "provider") != "example" || !strings.HasPrefix(str(r.Body, "checkout_url"), "https://pay.example.test/checkout/ex_") ||
		str(r.Body, "return_url") != "https://app.example.test/orgs/"+c.org.ID.String()+"/pay/return?checkout_id="+str(r.Body, "id") || str(r.Body, "expires_at") == "" {
		t.Fatalf("checkout = %s", r.Raw)
	}
	if amt := r.Body["amount"].(map[string]any); amt["amount"].(float64) != 30000 || amt["currency"] != "TRY" {
		t.Errorf("amount = %v", amt)
	}
	c.must(c.create(c.tenant, c.side("owner", "")), 403)
	// a charge of the other side is not open here
	c.problemCode(c.create(c.tenant, c.side("tenant", fmt.Sprintf(`,"charge_ids":["%s"]`, c.ownerCharge))), 422, "validation_failed")

	// the owner reaches both sides
	c.must(c.create(c.owner, c.side("owner", "")), 201)
	c.must(c.create(c.owner, c.side("tenant", "")), 201)

	// a household member: only with may_see_money, and only on their own (the owner) side
	c.must(c.create(c.hhNo, c.side("owner", "")), 403)
	c.must(c.create(c.hhOk, c.side("owner", "")), 201)
	c.must(c.create(c.hhOk, c.side("tenant", "")), 403)

	// managers pay for anyone; other organizations and anonymous callers get nothing
	c.must(c.create(c.manager, c.side("owner", "")), 201)
	c.must(c.create(c.siteMgr, c.side("tenant", "")), 201)
	c.must(c.create(c.w.Token(t, c.w.Actor("auditor")), c.side("owner", "")), 403)
	c.must(c.create(c.w.Token(t, c.w.BAdmin), c.side("owner", "")), 404)
	c.must(c.create("", c.side("owner", "")), 401)

	// the same unit id in the body that is not a unit of the organization
	c.must(c.create(c.manager, fmt.Sprintf(`{"unit_id":"%s","debtor_kind":"owner"}`, id.New())), 404)
	// bad input
	c.must(c.create(c.tenant, c.side("landlord", "")), 422)
	c.must(c.create(c.tenant, c.side("tenant", `,"amount":{"amount":-5,"currency":"TRY"}`)), 422)
	c.must(c.create(c.tenant, c.side("tenant", `,"client":"carrier-pigeon"`)), 422)
	// Idempotency-Key is required
	c.must(c.call("POST", c.base()+"/checkouts", c.side("tenant", ""), c.tenant), 422)
}

func TestIntegrationCheckoutIdempotentCreateAndMobileReturnURL(t *testing.T) {
	c := newCoEnv(t)
	c.enablePack()
	body := c.side("tenant", `,"client":"mobile"`)
	r1 := c.must(c.call("POST", c.base()+"/checkouts", body, c.tenant, "Idempotency-Key", "same-key-0001"), 201)
	r2 := c.must(c.call("POST", c.base()+"/checkouts", body, c.tenant, "Idempotency-Key", "same-key-0001"), 201)
	if str(r1.Body, "id") != str(r2.Body, "id") || str(r1.Body, "checkout_url") != str(r2.Body, "checkout_url") {
		t.Errorf("a replay made another checkout: %s vs %s", r1.Raw, r2.Raw)
	}
	if n := c.qInt(`SELECT count(*) FROM checkout WHERE org_id = $1`, c.org.ID); n != 1 {
		t.Errorf("%d checkouts after a replay", n)
	}
	if want := "openbekci://pay/return?org_id=" + c.org.ID.String() + "&checkout_id=" + str(r1.Body, "id"); str(r1.Body, "return_url") != want {
		t.Errorf("mobile return_url = %s", str(r1.Body, "return_url"))
	}
	c.problemCode(c.call("POST", c.base()+"/checkouts", c.side("tenant", `,"amount":{"amount":99,"currency":"TRY"}`), c.tenant, "Idempotency-Key", "same-key-0001"), 422, "idempotency_mismatch")
}

func TestIntegrationCheckoutPollListAndVisibility(t *testing.T) {
	c := newCoEnv(t)
	c.enablePack()
	mine := str(c.must(c.create(c.tenant, c.side("tenant", "")), 201).Body, "id")
	ownerOwn := str(c.must(c.create(c.owner, c.side("owner", "")), 201).Body, "id")

	// the poll: the creator, a manager and a resident who reaches the side see it; nobody else does
	get := func(tok, id string) resp { return c.call("GET", c.base()+"/checkouts/"+id, "", tok) }
	if r := c.must(get(c.tenant, mine), 200); str(r.Body, "status") != "pending" || str(r.Body, "id") != mine {
		t.Fatalf("poll = %s", r.Raw)
	}
	c.must(get(c.owner, mine), 200) // the owner reaches the tenant side
	c.must(get(c.manager, mine), 200)
	c.must(get(c.tenant, ownerOwn), 404) // the tenant does not reach the owner side
	c.must(get(c.hhNo, mine), 404)
	c.must(get(c.w.Token(t, c.w.BAdmin), mine), 404)
	c.must(get(c.tenant, id.New().String()), 404)
	c.must(get("", mine), 401)

	// my checkouts: only the ones I started
	if got := items(c.must(c.call("GET", c.base()+"/me/checkouts", "", c.tenant), 200)); len(got) != 1 || str(got[0], "id") != mine {
		t.Errorf("tenant's checkouts = %v", got)
	}
	c.must(c.call("GET", c.base()+"/me/checkouts", "", c.w.Token(t, c.w.Actor("auditor"))), 403)

	// the manager's list per site, with a status filter; residents and other organizations cannot read it
	list := c.base() + "/checkouts?site_id=" + c.f.Site.String()
	if got := items(c.must(c.call("GET", list, "", c.manager), 200)); len(got) != 2 {
		t.Errorf("site checkouts = %d, want 2", len(got))
	}
	if got := items(c.must(c.call("GET", list+"&status=paid", "", c.manager), 200)); len(got) != 0 {
		t.Errorf("paid checkouts = %d, want 0", len(got))
	}
	c.must(c.call("GET", list, "", c.tenant), 403)
	c.must(c.call("GET", list, "", c.w.Token(t, c.w.BAdmin)), 404)
}

func TestIntegrationCheckoutWebhookCompletesAndRefundIsGuarded(t *testing.T) {
	c := newCoEnv(t)
	c.enablePack()
	_, _ = c.newEndpoint("https://93.184.216.34/pay-hook", `["payment.confirmed","payment.recorded"]`)

	co := str(c.must(c.create(c.tenant, c.side("tenant", fmt.Sprintf(`,"charge_ids":["%s"]`, c.tenantCharge))), 201).Body, "id")

	// a delivery with a wrong signature records nothing
	if rec := c.webhook(map[string]any{"event": "paid", "txn": c.txn(co), "gross": 30000, "fee": 900, "currency": "TRY"}, "wrong-secret"); rec.Code != 401 {
		t.Fatalf("tampered webhook: %d", rec.Code)
	}
	if n := c.qInt(`SELECT count(*) FROM payment WHERE org_id = $1`, c.org.ID); n != 0 {
		t.Fatal("a tampered webhook recorded a payment")
	}
	if rec := c.paid(co, 30000, 900); rec.Code != 200 {
		t.Fatalf("webhook: %d %s", rec.Code, rec.Body)
	}
	r := c.must(c.call("GET", c.base()+"/checkouts/"+co, "", c.tenant), 200)
	pid := str(r.Body, "payment_id")
	if str(r.Body, "status") != "paid" || pid == "" {
		t.Fatalf("after the webhook: %s", r.Raw)
	}
	pay := c.must(c.call("GET", c.base()+"/payments/"+pid, "", c.manager), 200)
	if str(pay.Body, "method") != "card:example" || str(pay.Body, "status") != "confirmed" || str(pay.Body, "receipt_no") == "" || num(pay.Body, "version") != 1 || str(pay.Body, "provider_txn_id") == "" {
		t.Fatalf("payment = %s", pay.Raw)
	}
	if g, f, n := pay.Body["gross"].(map[string]any)["amount"], pay.Body["fee"].(map[string]any)["amount"], pay.Body["net"].(map[string]any)["amount"]; g != 30000.0 || f != 900.0 || n != 29100.0 {
		t.Errorf("gross/fee/net = %v/%v/%v", g, f, n)
	}
	c.must(c.call("GET", c.base()+"/payments/"+pid, "", c.tenant), 200) // the payer sees the payment of their own side
	if c.notes.count("payment.confirmed") != 1 || len(c.rcpt.ids) != 1 {
		t.Errorf("notifications %d, receipts %d", c.notes.count("payment.confirmed"), len(c.rcpt.ids))
	}
	// the webhook events: payment.confirmed (and payment.recorded) queued once for the endpoint
	events := func() map[string]int {
		out := map[string]int{}
		err := c.e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
			rows, err := tx.Query(context.Background(), `SELECT event_type, count(*) FROM webhook_delivery WHERE org_id = $1 GROUP BY 1`, c.org.ID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var typ string
				var n int
				if err := rows.Scan(&typ, &n); err != nil {
					return err
				}
				out[typ] = n
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if ev := events(); ev["payment.confirmed"] != 1 || ev["payment.recorded"] != 1 {
		t.Fatalf("webhook events = %v", ev)
	}
	// a replay is acknowledged and changes nothing
	if rec := c.paid(co, 30000, 900); rec.Code != 200 {
		t.Fatalf("replay: %d", rec.Code)
	}
	if ev := events(); ev["payment.confirmed"] != 1 || c.notes.count("payment.confirmed") != 1 || len(c.rcpt.ids) != 1 {
		t.Errorf("a replay emitted again: %v, %d, %d", ev, c.notes.count("payment.confirmed"), len(c.rcpt.ids))
	}

	// refund: a resident cannot, a manager needs the current version
	refund := c.base() + "/payments/" + pid + ":refund"
	idem := func() []string { return []string{"Idempotency-Key", "rf-" + id.New().String()} }
	c.must(c.call("POST", refund, `{"reason":"asked"}`, c.tenant, append(idem(), "If-Match", `"1"`)...), 403)
	c.must(c.call("POST", refund, `{"reason":"asked"}`, c.manager, idem()...), 422) // If-Match is required
	c.problemCode(c.call("POST", refund, `{"reason":"asked"}`, c.manager, append(idem(), "If-Match", `"7"`)...), 409, "version_conflict")
	key := "rf-final-0001"
	done := c.must(c.call("POST", refund, `{"reason":"asked"}`, c.manager, "Idempotency-Key", key, "If-Match", `"1"`), 200)
	if str(done.Body, "status") != "refunded" || num(done.Body, "version") != 2 {
		t.Fatalf("refunded payment = %s", done.Raw)
	}
	if got := c.must(c.call("GET", c.base()+"/checkouts/"+co, "", c.tenant), 200); str(got.Body, "status") != "refunded" {
		t.Errorf("checkout after the refund = %s", got.Raw)
	}
	again := c.must(c.call("POST", refund, `{"reason":"asked"}`, c.manager, "Idempotency-Key", key, "If-Match", `"1"`), 200)
	if str(again.Body, "status") != "refunded" {
		t.Errorf("replay = %s", again.Raw)
	}
	// without the key the second refund finds the payment already refunded and posts nothing more
	n := c.qInt(`SELECT count(*) FROM journal_entry WHERE org_id = $1`, c.org.ID)
	c.must(c.call("POST", refund, `{}`, c.manager, append(idem(), "If-Match", `"1"`)...), 200)
	if c.qInt(`SELECT count(*) FROM journal_entry WHERE org_id = $1`, c.org.ID) != n {
		t.Error("a second refund posted another reversal")
	}
	// a cash payment is no provider payment
	cash := c.must(c.call("POST", c.base()+"/payments", fmt.Sprintf(`{"site_id":"%s","method":"bank_transfer","amount":{"amount":100,"currency":"TRY"},"received_on":"2026-01-10","asset_account_id":"%s","debtor":{"mode":"unit_side","unit_id":"%s","debtor_kind":"owner"}}`,
		c.f.Site, c.bankAccount(), c.f.Unit), c.manager, idem()...), 201)
	c.problemCode(c.call("POST", c.base()+"/payments/"+str(cash.Body, "id")+":refund", `{}`, c.manager, append(idem(), "If-Match", `"1"`)...), 409, "conflict")
	c.invariants()
}

func (c *coEnv) bankAccount() uuid.UUID {
	var acc uuid.UUID
	err := c.e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT id FROM account WHERE org_id = $1 AND site_id = $2 AND subtype = 'bank'`, c.org.ID, c.f.Site).Scan(&acc)
	})
	if err != nil {
		c.t.Fatal(err)
	}
	return acc
}

func TestIntegrationMerchantOfRecordSettingAndSettlementEndpoints(t *testing.T) {
	c := newCoEnv(t)
	c.enablePack()
	orgPath := c.base()
	sitePath := orgPath + "/sites/" + c.f.Site.String() + "/settings"
	pay := func() map[string]any {
		return c.must(c.call("GET", sitePath, "", c.manager), 200).Body["payments"].(map[string]any)
	}
	patchOrg := func(settings string) resp {
		return c.call("PATCH", orgPath, `{"settings":`+settings+`}`, c.manager, "If-Match", fmt.Sprintf(`"%d"`, c.w.OrgVersion(t, c.w.OrgA)))
	}
	siteVersion := func() string {
		return fmt.Sprintf(`"%d"`, num(c.must(c.call("GET", sitePath, "", c.manager), 200).Body, "version"))
	}

	// the default: the site collects into its own provider account
	if p := pay(); p["merchant_of_record"] != "site" || p["merchant_of_record_override"] != nil {
		t.Fatalf("default = %v", p)
	}
	// the organization may switch to central collection (org.manage), with a value check
	c.must(patchOrg(`{"payments.merchant_of_record":"everyone"}`), 422)
	c.must(patchOrg(`{"payments.merchant_of_record":"organization"}`), 200)
	if p := pay(); p["merchant_of_record"] != "organization" || p["merchant_of_record_override"] != nil {
		t.Fatalf("after the switch = %v", p)
	}
	co := c.must(c.create(c.tenant, c.side("tenant", "")), 201)
	if str(co.Body, "merchant_of_record") != "organization" {
		t.Errorf("checkout merchant = %s", co.Raw)
	}
	// a site manager cannot override who collects the money; an org admin can, and null removes the override
	c.must(c.call("PATCH", sitePath, `{"merchant_of_record":"site"}`, c.siteMgr, "If-Match", siteVersion()), 403)
	c.must(c.call("PATCH", sitePath, `{"merchant_of_record":"nobody"}`, c.manager, "If-Match", siteVersion()), 422)
	c.must(c.call("PATCH", sitePath, `{"merchant_of_record":"site"}`, c.manager, "If-Match", siteVersion()), 200)
	if p := pay(); p["merchant_of_record"] != "site" || p["merchant_of_record_override"] != "site" {
		t.Fatalf("after the override = %v", p)
	}
	c.must(c.call("PATCH", sitePath, `{"merchant_of_record":null}`, c.manager, "If-Match", siteVersion()), 200)
	if p := pay(); p["merchant_of_record"] != "organization" || p["merchant_of_record_override"] != nil {
		t.Fatalf("after removing the override = %v", p)
	}
	// a site manager still edits the other site settings
	c.must(c.call("PATCH", sitePath, `{"directory_enabled":true}`, c.siteMgr, "If-Match", siteVersion()), 200)

	// settlement and clearing endpoints: managers read them, residents do not
	if rec := c.paid(str(co.Body, "id"), 20000, 600); rec.Code != 200 {
		t.Fatalf("webhook: %d", rec.Code)
	}
	clr := c.must(c.call("GET", orgPath+"/payment-clearing?site_id="+c.f.Site.String(), "", c.siteMgr), 200)
	provs := clr.Body["providers"].([]any)
	if str(clr.Body, "merchant_of_record") != "organization" || len(provs) != 1 || provs[0].(map[string]any)["provider"] != "example" ||
		provs[0].(map[string]any)["balance"].(map[string]any)["amount"] != 19400.0 || clr.Body["client_money_owed"].(map[string]any)["amount"] != 0.0 {
		t.Fatalf("clearing = %s", clr.Raw)
	}
	if n, err := c.e.deps.Packs.SyncSettlements(context.Background(), c.org.ID, "example", "example", sdk.ID(c.f.Site.String()),
		sdk.SettlementRequest{From: time.Now().UTC().Format("2006-01-02"), To: time.Now().UTC().Format("2006-01-02"), MerchantOfRecord: sdk.MerchantOrganization}); err != nil || n != 1 {
		t.Fatalf("SyncSettlements = %d, %v", n, err)
	}
	clr = c.must(c.call("GET", orgPath+"/payment-clearing?site_id="+c.f.Site.String(), "", c.siteMgr), 200)
	if clr.Body["client_money_owed"].(map[string]any)["amount"] != 19400.0 || clr.Body["providers"].([]any)[0].(map[string]any)["balance"].(map[string]any)["amount"] != 0.0 {
		t.Fatalf("clearing after the payout = %s", clr.Raw)
	}
	set := items(c.must(c.call("GET", orgPath+"/payment-settlements?site_id="+c.f.Site.String(), "", c.siteMgr), 200))
	if len(set) != 1 || str(set[0], "merchant_of_record") != "organization" || set[0]["net"].(map[string]any)["amount"] != 19400.0 || str(set[0], "provider") != "example" {
		t.Fatalf("settlements = %v", set)
	}
	page := c.must(c.call("GET", orgPath+"/payment-settlements?site_id="+c.f.Site.String()+"&limit=1&provider=example", "", c.manager), 200)
	if page.Body["next_cursor"] != nil || len(items(page)) != 1 {
		t.Errorf("page = %s", page.Raw)
	}
	c.must(c.call("GET", orgPath+"/payment-settlements?site_id="+c.f.Site.String()+"&cursor=!!", "", c.manager), 422)
	c.must(c.call("GET", orgPath+"/payment-settlements?site_id="+c.f.Site.String(), "", c.tenant), 403)
	c.must(c.call("GET", orgPath+"/payment-clearing?site_id="+c.f.Site.String(), "", c.owner), 403)
	c.invariants()
}
