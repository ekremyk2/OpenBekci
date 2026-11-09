// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openbekci/openbekci/sdk"
)

// IyzicoID is the payment provider id; the payment method of a card payment is "card:tr.iyzico".
const IyzicoID = ID + ".iyzico"

// SignatureHeader is the header iyzico signs its webhook notifications with.
const SignatureHeader = "X-IYZ-SIGNATURE-V3"

// basketPrefix marks the basketId of a checkout this pack created.
const basketPrefix = "tr1"

// maxBasketID is the length assumed to be accepted for iyzico's basketId.
const maxBasketID = 255

// iyzico is the iyzico PaymentProvider on the Checkout Form (hosted payment
// page) API. The flow, and everything assumed, is in PAYMENTS.md.
//
// The provider transaction id is the Checkout Form token: it exists from the
// moment the checkout is created, is what the callback and the webhook carry,
// and is enough to find the payment (retrieve) and refund it.
type iyzico struct{ p *Pack }

var _ sdk.PaymentProvider = (*iyzico)(nil)

func (*iyzico) ID() string { return IyzicoID }

// creds finds the merchant account to use.
//
// TODO(PR #35): replace the site-then-organization lookup with the scope
// sdk.AccountScope returns for the merchant-of-record setting (the site's own
// iyzico account, or the management company's at organization level).
func (pr *iyzico) creds(ctx context.Context, site sdk.ID) (iyzicoCreds, error) {
	scopes := []sdk.Scope{sdk.OrgScope()}
	if site != "" {
		scopes = []sdk.Scope{sdk.SiteScope(site), sdk.OrgScope()}
	}
	for _, sc := range scopes {
		doc, err := pr.p.svc.Settings().All(ctx, sc)
		if err != nil {
			return iyzicoCreds{}, err
		}
		key, _ := doc["tr_payments_iyzico_api_key"].(string)
		secret, _ := doc["tr_payments_iyzico_secret_key"].(string)
		if key == "" || secret == "" {
			continue
		}
		if en, _ := doc["tr_payments_iyzico_enabled"].(bool); !en {
			return iyzicoCreds{}, fmt.Errorf("iyzico is not enabled in the settings: %w", sdk.ErrUnsupported)
		}
		sandbox, _ := doc["tr_payments_iyzico_sandbox"].(bool)
		base, _ := doc["tr_payments_iyzico_callback_base_url"].(string)
		return iyzicoCreds{APIKey: key, SecretKey: secret, Sandbox: sandbox, CallbackBase: strings.TrimRight(base, "/")}, nil
	}
	return iyzicoCreds{}, fmt.Errorf("no iyzico api key and secret key are configured: %w", sdk.ErrCredentials)
}

func (pr *iyzico) client(ctx context.Context, site sdk.ID) (*iyzicoClient, error) {
	c, err := pr.creds(ctx, site)
	if err != nil {
		return nil, err
	}
	return &iyzicoClient{http: pr.p.client(), creds: c}, nil
}

// mapErr turns an iyzico failure answer into an SDK error. The error codes of
// iyzico are not stable enough to switch on, so the message is read instead
// (assumption, PAYMENTS.md).
func mapErr(err error) error {
	ie, ok := err.(*iyzicoError)
	if !ok {
		return err
	}
	msg := strings.ToLower(ie.Message)
	switch {
	case strings.Contains(msg, "token") && (strings.Contains(msg, "bulunamad") || strings.Contains(msg, "not found") || strings.Contains(msg, "invalid") || strings.Contains(msg, "geçersiz")):
		return fmt.Errorf("%w: %w", sdk.ErrNotFound, ie)
	case strings.Contains(msg, "api key") || strings.Contains(msg, "api bilgileri") || strings.Contains(msg, "signature") || strings.Contains(msg, "imza"):
		return fmt.Errorf("%w: %w", sdk.ErrCredentials, ie)
	}
	return fmt.Errorf("%w: %w", sdk.ErrRejected, ie)
}

// encodeBasket carries the payment target in the basketId, which iyzico
// returns on retrieve: tr1.<site>.<unit>.<o|t>[.<charge>,<charge>...].
func encodeBasket(t sdk.PaymentTarget) (string, error) {
	kind := "o"
	switch t.DebtorKind {
	case "owner", "":
	case "tenant":
		kind = "t"
	default:
		return "", fmt.Errorf("debtor kind %q: %w", t.DebtorKind, sdk.ErrRejected)
	}
	parts := []string{basketPrefix, string(t.SiteID), string(t.UnitID), kind}
	if len(t.ChargeIDs) > 0 {
		ids := make([]string, len(t.ChargeIDs))
		for i, c := range t.ChargeIDs {
			ids[i] = string(c)
		}
		parts = append(parts, strings.Join(ids, ","))
	}
	s := strings.Join(parts, ".")
	if len(s) > maxBasketID {
		return "", fmt.Errorf("a checkout for %d charges does not fit iyzico's basket id: pay fewer charges at once: %w", len(t.ChargeIDs), sdk.ErrRejected)
	}
	return s, nil
}

func decodeBasket(s string) (sdk.PaymentTarget, error) {
	parts := strings.Split(s, ".")
	if len(parts) < 4 || len(parts) > 5 || parts[0] != basketPrefix || parts[1] == "" || parts[2] == "" {
		return sdk.PaymentTarget{}, fmt.Errorf("basket id %q is not one of this system's checkouts: %w", s, sdk.ErrRejected)
	}
	t := sdk.PaymentTarget{SiteID: sdk.ID(parts[1]), UnitID: sdk.ID(parts[2]), DebtorKind: "owner"}
	switch parts[3] {
	case "o":
	case "t":
		t.DebtorKind = "tenant"
	default:
		return sdk.PaymentTarget{}, fmt.Errorf("basket id %q: bad debtor kind: %w", s, sdk.ErrRejected)
	}
	if len(parts) == 5 && parts[4] != "" {
		for _, c := range strings.Split(parts[4], ",") {
			t.ChargeIDs = append(t.ChargeIDs, sdk.ID(c))
		}
	}
	return t, nil
}

// callbackSig signs the return URL carried in the callback URL, so that the
// redirect after payment cannot be pointed at a foreign site.
func callbackSig(secret, site, returnURL string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("cb|" + site + "|" + returnURL))
	return hex.EncodeToString(m.Sum(nil))
}

// KVKK minimisation: iyzico requires buyer fields the system does not hold
// (national id, address) or should not send. These placeholders are sent
// instead; the name, e-mail and phone are sent only when the person has them.
const (
	placeholderIdentity = "11111111111"
	placeholderAddress  = "Adres bilgisi paylasilmadi"
	placeholderCity     = "Istanbul"
	placeholderCountry  = "Turkey"
	placeholderIP       = "85.34.78.112"
	placeholderPhone    = "+905000000000"
	placeholderEmail    = "sakin@example.invalid"
)

func splitName(full string) (name, surname string) {
	f := strings.Fields(full)
	switch len(f) {
	case 0:
		return "Sakin", "Sakin"
	case 1:
		return f[0], f[0]
	}
	return strings.Join(f[:len(f)-1], " "), f[len(f)-1]
}

func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (pr *iyzico) CreateCheckout(ctx context.Context, req sdk.CheckoutRequest) (sdk.Checkout, error) {
	if req.Gross.Currency != "TRY" {
		return sdk.Checkout{}, fmt.Errorf("iyzico takes TRY only, not %q: %w", req.Gross.Currency, sdk.ErrRejected)
	}
	if req.Gross.Amount <= 0 {
		return sdk.Checkout{}, fmt.Errorf("the amount must be positive: %w", sdk.ErrRejected)
	}
	if req.Reference == "" {
		return sdk.Checkout{}, fmt.Errorf("a checkout needs a reference: %w", sdk.ErrRejected)
	}
	basket, err := encodeBasket(req.Target)
	if err != nil {
		return sdk.Checkout{}, err
	}
	cl, err := pr.client(ctx, req.Target.SiteID)
	if err != nil {
		return sdk.Checkout{}, err
	}
	org, ok := sdk.OrgFrom(ctx)
	if !ok {
		return sdk.Checkout{}, sdk.ErrNoOrg
	}
	base := cl.creds.CallbackBase
	if base == "" {
		base = origin(req.ReturnURL)
	}
	if base == "" {
		return sdk.Checkout{}, fmt.Errorf("set the public address of the server (tr_payments_iyzico_callback_base_url) or give a return url: %w", sdk.ErrRejected)
	}
	q := url.Values{"site": {string(req.Target.SiteID)}}
	if req.ReturnURL != "" {
		q.Set("r", base64.RawURLEncoding.EncodeToString([]byte(req.ReturnURL)))
		q.Set("s", callbackSig(cl.creds.SecretKey, string(req.Target.SiteID), req.ReturnURL))
	}
	callback := base + "/v1/orgs/" + string(org) + "/packs/" + ID + "/payments/" + IyzicoID + "/webhook?" + q.Encode()

	name, surname := splitName(req.Payer.Name)
	email, phone := req.Payer.Email, req.Payer.Phone
	if email == "" {
		email = placeholderEmail
	}
	if phone == "" {
		phone = placeholderPhone
	}
	price := decimalString(req.Gross.Amount)
	locale, itemName := "tr", "Aidat ve ortak gider odemesi"
	if strings.HasPrefix(strings.ToLower(req.Locale), "en") {
		locale, itemName = "en", "Dues and shared costs payment"
	}
	addr := iyzAddress{ContactName: name + " " + surname, City: placeholderCity, Country: placeholderCountry, Address: placeholderAddress}
	body := cfInitRequest{
		Locale: locale, ConversationID: req.Reference, Price: price, PaidPrice: price, Currency: "TRY",
		BasketID: basket, PaymentGroup: "PRODUCT", CallbackURL: callback,
		EnabledInstallments: []int{1},
		Buyer: iyzBuyer{ID: string(req.Target.UnitID), Name: name, Surname: surname, IdentityNumber: placeholderIdentity, Email: email,
			GSMNumber: phone, RegistrationAddress: placeholderAddress, IP: placeholderIP, City: placeholderCity, Country: placeholderCountry},
		ShippingAddress: addr, BillingAddress: addr,
		BasketItems: []iyzItem{{ID: req.Reference, Name: itemName, Category1: "Aidat", ItemType: "VIRTUAL", Price: price}},
	}
	var out cfInitResponse
	if err := cl.do(ctx, http.MethodPost, pathCFInit, nil, body, &out); err != nil {
		return sdk.Checkout{}, mapErr(err)
	}
	if out.Token == "" || out.PaymentPageURL == "" {
		return sdk.Checkout{}, fmt.Errorf("%w: iyzico answered without a token or a payment page", sdk.ErrUnavailable)
	}
	return sdk.Checkout{ProviderTxnID: out.Token, CheckoutURL: out.PaymentPageURL}, nil
}

func (pr *iyzico) retrieve(ctx context.Context, cl *iyzicoClient, token string) (cfDetailResponse, error) {
	var d cfDetailResponse
	err := cl.do(ctx, http.MethodPost, pathCFDetail, nil, cfDetailRequest{Locale: "tr", Token: token}, &d)
	if err != nil {
		return d, mapErr(err)
	}
	return d, nil
}

// settled turns a retrieve answer into a status. paymentStatus SUCCESS is
// paid, FAILURE failed, anything else (INIT_BANK_TRANSFER, empty: the form was
// opened but not completed) pending.
func settled(d cfDetailResponse) (sdk.PaymentStatus, error) {
	switch strings.ToUpper(d.PaymentStatus) {
	case "SUCCESS":
		if d.Currency != "" && d.Currency != "TRY" {
			return sdk.PaymentStatus{}, fmt.Errorf("paid in %q, not TRY: %w", d.Currency, sdk.ErrRejected)
		}
		gross := d.PaidPrice.minor()
		// iyzico's commission is the fee plus the rate amount; the merchant
		// commission (instalment cost) is added, installments are off so it is normally 0
		fee := (d.IyziCommissionFee + d.IyziCommissionRateAmount + d.MerchantCommissionRateAmount).minor()
		if gross <= 0 || fee < 0 || fee > gross {
			return sdk.PaymentStatus{}, fmt.Errorf("iyzico reported gross %d and fee %d: %w", gross, fee, sdk.ErrRejected)
		}
		m := func(n int64) sdk.Money { return sdk.Money{Amount: n, Currency: "TRY"} }
		return sdk.PaymentStatus{State: sdk.PaymentPaid, Gross: m(gross), Fee: m(fee), Net: m(gross - fee)}, nil
	case "FAILURE":
		return sdk.PaymentStatus{State: sdk.PaymentFailed}, nil
	}
	return sdk.PaymentStatus{State: sdk.PaymentPending}, nil
}

func (pr *iyzico) Status(ctx context.Context, ref sdk.ProviderRef) (sdk.PaymentStatus, error) {
	cl, err := pr.client(ctx, "")
	if err != nil {
		return sdk.PaymentStatus{}, err
	}
	d, err := pr.retrieve(ctx, cl, ref.ProviderTxnID)
	if err != nil {
		return sdk.PaymentStatus{}, err
	}
	return settled(d)
}

type webhookBody struct {
	EventType      string `json:"iyziEventType"`
	IyziPaymentID  string `json:"iyziPaymentId"`
	Token          string `json:"token"`
	ConversationID string `json:"paymentConversationId"`
	Status         string `json:"status"`
}

func (pr *iyzico) HandleWebhook(ctx context.Context, req sdk.WebhookRequest) (sdk.WebhookResult, error) {
	site := sdk.ID(req.Query.Get("site"))
	cl, err := pr.client(ctx, site)
	if err != nil {
		// without credentials nothing can be verified
		return sdk.WebhookResult{}, fmt.Errorf("webhook: %w", sdk.ErrInvalidSignature)
	}
	var token string
	redirect := ""
	trimmed := strings.TrimSpace(string(req.Body))
	if strings.HasPrefix(trimmed, "{") {
		// the server-to-server notification: verified by its signature
		var wb webhookBody
		if err := json.Unmarshal(req.Body, &wb); err != nil {
			return sdk.WebhookResult{}, fmt.Errorf("webhook body: %w", sdk.ErrRejected)
		}
		want := webhookSignature(cl.creds.SecretKey, wb.EventType, wb.IyziPaymentID, wb.Token, wb.ConversationID, wb.Status)
		got := strings.ToLower(strings.TrimSpace(req.Header.Get(SignatureHeader)))
		if got == "" || !hmac.Equal([]byte(want), []byte(got)) {
			return sdk.WebhookResult{}, fmt.Errorf("webhook: %w", sdk.ErrInvalidSignature)
		}
		token = wb.Token
		if token == "" {
			return sdk.WebhookResult{Response: ok()}, nil // a notification of another kind (a plain payment): acknowledged
		}
	} else {
		// the browser callback after the hosted page: only a token, authenticated by retrieving it
		form, err := url.ParseQuery(trimmed)
		if err != nil || form.Get("token") == "" {
			return sdk.WebhookResult{}, fmt.Errorf("callback without a token: %w", sdk.ErrRejected)
		}
		token = form.Get("token")
		if r := req.Query.Get("r"); r != "" {
			if raw, err := base64.RawURLEncoding.DecodeString(r); err == nil {
				ru := string(raw)
				if s := req.Query.Get("s"); s != "" && hmac.Equal([]byte(s), []byte(callbackSig(cl.creds.SecretKey, string(site), ru))) && (strings.HasPrefix(ru, "https://") || strings.HasPrefix(ru, "http://")) {
					redirect = ru
				}
			}
		}
	}
	d, err := pr.retrieve(ctx, cl, token)
	if err != nil {
		return sdk.WebhookResult{}, err
	}
	st, err := settled(d)
	if err != nil {
		return sdk.WebhookResult{}, err
	}
	var events []sdk.PaymentEvent
	switch st.State {
	case sdk.PaymentPaid:
		target, err := decodeBasket(d.BasketID)
		if err != nil {
			return sdk.WebhookResult{}, err
		}
		// OccurredAt stays zero: the core books the payment on the day it is confirmed, at the site
		events = append(events, sdk.PaymentEvent{Kind: sdk.EventKindPaid, ProviderTxnID: token, Target: target, Gross: st.Gross, Fee: st.Fee, Net: st.Net, Reference: d.ConversationID})
	case sdk.PaymentFailed:
		events = append(events, sdk.PaymentEvent{Kind: sdk.EventKindFailed, ProviderTxnID: token, Reference: d.ConversationID})
	}
	resp := ok()
	if redirect != "" {
		u, _ := url.Parse(redirect)
		qq := u.Query()
		qq.Set("status", string(st.State))
		u.RawQuery = qq.Encode()
		resp = &sdk.HTTPResponse{Status: http.StatusSeeOther, Header: http.Header{"Location": {u.String()}}}
	}
	return sdk.WebhookResult{Events: events, Response: resp}, nil
}

func ok() *sdk.HTTPResponse {
	return &sdk.HTTPResponse{Status: http.StatusOK, Header: http.Header{"Content-Type": {"text/plain; charset=utf-8"}}, Body: []byte("OK")}
}

// refundedStatuses are the paymentRefundStatus values of the transaction
// detail report that mean "refunded in full" (assumption, PAYMENTS.md).
var refundedStatuses = map[string]bool{"FULL": true, "FULLY_REFUNDED": true, "REFUNDED": true, "FULL_REFUND": true}

func (pr *iyzico) Refund(ctx context.Context, req sdk.RefundRequest) (sdk.RefundResult, error) {
	cl, err := pr.client(ctx, "")
	if err != nil {
		return sdk.RefundResult{}, err
	}
	d, err := pr.retrieve(ctx, cl, req.ProviderTxnID)
	if err != nil {
		return sdk.RefundResult{}, err
	}
	st, err := settled(d)
	if err != nil {
		return sdk.RefundResult{}, err
	}
	if st.State != sdk.PaymentPaid {
		return sdk.RefundResult{}, fmt.Errorf("the transaction is %s, not paid: %w", st.State, sdk.ErrRejected)
	}
	if req.Amount.Amount != 0 && req.Amount.Amount != st.Gross.Amount {
		return sdk.RefundResult{}, fmt.Errorf("partial refunds: %w", sdk.ErrUnsupported)
	}
	// the refund id is deterministic: a payment is refunded once, in full
	res := sdk.RefundResult{ProviderRefundID: "iyz-refund-" + d.PaymentID, Amount: st.Gross}
	// iyzico documents no idempotency for refunds: look at the payment first, so a replay is not an error
	var det payDetailResponse
	if err := cl.do(ctx, http.MethodGet, pathPayDetail, url.Values{"paymentId": {d.PaymentID}}, nil, &det); err == nil && refundedStatuses[strings.ToUpper(det.PaymentRefundStatus)] {
		return res, nil
	}
	var out refundResponse
	err = cl.do(ctx, http.MethodPost, pathRefundV2, nil, refundV2Request{Locale: "tr", ConversationID: req.RefundRef, PaymentID: d.PaymentID, Price: decimalString(st.Gross.Amount)}, &out)
	if err != nil {
		return sdk.RefundResult{}, mapErr(err)
	}
	return res, nil
}

// maxSettlementDays bounds one Settlements call (one report request per day).
const maxSettlementDays = 93

// Settlements derives one settlement per day from the daily transaction
// report: iyzico documents no payout report API (payouts are reconciled with
// downloadable settlement files). The settlement of a day is the net of that
// day's payments minus its refunds and cancellations; the reference is
// "iyzico-<date>", deterministic, so a replay posts nothing twice. SettledOn
// is the transaction date, not the bank date (iyzico pays out after a
// blockage period): see PAYMENTS.md for the limitation.
func (pr *iyzico) Settlements(ctx context.Context, req sdk.SettlementRequest) ([]sdk.Settlement, error) {
	from, err := time.Parse("2006-01-02", req.From)
	if err != nil {
		return nil, fmt.Errorf("from %q: %w", req.From, sdk.ErrRejected)
	}
	to, err := time.Parse("2006-01-02", req.To)
	if err != nil || to.Before(from) {
		return nil, fmt.Errorf("to %q: %w", req.To, sdk.ErrRejected)
	}
	if to.Sub(from) > maxSettlementDays*24*time.Hour {
		return nil, fmt.Errorf("a settlement period is at most %d days: %w", maxSettlementDays, sdk.ErrRejected)
	}
	cl, err := pr.client(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []sdk.Settlement
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		var gross, fee, net int64
		for page := 1; ; page++ {
			var r dailyResponse
			q := url.Values{"transactionDate": {date}, "page": {fmt.Sprint(page)}}
			if err := cl.do(ctx, http.MethodGet, pathDailyTx, q, nil, &r); err != nil {
				return nil, mapErr(err)
			}
			for _, t := range r.Transactions {
				g, n := t.PaidPrice.minor(), t.MerchantPayoutAmount.minor()
				switch strings.ToUpper(t.TransactionType) {
				case "PAYMENT":
					gross, net = gross+g, net+n
					fee += g - n
				case "REFUND", "CANCEL":
					if g < 0 {
						g = -g
					}
					if n < 0 {
						n = -n
					}
					gross, net = gross-g, net-n
					fee -= g - n
				}
			}
			if page >= r.TotalPageCount {
				break
			}
		}
		if net <= 0 {
			continue
		}
		m := func(n int64) sdk.Money { return sdk.Money{Amount: n, Currency: "TRY"} }
		out = append(out, sdk.Settlement{Ref: "iyzico-" + date, SettledOn: date, Gross: m(gross), Fee: m(fee), Net: m(net)})
	}
	return out, nil
}

// BasketID returns the iyzico basketId that carries a payment target (the
// provider hands it back on retrieve). Exposed for tests and tools that build
// provider fixtures.
func BasketID(t sdk.PaymentTarget) (string, error) { return encodeBasket(t) }
