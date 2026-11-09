// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbekci/openbekci/packs/tr/trtest"
	"github.com/openbekci/openbekci/sdk"
)

// stubSettings serves pack settings: keys "org" and "site:<id>".
type stubSettings map[string]map[string]any

func (s stubSettings) All(_ context.Context, sc sdk.Scope) (map[string]any, error) {
	key := "org"
	if sc.Kind == sdk.ScopeSite {
		key = "site:" + string(sc.SiteID)
	}
	out := map[string]any{}
	for k, v := range s[key] {
		out[k] = v
	}
	return out, nil
}

func (s stubSettings) Get(ctx context.Context, sc sdk.Scope, k string) (any, bool, error) {
	all, _ := s.All(ctx, sc)
	v, ok := all[k]
	return v, ok, nil
}

type stubServices struct {
	sdk.Services
	set sdk.Settings
}

func (s stubServices) Settings() sdk.Settings { return s.set }

const (
	tKey    = "sandbox-key-123"
	tSecret = "sandbox-secret-456"
	tOrg    = sdk.ID("11111111-1111-1111-1111-111111111111")
	tSite   = sdk.ID("22222222-2222-2222-2222-222222222222")
	tUnit   = sdk.ID("33333333-3333-3333-3333-333333333333")
)

func orgIyzico(extra map[string]any) map[string]any {
	m := map[string]any{"tr_payments_iyzico_enabled": true, "tr_payments_iyzico_api_key": tKey, "tr_payments_iyzico_secret_key": tSecret,
		"tr_payments_iyzico_sandbox": true}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func newIyzico(t *testing.T, doc map[string]any) (*iyzico, *trtest.Iyzico, context.Context) {
	t.Helper()
	fake := trtest.NewIyzico(tKey, tSecret)
	t.Cleanup(fake.Close)
	p := New(WithHTTPClient(fake.Client()))
	p.svc = stubServices{set: stubSettings{"org": doc}}
	p.accounts = memAccounts{}
	return &iyzico{p: p}, fake, sdk.WithOrg(context.Background(), tOrg)
}

func checkoutReq() sdk.CheckoutRequest {
	return sdk.CheckoutRequest{Reference: "chk-1", Gross: sdk.Money{Amount: 10000, Currency: "TRY"}, Locale: "tr",
		Target:    sdk.PaymentTarget{SiteID: tSite, UnitID: tUnit, DebtorKind: "tenant", ChargeIDs: []sdk.ID{"44444444-4444-4444-4444-444444444444"}},
		ReturnURL: "https://app.example.test/pay/done", Payer: sdk.Payer{Name: "Ayşe Yılmaz"}, MerchantOfRecord: sdk.MerchantOrganization}
}

func TestCreateCheckoutSendsTheDocumentedRequest(t *testing.T) {
	pr, fake, ctx := newIyzico(t, orgIyzico(nil))
	co, err := pr.CreateCheckout(ctx, checkoutReq())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(co.ProviderTxnID, "tok-") || !strings.Contains(co.CheckoutURL, co.ProviderTxnID) {
		t.Fatalf("checkout %+v", co)
	}
	in := fake.InitRequests[0]
	if in["conversationId"] != "chk-1" || in["price"] != "100.00" || in["paidPrice"] != "100.00" || in["currency"] != "TRY" {
		t.Errorf("request %v", in)
	}
	basket, _ := in["basketId"].(string)
	target, err := decodeBasket(basket)
	if err != nil || target.DebtorKind != "tenant" || target.UnitID != tUnit || len(target.ChargeIDs) != 1 {
		t.Errorf("basket %q decodes to %+v %v", basket, target, err)
	}
	cb, _ := in["callbackUrl"].(string)
	if !strings.HasPrefix(cb, "https://app.example.test/v1/orgs/"+string(tOrg)+"/packs/tr/payments/tr.iyzico/webhook?") || !strings.Contains(cb, "site="+string(tSite)) {
		t.Errorf("callback url %q", cb)
	}
	buyer, _ := in["buyer"].(map[string]any)
	if buyer["identityNumber"] != placeholderIdentity || buyer["registrationAddress"] != placeholderAddress || buyer["name"] != "Ayşe" || buyer["surname"] != "Yılmaz" {
		t.Errorf("buyer %v: national id and address must be placeholders, the name is the payer's", buyer)
	}
	if buyer["email"] != placeholderEmail {
		t.Errorf("email %v", buyer["email"])
	}
	if inst, _ := in["enabledInstallments"].([]any); len(inst) != 1 || inst[0] != float64(1) {
		t.Errorf("installments %v: a dues payment is a single payment", in["enabledInstallments"])
	}
}

func TestCreateCheckoutUsesTheConfiguredCallbackBase(t *testing.T) {
	pr, fake, ctx := newIyzico(t, orgIyzico(map[string]any{"tr_payments_iyzico_callback_base_url": "https://api.example.test/"}))
	if _, err := pr.CreateCheckout(ctx, checkoutReq()); err != nil {
		t.Fatal(err)
	}
	if cb, _ := fake.InitRequests[0]["callbackUrl"].(string); !strings.HasPrefix(cb, "https://api.example.test/v1/orgs/") {
		t.Fatalf("callback url %q", cb)
	}
}

func TestCreateCheckoutRefusals(t *testing.T) {
	pr, _, ctx := newIyzico(t, orgIyzico(nil))
	cases := map[string]struct {
		mut  func(*sdk.CheckoutRequest)
		want error
	}{
		"eur":       {func(r *sdk.CheckoutRequest) { r.Gross.Currency = "EUR" }, sdk.ErrRejected},
		"zero":      {func(r *sdk.CheckoutRequest) { r.Gross.Amount = 0 }, sdk.ErrRejected},
		"no ref":    {func(r *sdk.CheckoutRequest) { r.Reference = "" }, sdk.ErrRejected},
		"no origin": {func(r *sdk.CheckoutRequest) { r.ReturnURL = "" }, sdk.ErrRejected},
		"kind":      {func(r *sdk.CheckoutRequest) { r.Target.DebtorKind = "guest" }, sdk.ErrRejected},
		"too many charges": {func(r *sdk.CheckoutRequest) {
			for i := 0; i < 10; i++ {
				r.Target.ChargeIDs = append(r.Target.ChargeIDs, "44444444-4444-4444-4444-444444444444")
			}
		}, sdk.ErrRejected},
	}
	for name, c := range cases {
		req := checkoutReq()
		c.mut(&req)
		if _, err := pr.CreateCheckout(ctx, req); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

func TestCredentialsAreRequiredAndNeverLeak(t *testing.T) {
	// not configured
	pr, _, ctx := newIyzico(t, map[string]any{})
	if _, err := pr.CreateCheckout(ctx, checkoutReq()); !errors.Is(err, sdk.ErrCredentials) {
		t.Fatalf("no credentials: %v", err)
	}
	// configured but disabled
	pr, _, ctx = newIyzico(t, orgIyzico(map[string]any{"tr_payments_iyzico_enabled": false}))
	if _, err := pr.CreateCheckout(ctx, checkoutReq()); !errors.Is(err, sdk.ErrUnsupported) {
		t.Fatalf("disabled: %v", err)
	}
	// wrong secret: iyzico answers 401, which maps to ErrCredentials, and no error carries a key
	pr, _, ctx = newIyzico(t, orgIyzico(map[string]any{"tr_payments_iyzico_secret_key": "the-wrong-secret"}))
	_, err := pr.CreateCheckout(ctx, checkoutReq())
	if !errors.Is(err, sdk.ErrCredentials) {
		t.Fatalf("wrong secret: %v", err)
	}
	for _, secret := range []string{"the-wrong-secret", tKey} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("the error %q leaks a credential", err)
		}
	}
}

func payNow(fake *trtest.Iyzico, token string) {
	fake.Pay(token, "2026-02-03", "0.25", "2.25")
}

func TestWebhookPaidMapsGrossFeeAndNet(t *testing.T) {
	pr, fake, ctx := newIyzico(t, orgIyzico(nil))
	co, err := pr.CreateCheckout(ctx, checkoutReq())
	if err != nil {
		t.Fatal(err)
	}
	payNow(fake, co.ProviderTxnID)
	req := fake.Webhook(co.ProviderTxnID)
	req.ReceivedAt = time.Date(2026, 2, 3, 10, 0, 0, 0, time.UTC)
	res, err := pr.HandleWebhook(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 1 || res.Response.Status != 200 {
		t.Fatalf("result %+v", res)
	}
	ev := res.Events[0]
	if ev.Kind != sdk.EventKindPaid || ev.ProviderTxnID != co.ProviderTxnID || ev.Gross.Amount != 10000 || ev.Fee.Amount != 250 || ev.Net.Amount != 9750 || ev.Reference != "chk-1" {
		t.Errorf("event %+v", ev)
	}
	if ev.Target.UnitID != tUnit || ev.Target.DebtorKind != "tenant" || len(ev.Target.ChargeIDs) != 1 {
		t.Errorf("target %+v", ev.Target)
	}
}

func TestWebhookSignatureIsVerified(t *testing.T) {
	pr, fake, ctx := newIyzico(t, orgIyzico(nil))
	co, _ := pr.CreateCheckout(ctx, checkoutReq())
	payNow(fake, co.ProviderTxnID)
	good := fake.Webhook(co.ProviderTxnID)
	tampers := map[string]func(*sdk.WebhookRequest){
		"wrong signature": func(r *sdk.WebhookRequest) { r.Header.Set(SignatureHeader, strings.Repeat("0", 64)) },
		"no signature":    func(r *sdk.WebhookRequest) { r.Header.Del(SignatureHeader) },
		"body changed":    func(r *sdk.WebhookRequest) { r.Body = []byte(strings.Replace(string(r.Body), "SUCCESS", "FAILURE", 1)) },
	}
	for name, f := range tampers {
		r := good
		r.Header = good.Header.Clone()
		f(&r)
		if _, err := pr.HandleWebhook(ctx, r); !errors.Is(err, sdk.ErrInvalidSignature) {
			t.Errorf("%s: %v, want ErrInvalidSignature", name, err)
		}
	}
	// the signature of a notification for one token must not carry another
	other := fake.Seed(trtest.IyzicoTxn{Token: "tok-other", PaymentID: "1", ConversationID: "x", BasketID: "tr1.a.b.o", Price: "1.00", Status: "SUCCESS"})
	forged := fake.Webhook(other.Token)
	forged.Body = good.Body
	if _, err := pr.HandleWebhook(ctx, forged); !errors.Is(err, sdk.ErrInvalidSignature) {
		t.Errorf("a signature moved to another body verified: %v", err)
	}
	// no credentials at all: nothing can be verified
	p2 := New()
	p2.svc = stubServices{set: stubSettings{"org": {}}}
	p2.accounts = memAccounts{}
	if _, err := (&iyzico{p: p2}).HandleWebhook(ctx, good); !errors.Is(err, sdk.ErrInvalidSignature) {
		t.Errorf("without credentials: %v", err)
	}
}

func TestWebhookFailedPendingAndForeignBasket(t *testing.T) {
	pr, fake, ctx := newIyzico(t, orgIyzico(nil))
	co, _ := pr.CreateCheckout(ctx, checkoutReq())
	// the form was opened and nothing happened: no events
	res, err := pr.HandleWebhook(ctx, fake.CallbackFor("https://x.test/cb", co.ProviderTxnID))
	if err != nil || len(res.Events) != 0 {
		t.Fatalf("pending: %+v %v", res, err)
	}
	pr.p.accounts.(memAccounts)["tok-fail"] = account{Site: tSite, Merchant: sdk.MerchantOrganization, Basket: "tr1." + string(tSite) + "." + string(tUnit) + ".o"}
	fake.Seed(trtest.IyzicoTxn{Token: "tok-fail", ConversationID: "c", BasketID: "tr1.a.b.o", Price: "1.00", Status: "FAILURE"})
	res, err = pr.HandleWebhook(ctx, fake.Webhook("tok-fail"))
	if err != nil || len(res.Events) != 1 || res.Events[0].Kind != sdk.EventKindFailed {
		t.Fatalf("failed: %+v %v", res, err)
	}
	// the stored checkout names unit tUnit; iyzico echoing another basket (another unit, another site) is refused
	pr.p.accounts.(memAccounts)["tok-foreign"] = account{Site: tSite, Merchant: sdk.MerchantOrganization, Basket: "tr1." + string(tSite) + "." + string(tUnit) + ".o"}
	fake.Seed(trtest.IyzicoTxn{Token: "tok-foreign", PaymentID: "5", ConversationID: "c", BasketID: "tr1." + string(tSite) + ".99999999-9999-9999-9999-999999999999.o", Price: "1.00", Status: "SUCCESS", IyziFee: "0", IyziRate: "0"})
	if _, err = pr.HandleWebhook(ctx, fake.Webhook("tok-foreign")); !errors.Is(err, sdk.ErrRejected) {
		t.Fatalf("a payment of another basket must not be recorded: %v", err)
	}
	if _, err = pr.HandleWebhook(ctx, fake.CallbackFor("https://x.test/cb", "tok-unknown")); !errors.Is(err, sdk.ErrInvalidSignature) {
		t.Fatalf("unknown token: %v", err)
	}
	if _, err = pr.HandleWebhook(ctx, sdk.WebhookRequest{Body: []byte("garbage"), Header: http.Header{}}); !errors.Is(err, sdk.ErrRejected) {
		t.Fatalf("garbage: %v", err)
	}
}

func TestCallbackRedirectsOnlyToTheSignedReturnURL(t *testing.T) {
	pr, fake, ctx := newIyzico(t, orgIyzico(nil))
	co, _ := pr.CreateCheckout(ctx, checkoutReq())
	payNow(fake, co.ProviderTxnID)
	cb, _ := fake.InitRequests[0]["callbackUrl"].(string)
	res, err := pr.HandleWebhook(ctx, fake.CallbackFor(cb, co.ProviderTxnID))
	if err != nil {
		t.Fatal(err)
	}
	if res.Response.Status != http.StatusSeeOther || !strings.HasPrefix(res.Response.Header.Get("Location"), "https://app.example.test/pay/done?") ||
		!strings.Contains(res.Response.Header.Get("Location"), "status=paid") || len(res.Events) != 1 {
		t.Fatalf("callback result %+v", res.Response)
	}
	// a tampered return url is not followed (open redirect)
	bad := strings.Replace(cb, "r=", "r=aHR0cHM6Ly9ldmlsLmV4YW1wbGUv&x=", 1)
	res, err = pr.HandleWebhook(ctx, fake.CallbackFor(bad, co.ProviderTxnID))
	if err != nil || res.Response.Status != 200 || res.Response.Header.Get("Location") != "" {
		t.Fatalf("tampered return url: %+v %v", res.Response, err)
	}
}

func TestFeeRoundingAndMerchantCommission(t *testing.T) {
	d := cfDetailResponse{PaymentStatus: "SUCCESS", PaidPrice: 100_000_000, IyziCommissionFee: 250_000, IyziCommissionRateAmount: 2_983_500, MerchantCommissionRateAmount: 10_000}
	st, err := settled(d)
	if err != nil {
		t.Fatal(err)
	}
	// 0.25 + 2.9835 + 0.01 = 3.2435: rounded once, on the sum
	if st.Gross.Amount != 10000 || st.Fee.Amount != 324 || st.Net.Amount != 9676 {
		t.Fatalf("%+v", st)
	}
	d.IyziCommissionFee = 200_000_000
	if _, err := settled(d); !errors.Is(err, sdk.ErrRejected) {
		t.Fatalf("a fee above the gross: %v", err)
	}
	if _, err := settled(cfDetailResponse{PaymentStatus: "SUCCESS", Currency: "USD", PaidPrice: 1_000_000}); !errors.Is(err, sdk.ErrRejected) {
		t.Fatalf("USD: %v", err)
	}
}

func TestDecimalHelpers(t *testing.T) {
	for in, want := range map[string]int64{"100": 10000, "100.0": 10000, "0.5": 50, "2.9835": 298, "2.995": 300, "-1.25": -125, "": 0} {
		var a amount
		if err := a.UnmarshalJSON([]byte(`"` + in + `"`)); err != nil || a.minor() != want {
			t.Errorf("%q -> %d %v, want %d", in, a.minor(), err, want)
		}
	}
	var a amount
	if err := a.UnmarshalJSON([]byte(`12.34`)); err != nil || a.minor() != 1234 {
		t.Errorf("number: %d %v", a.minor(), err)
	}
	if decimalString(5) != "0.05" || decimalString(123456) != "1234.56" || decimalString(-250) != "-2.50" {
		t.Errorf("decimalString")
	}
}

func TestStatus(t *testing.T) {
	pr, fake, ctx := newIyzico(t, orgIyzico(nil))
	co, _ := pr.CreateCheckout(ctx, checkoutReq())
	st, err := pr.Status(ctx, sdk.ProviderRef{ProviderTxnID: co.ProviderTxnID})
	if err != nil || st.State != sdk.PaymentPending {
		t.Fatalf("pending: %+v %v", st, err)
	}
	payNow(fake, co.ProviderTxnID)
	st, err = pr.Status(ctx, sdk.ProviderRef{ProviderTxnID: co.ProviderTxnID})
	if err != nil || st.State != sdk.PaymentPaid || st.Net.Amount != 9750 {
		t.Fatalf("paid: %+v %v", st, err)
	}
	if _, err := pr.Status(ctx, sdk.ProviderRef{ProviderTxnID: "no-such"}); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestRefund(t *testing.T) {
	pr, fake, ctx := newIyzico(t, orgIyzico(nil))
	co, _ := pr.CreateCheckout(ctx, checkoutReq())
	req := sdk.RefundRequest{ProviderTxnID: co.ProviderTxnID, RefundRef: "r1", Amount: sdk.Money{Amount: 10000, Currency: "TRY"}}
	if _, err := pr.Refund(ctx, req); !errors.Is(err, sdk.ErrRejected) {
		t.Fatalf("an unpaid checkout cannot be refunded: %v", err)
	}
	payNow(fake, co.ProviderTxnID)
	half := req
	half.Amount.Amount = 5000
	if _, err := pr.Refund(ctx, half); !errors.Is(err, sdk.ErrUnsupported) {
		t.Fatalf("partial: %v", err)
	}
	res, err := pr.Refund(ctx, req)
	if err != nil || res.Amount.Amount != 10000 || res.ProviderRefundID == "" {
		t.Fatalf("refund: %+v %v", res, err)
	}
	if tx, _ := fake.Txn(co.ProviderTxnID); !tx.Refunded {
		t.Fatal("iyzico did not refund")
	}
	// iyzico has no idempotency for refunds: the pack looks first, so a replay is not an error
	again, err := pr.Refund(ctx, req)
	if err != nil || again.ProviderRefundID != res.ProviderRefundID {
		t.Fatalf("replayed refund: %+v %v", again, err)
	}
}

func TestSettlementsDeriveOneSettlementPerDay(t *testing.T) {
	pr, fake, ctx := newIyzico(t, orgIyzico(nil))
	for i, tok := range []string{"a", "b", "c"} {
		fake.Seed(trtest.IyzicoTxn{Token: tok, PaymentID: "10" + tok, ConversationID: tok, BasketID: "tr1.s.u.o", Price: "100.00"})
		fake.Pay(tok, "2026-03-05", "0.25", "2.25")
		_ = i
	}
	fake.Seed(trtest.IyzicoTxn{Token: "d", PaymentID: "10d", ConversationID: "d", BasketID: "tr1.s.u.o", Price: "50.00"})
	fake.Pay("d", "2026-03-06", "0.25", "0.00")
	// a refunded payment of the 5th is deducted
	if _, err := pr.Refund(ctx, sdk.RefundRequest{ProviderTxnID: "c", RefundRef: "r"}); err != nil {
		t.Fatal(err)
	}
	got, err := pr.Settlements(ctx, sdk.SettlementRequest{From: "2026-03-04", To: "2026-03-07"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("settlements %+v, want the 5th and the 6th (the pages of the report are read to the end)", got)
	}
	d5, d6 := got[0], got[1]
	if d5.Ref != "iyzico-2026-03-05" || d5.Gross.Amount != 20000 || d5.Net.Amount != 19500 || d5.Fee.Amount != 500 || d5.Net.Amount != d5.Gross.Amount-d5.Fee.Amount {
		t.Errorf("5th: %+v", d5)
	}
	if d6.Ref != "iyzico-2026-03-06" || d6.Net.Amount != 4975 || d6.Fee.Amount != 25 {
		t.Errorf("6th: %+v", d6)
	}
	// deterministic: the same call gives the same refs
	again, _ := pr.Settlements(ctx, sdk.SettlementRequest{From: "2026-03-04", To: "2026-03-07"})
	if len(again) != 2 || again[0].Ref != d5.Ref {
		t.Errorf("not deterministic: %+v", again)
	}
	for _, bad := range []sdk.SettlementRequest{{From: "x", To: "2026-03-07"}, {From: "2026-03-07", To: "2026-03-01"}, {From: "2025-01-01", To: "2026-03-01"}} {
		if _, err := pr.Settlements(ctx, bad); !errors.Is(err, sdk.ErrRejected) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestAuthHeaderMatchesTheDocumentedAlgorithm(t *testing.T) {
	// documented: base64("apiKey:<k>&randomKey:<r>&signature:<hex hmac-sha256(secret, r + uri path + body)>")
	got := authHeader("api", "secret", "123456789", "/payment/bin/check", []byte(`{"locale":"tr"}`))
	enc, ok := strings.CutPrefix(got, "IYZWSv2 ")
	if !ok {
		t.Fatal(got)
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatal(err)
	}
	m := hmac.New(sha256.New, []byte("secret"))
	m.Write([]byte(`123456789/payment/bin/check{"locale":"tr"}`))
	if want := "apiKey:api&randomKey:123456789&signature:" + hex.EncodeToString(m.Sum(nil)); string(raw) != want {
		t.Fatalf("%s != %s", raw, want)
	}
}

func TestValidateSettings(t *testing.T) {
	p := New()
	org := sdk.OrgScope()
	bad := []map[string]any{
		{"tr_payments_iyzico_api_key": "k"},
		{"tr_payments_iyzico_enabled": true},
		{"tr_payments_iyzico_callback_base_url": "http://insecure.example"},
		{"tr_sms_iletimerkezi_username": "u"},
		{"tr_sms_iletimerkezi_enabled": true, "tr_sms_iletimerkezi_username": "u", "tr_sms_iletimerkezi_password": "p"},
	}
	for _, v := range bad {
		if err := p.ValidateSettings(context.Background(), org, v); err == nil {
			t.Errorf("%v accepted", v)
		}
	}
	good := map[string]any{"tr_payments_iyzico_enabled": true, "tr_payments_iyzico_api_key": "k", "tr_payments_iyzico_secret_key": "s",
		"tr_sms_iletimerkezi_enabled": true, "tr_sms_iletimerkezi_username": "u", "tr_sms_iletimerkezi_password": "p", "tr_sms_iletimerkezi_sender_id": "SITEYONT"}
	if err := p.ValidateSettings(context.Background(), org, good); err != nil {
		t.Fatal(err)
	}
	// a site document may leave out what the organization holds
	if err := p.ValidateSettings(context.Background(), sdk.SiteScope(tSite), map[string]any{"tr_payments_iyzico_enabled": true}); err != nil {
		t.Fatal(err)
	}
}
