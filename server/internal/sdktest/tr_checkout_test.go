package sdktest_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/packs/tr"
	"github.com/openbekci/openbekci/packs/tr/trtest"
	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/checkout"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/sdktest"
)

// The Turkish pack's iyzico provider through the core checkout, end to end against the in-process fake:
// create the checkout through the core, the resident's browser callback, the payment recorded with gross, fee and
// net, a replayed webhook, the settlement sync, in both merchant-of-record modes (site-owned keys and the
// organization's keys).

const (
	siteKey, siteSecret = "site-account-key", "site-account-secret"
	orgKey, orgSecret   = "org-account-key", "org-account-secret"
)

func iyzicoDoc(key, secret string) map[string]any {
	return map[string]any{"tr_payments_iyzico_enabled": true, "tr_payments_iyzico_api_key": key, "tr_payments_iyzico_secret_key": secret, "tr_payments_iyzico_sandbox": true}
}

// newTRCheckoutEnv boots the tr pack with a fake iyzico that accepts the keys of the account the mode must use,
// and wrong keys at the other scope, so a call on the wrong account is a 401.
func newTRCheckoutEnv(t *testing.T, mode string) (*cenv, *trtest.Iyzico) {
	t.Helper()
	right, wrong := [2]string{siteKey, siteSecret}, [2]string{orgKey, orgSecret} // the other scope holds the wrong keys
	if mode == sdk.MerchantOrganization {
		right = wrong
	}
	fake := trtest.NewIyzico(right[0], right[1])
	t.Cleanup(fake.Close)
	h := sdktest.New(t, tr.New(tr.WithHTTPClient(fake.Client())), sdktest.WithoutEnable())
	e := &cenv{Harness: h, note: &notes{}, rcpt: &receipts{}}
	h.Payments.Notifier = e.note
	e.chk = checkout.NewService(checkout.Config{DB: h.Env.DB, Platform: h.Env.DB, Payments: h.Payments, Providers: h.Host, Receipts: e.rcpt, BaseURL: "https://app.example.test"})
	h.Host.AttachCheckouts(e.chk)
	e.unit = uuid.MustParse(string(h.Unit("A-1")))
	orgDoc, siteDoc := iyzicoDoc(orgKey, orgSecret), iyzicoDoc(siteKey, siteSecret)
	if mode == sdk.MerchantOrganization {
		h.Exec(`UPDATE organization SET settings = settings || '{"payments.merchant_of_record":"organization"}'::jsonb WHERE id = $1`, h.Env.OrgID)
	}
	h.Enable(orgDoc)
	if _, _, err := h.Host.UpdateSettings(h.Ctx(), h.Env.OrgID, "tr", sdk.SiteScope(h.Site), siteDoc); err != nil {
		t.Fatalf("site settings: %v", err)
	}
	return e, fake
}

// callback is what iyzico's hosted page makes the resident's browser post to the callbackUrl of a checkout.
func (e *cenv) callback(fake *trtest.Iyzico, c checkout.Checkout) *http.Response {
	e.T.Helper()
	token := e.txnOf(c)
	tx, _ := fake.Txn(token)
	var cb string
	for _, in := range fake.InitRequests {
		if in["conversationId"] == tx.ConversationID {
			cb, _ = in["callbackUrl"].(string)
		}
	}
	u, err := url.Parse(cb)
	if err != nil || cb == "" {
		e.T.Fatalf("no callback url for %s: %q", token, cb)
	}
	rec := e.CallRoute("POST", "/payments/tr.iyzico/webhook?"+u.RawQuery, []byte("token="+url.QueryEscape(token)), "Content-Type", "application/x-www-form-urlencoded")
	return rec.Result()
}

func (e *cenv) payments(token string) (n, gross, fee, net int64) {
	row := []any{&n, &gross, &fee, &net}
	e.Env.Query(row, `SELECT count(*), COALESCE(sum(gross),0), COALESCE(sum(fee),0), COALESCE(sum(net),0) FROM payment WHERE org_id = $1 AND provider = 'tr.iyzico' AND provider_txn_id = $2`, e.Env.OrgID, token)
	return
}

func TestTRCheckoutEndToEndInBothMerchantModes(t *testing.T) {
	for _, mode := range []string{sdk.MerchantSite, sdk.MerchantOrganization} {
		t.Run(mode, func(t *testing.T) {
			e, fake := newTRCheckoutEnv(t, mode)
			owner := e.charge("owner", 50000, "dues")
			c := e.create("owner", owner)
			token := e.txnOf(c)
			if c.Provider != "tr.iyzico" || c.MerchantOfRecord != mode || c.Status != checkout.StatusPending || c.CheckoutURL == nil || !strings.Contains(*c.CheckoutURL, token) {
				t.Fatalf("checkout = %+v", c)
			}
			if got := e.get(c); got.Status != checkout.StatusPending {
				t.Fatalf("a fresh checkout is %s", got.Status)
			}
			// the resident pays on iyzico's page: 500.00 gross, 15.00 commission
			fake.Pay(token, today(), "0.50", "14.50")
			resp := e.callback(fake, c)
			if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "https://app.example.test/orgs/"+e.Env.OrgID.String()+"/pay/return?checkout_id="+c.ID.String()) ||
				!strings.Contains(resp.Header.Get("Location"), "status=paid") {
				t.Fatalf("callback answered %d, Location %q", resp.StatusCode, resp.Header.Get("Location"))
			}
			if n, gross, fee, net := e.payments(token); n != 1 || gross != 50000 || fee != 1500 || net != 48500 {
				t.Fatalf("payments: %d with gross %d fee %d net %d, want 1 with 50000/1500/48500", n, gross, fee, net)
			}
			if got := e.get(c); got.Status != checkout.StatusPaid {
				t.Fatalf("checkout after the callback is %s", got.Status)
			}
			if d, cr := e.Balance("1200"); d != cr {
				t.Fatalf("the owner's receivable is not settled: %d/%d", d, cr)
			}
			if clearing := e.bal(e.Env.SiteID, "1190.tr.iyzico"); clearing != 48500 {
				t.Fatalf("clearing holds %d, want the net 48500", clearing)
			}
			// the signed server notification for the same payment, and the callback again, change nothing
			if code := e.Webhook("tr.iyzico", fake.Webhook(token).Body, fake.Webhook(token).Header).Code; code != http.StatusOK {
				t.Fatalf("webhook replay: %d", code)
			}
			e.callback(fake, c)
			if n, _, _, _ := e.payments(token); n != 1 {
				t.Fatalf("%d payments after replays", n)
			}
			// the status poll reads the same account (Status gets nothing but the transaction id)
			if _, err := e.Host.PaymentStatus(e.Ctx(), e.Env.OrgID, "tr", "tr.iyzico", token); err != nil {
				t.Fatalf("status: %v", err)
			}
			// settlement sync: the net moves from clearing to the bank, once
			posted, failed, err := e.chk.SyncSettlements(e.Ctx(), today(), today())
			if err != nil || failed != 0 || posted != 1 {
				t.Fatalf("SyncSettlements = %d posted, %d failed, %v", posted, failed, err)
			}
			if mode == sdk.MerchantSite {
				if bank := e.bal(e.Env.SiteID, "1100"); bank != 48500 {
					t.Fatalf("bank holds %d after the payout, want 48500", bank)
				}
			} else if cm := e.bal(e.Env.SiteID, ledger.ClientMoneyCode); cm != 48500 {
				t.Fatalf("client money holds %d after the payout, want 48500 (the organization's account holds it)", cm)
			}
			if clearing := e.bal(e.Env.SiteID, "1190.tr.iyzico"); clearing != 0 {
				t.Fatalf("clearing holds %d after the payout, want 0", clearing)
			}
			if posted, _, _ := e.chk.SyncSettlements(e.Ctx(), today(), today()); posted != 0 {
				t.Fatalf("a second sync posted %d", posted)
			}
			// a refund reaches the same account and reverses the posting
			if _, err := e.Host.RefundPayment(e.Ctx(), e.Env.OrgID, "tr", "tr.iyzico", sdk.RefundRequest{ProviderTxnID: token, RefundRef: "r-1", Amount: sdk.Money{Amount: 50000, Currency: "TRY"}}); err != nil {
				t.Fatalf("refund: %v", err)
			}
			if tx, _ := fake.Txn(token); !tx.Refunded {
				t.Fatal("iyzico was not asked to refund")
			}
			e.AssertLedgerInvariants()
		})
	}
}

// Organization mode with two sites: the provider reports no per-payment ids (the daily report carries iyzico's
// payment id, the core keys payments by token), so the organization payout is posted to the reporting site whole.
// The books still balance, a replay posts nothing, and the sites' clearing accounts net out across the organization.
func TestTROrganizationPayoutWithoutTxnIDsFallsBackToTheReportingSite(t *testing.T) {
	e, fake := newTRCheckoutEnv(t, sdk.MerchantOrganization)
	site2, unit2 := e.secondSite()
	e.charge("owner", 40000, "site 1 dues")
	c1 := e.create("owner")
	amt := money.New(25000, "TRY")
	c2, err := e.chk.Create(e.Ctx(), e.Env.OrgID, checkout.CreateInput{UnitID: unit2, DebtorKind: "owner", Amount: &amt})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		c   checkout.Checkout
		fee [2]string
	}{{c1, [2]string{"0.40", "11.60"}}, {c2, [2]string{"0.25", "7.25"}}} {
		c, fee := p.c, p.fee
		fake.Pay(e.txnOf(c), today(), fee[0], fee[1])
		if resp := e.callback(fake, c); resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("callback: %d", resp.StatusCode)
		}
	}
	// 400.00 - 12.00 = 388.00 and 250.00 - 7.50 = 242.50
	if a, b := e.bal(e.Env.SiteID, "1190.tr.iyzico"), e.bal(site2, "1190.tr.iyzico"); a != 38800 || b != 24250 {
		t.Fatalf("clearing = %d and %d, want 38800 and 24250", a, b)
	}
	posted, failed, err := e.chk.SyncSettlements(e.Ctx(), today(), today())
	if err != nil || failed != 0 || posted != 1 {
		t.Fatalf("SyncSettlements = %d posted, %d failed, %v (one payout for the organization's account)", posted, failed, err)
	}
	// the money of an organization payout is client money (the organization's account holds it), not a site's bank
	b1, b2 := e.bal(e.Env.SiteID, ledger.ClientMoneyCode), e.bal(site2, ledger.ClientMoneyCode)
	if b1+b2 != 38800+24250 || (b1 != 0 && b2 != 0) {
		t.Fatalf("client money = %d and %d: the whole payout (63050) goes to the one reporting site", b1, b2)
	}
	if e.bal(e.Env.SiteID, "1100")+e.bal(site2, "1100") != 0 {
		t.Fatal("the organization's payout reached a site bank account")
	}
	if c1c, c2c := e.bal(e.Env.SiteID, "1190.tr.iyzico"), e.bal(site2, "1190.tr.iyzico"); c1c+c2c != 0 {
		t.Fatalf("clearing across the sites = %d, want 0", c1c+c2c)
	}
	if posted, _, _ := e.chk.SyncSettlements(e.Ctx(), today(), today()); posted != 0 {
		t.Fatalf("a replay posted %d", posted)
	}
	e.AssertLedgerInvariants()
}

func TestTRBankDetailsSuggestTheReferenceForEnabledPacks(t *testing.T) {
	h := newTR(t, sdktest.WithoutEnable())
	unit := h.Unit("A-12")
	org, site := h.Env.OrgID, h.Env.SiteID
	ref := func() *string {
		t.Helper()
		got, err := h.Tenancy.WithBankReferenceHint(h.Host).SuggestedReference(h.Ctx(), org, site, uuid.MustParse(string(unit)))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := ref(); got != nil {
		t.Fatalf("a pack that is not enabled suggested %q", *got)
	}
	h.Enable(nil)
	got := ref()
	if got == nil || !strings.HasPrefix(*got, "A-12-20") || len(*got) != len("A-12-2026-03") {
		t.Fatalf("suggested reference = %v, want <door>-<YYYY-MM>", got)
	}
}
