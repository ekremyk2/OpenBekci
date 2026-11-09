// SPDX-License-Identifier: AGPL-3.0-or-later

// Package trtest holds the test doubles and fixtures of the Turkish pack: an
// in-process fake of iyzico's Checkout Form API, an in-process fake of İleti
// Merkezi's SMS API, and the synthetic bank statement fixtures. The fakes
// reproduce the request and response shapes of the public documentation and
// verify what the real services verify (the IYZWSv2 signature of every
// request), so a pack bug in signing shows up as a 401 here.
//
// The fakes are deliberately independent of package tr (they re-implement the
// documented signature algorithms), so that the pack is tested against the
// documentation and not against itself.
package trtest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/openbekci/openbekci/sdk"
)

// IyzicoTxn is one transaction of the fake.
type IyzicoTxn struct {
	Token, PaymentID, ConversationID, BasketID string
	Price                                      string
	// Status is "" (the form was not completed), "SUCCESS" or "FAILURE".
	Status string
	// IyziFee and IyziRate are iyziCommissionFee and iyziCommissionRateAmount.
	IyziFee, IyziRate string
	// Date is the civil date the payment happened (the daily report is by date).
	Date     string
	Refunded bool
	Currency string
}

// Iyzico is the fake iyzico server.
type Iyzico struct {
	Server            *httptest.Server
	APIKey, SecretKey string

	mu   sync.Mutex
	txns map[string]*IyzicoTxn
	seq  int
	// Calls records "METHOD path" of every authenticated request.
	Calls []string
	// InitRequests keeps the decoded bodies of the initialize calls.
	InitRequests []map[string]any
}

// NewIyzico starts a fake accepting the given credentials; close it with Close.
func NewIyzico(apiKey, secretKey string) *Iyzico {
	f := &Iyzico{APIKey: apiKey, SecretKey: secretKey, txns: map[string]*IyzicoTxn{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// Close stops the server.
func (f *Iyzico) Close() { f.Server.Close() }

// Client returns an HTTP client that sends every request, whatever its host,
// to the fake: pass it to tr.WithHTTPClient. A go-vcr recorder replaces it
// the same way (PAYMENTS.md).
func (f *Iyzico) Client() *http.Client { return RewriteClient(f.Server.URL) }

// Seed adds a transaction (a paid one, for webhook fixtures) and returns it.
func (f *Iyzico) Seed(t IyzicoTxn) *IyzicoTxn {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t.Currency == "" {
		t.Currency = "TRY"
	}
	c := t
	f.txns[t.Token] = &c
	return &c
}

// Pay marks a transaction paid with the given iyzico commission parts.
func (f *Iyzico) Pay(token, date, iyziFee, iyziRate string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.txns[token]
	t.Status, t.Date, t.IyziFee, t.IyziRate = "SUCCESS", date, iyziFee, iyziRate
	f.seq++
	if t.PaymentID == "" {
		t.PaymentID = fmt.Sprintf("%d", 900000+f.seq)
	}
}

// Txn returns a copy of a transaction.
func (f *Iyzico) Txn(token string) (IyzicoTxn, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.txns[token]
	if !ok {
		return IyzicoTxn{}, false
	}
	return *t, true
}

// Tokens returns the tokens of the transactions created through initialize.
func (f *Iyzico) Tokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.txns {
		out = append(out, k)
	}
	return out
}

func hmacHex(key, msg string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

// WebhookSignature is the documented X-IYZ-SIGNATURE-V3 of a Checkout Form
// notification (docs.iyzico.com, Webhook, HPP format).
func WebhookSignature(secret, eventType, iyziPaymentID, token, conversationID, status string) string {
	return hmacHex(secret, secret+eventType+iyziPaymentID+token+conversationID+status)
}

// Webhook builds the signed server-to-server notification of a transaction.
func (f *Iyzico) Webhook(token string) sdk.WebhookRequest {
	t, _ := f.Txn(token)
	status := t.Status
	if status == "" {
		status = "FAILURE"
	}
	body, _ := json.Marshal(map[string]any{
		"iyziEventType": "CHECKOUT_FORM_AUTH", "iyziEventTime": 1700000000000, "iyziReferenceCode": "ref-" + token,
		"iyziPaymentId": t.PaymentID, "token": token, "paymentConversationId": t.ConversationID, "merchantId": 777, "status": status,
	})
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("X-IYZ-SIGNATURE-V3", WebhookSignature(f.SecretKey, "CHECKOUT_FORM_AUTH", t.PaymentID, token, t.ConversationID, status))
	return sdk.WebhookRequest{Method: "POST", Header: h, Body: body}
}

// CallbackFor builds the browser callback iyzico posts to the callbackUrl: a
// form with the token, to the callback URL (whose query the host passes on).
func (f *Iyzico) CallbackFor(callbackURL, token string) sdk.WebhookRequest {
	u, _ := url.Parse(callbackURL)
	h := http.Header{}
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	return sdk.WebhookRequest{Method: "POST", Header: h, Query: u.Query(), Body: []byte("token=" + url.QueryEscape(token))}
}

func (f *Iyzico) reject(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "failure", "errorCode": code, "errorMessage": msg, "locale": "tr", "systemTime": 1700000000000})
}

func (f *Iyzico) ok(w http.ResponseWriter, v map[string]any) {
	v["status"], v["locale"], v["systemTime"] = "success", "tr", 1700000000000
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// authentic verifies the IYZWSv2 Authorization header: base64 of
// "apiKey:<key>&randomKey:<rnd>&signature:<hex hmac-sha256(randomKey+uriPath+body)>".
func (f *Iyzico) authentic(r *http.Request, body []byte) bool {
	h := r.Header.Get("Authorization")
	enc, ok := strings.CutPrefix(h, "IYZWSv2 ")
	if !ok {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return false
	}
	parts := strings.Split(string(raw), "&")
	if len(parts) != 3 {
		return false
	}
	key, ok1 := strings.CutPrefix(parts[0], "apiKey:")
	rnd, ok2 := strings.CutPrefix(parts[1], "randomKey:")
	sig, ok3 := strings.CutPrefix(parts[2], "signature:")
	if !ok1 || !ok2 || !ok3 || key != f.APIKey || r.Header.Get("x-iyzi-rnd") != rnd {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(hmacHex(f.SecretKey, rnd+r.URL.Path+string(body))))
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func (f *Iyzico) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if !f.authentic(r, body) {
		f.reject(w, http.StatusUnauthorized, "1000", "Geçersiz imza (invalid signature)")
		return
	}
	f.mu.Lock()
	f.Calls = append(f.Calls, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	var in map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &in); err != nil {
			f.reject(w, 400, "1", "bad json")
			return
		}
	}
	switch r.Method + " " + r.URL.Path {
	case "POST /payment/iyzipos/checkoutform/initialize/auth/ecom":
		f.initialize(w, in)
	case "POST /payment/iyzipos/checkoutform/auth/ecom/detail":
		f.detail(w, in)
	case "POST /v2/payment/refund":
		f.refund(w, in)
	case "GET /v2/reporting/payment/details":
		f.paymentDetails(w, r)
	case "GET /v2/reporting/payment/transactions":
		f.daily(w, r)
	default:
		f.reject(w, 404, "404", "no such endpoint "+r.URL.Path)
	}
}

func (f *Iyzico) initialize(w http.ResponseWriter, in map[string]any) {
	// the fields the documentation marks as required
	for _, k := range []string{"price", "paidPrice", "callbackUrl", "currency"} {
		if str(in, k) == "" {
			f.reject(w, 400, "11", k+" zorunludur")
			return
		}
	}
	buyer, _ := in["buyer"].(map[string]any)
	for _, k := range []string{"id", "name", "surname", "identityNumber", "email", "gsmNumber", "registrationAddress", "city", "country"} {
		if str(buyer, k) == "" {
			f.reject(w, 400, "11", "buyer."+k+" zorunludur")
			return
		}
	}
	for _, a := range []string{"billingAddress", "shippingAddress"} {
		m, _ := in[a].(map[string]any)
		for _, k := range []string{"address", "contactName", "city", "country"} {
			if str(m, k) == "" {
				f.reject(w, 400, "11", a+"."+k+" zorunludur")
				return
			}
		}
	}
	items, _ := in["basketItems"].([]any)
	if len(items) == 0 {
		f.reject(w, 400, "11", "basketItems zorunludur")
		return
	}
	for _, it := range items {
		m, _ := it.(map[string]any)
		for _, k := range []string{"id", "price", "name", "category1", "itemType"} {
			if str(m, k) == "" {
				f.reject(w, 400, "11", "basketItems."+k+" zorunludur")
				return
			}
		}
	}
	if !strings.HasPrefix(str(in, "callbackUrl"), "https://") && !strings.HasPrefix(str(in, "callbackUrl"), "http://localhost") {
		f.reject(w, 400, "11", "callbackUrl gecerli bir SSL sertifikasi gerektirir")
		return
	}
	f.mu.Lock()
	f.seq++
	token := fmt.Sprintf("tok-%04d", f.seq)
	f.txns[token] = &IyzicoTxn{Token: token, ConversationID: str(in, "conversationId"), BasketID: str(in, "basketId"), Price: str(in, "price"), Currency: str(in, "currency")}
	f.InitRequests = append(f.InitRequests, in)
	f.mu.Unlock()
	f.ok(w, map[string]any{"conversationId": str(in, "conversationId"), "token": token, "tokenExpireTime": 1800,
		"checkoutFormContent": base64.StdEncoding.EncodeToString([]byte("<script>/* fake */</script>")),
		"paymentPageUrl":      "https://sandbox-cpp.iyzipay.com?token=" + token + "&lang=tr"})
}

func (f *Iyzico) detail(w http.ResponseWriter, in map[string]any) {
	f.mu.Lock()
	t, ok := f.txns[str(in, "token")]
	var c IyzicoTxn
	if ok {
		c = *t
	}
	f.mu.Unlock()
	if !ok {
		f.reject(w, 400, "5", "token bulunamadı (invalid token)")
		return
	}
	out := map[string]any{"conversationId": c.ConversationID, "token": c.Token, "basketId": c.BasketID, "currency": c.Currency, "price": c.Price, "paidPrice": c.Price, "paymentStatus": c.Status}
	if c.Status == "SUCCESS" {
		out["paymentId"] = c.PaymentID
		out["iyziCommissionFee"], out["iyziCommissionRateAmount"] = json.Number(c.IyziFee), json.Number(c.IyziRate)
		out["merchantCommissionRate"], out["merchantCommissionRateAmount"] = json.Number("0"), json.Number("0")
		out["fraudStatus"] = 1
		out["itemTransactions"] = []map[string]any{{"paymentTransactionId": "tx" + c.PaymentID, "itemId": c.ConversationID, "paidPrice": c.Price, "merchantPayoutAmount": "0"}}
	}
	f.ok(w, out)
}

func (f *Iyzico) byPaymentID(id string) *IyzicoTxn {
	for _, t := range f.txns {
		if t.PaymentID == id && t.Status == "SUCCESS" {
			return t
		}
	}
	return nil
}

func (f *Iyzico) refund(w http.ResponseWriter, in map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.byPaymentID(str(in, "paymentId"))
	switch {
	case t == nil:
		f.reject(w, 400, "10", "odeme bulunamadi (payment not found)")
	case t.Refunded:
		f.reject(w, 400, "10051", "bu odeme zaten iade edilmis (already refunded)")
	case str(in, "price") != t.Price && !decEqual(str(in, "price"), t.Price):
		f.reject(w, 400, "10052", "iade tutari odeme tutarini asamaz")
	default:
		t.Refunded = true
		f.ok(w, map[string]any{"paymentId": t.PaymentID, "price": json.Number(str(in, "price")), "currency": "TRY", "conversationId": str(in, "conversationId"), "hostReference": "host-" + t.PaymentID})
	}
}

func decEqual(a, b string) bool {
	return strings.TrimRight(strings.TrimRight(a, "0"), ".") == strings.TrimRight(strings.TrimRight(b, "0"), ".")
}

func (f *Iyzico) paymentDetails(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.byPaymentID(r.URL.Query().Get("paymentId"))
	if t == nil {
		f.reject(w, 400, "10", "odeme bulunamadi")
		return
	}
	st := "NOT_REFUNDED"
	if t.Refunded {
		st = "FULL"
	}
	f.ok(w, map[string]any{"paymentId": t.PaymentID, "paymentStatus": "SUCCESS", "paymentRefundStatus": st})
}

// daily is the daily transaction report, two items per page.
func (f *Iyzico) daily(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	date := r.URL.Query().Get("transactionDate")
	var all []map[string]any
	tokens := make([]string, 0, len(f.txns))
	for k := range f.txns {
		tokens = append(tokens, k)
	}
	sort.Strings(tokens) // a stable report: pages must not reshuffle between calls
	for _, tok := range tokens {
		t := f.txns[tok]
		if t.Status != "SUCCESS" || t.Date != date {
			continue
		}
		gross := parseMicro(t.Price)
		fee := parseMicro(t.IyziFee) + parseMicro(t.IyziRate)
		all = append(all, map[string]any{"transactionType": "PAYMENT", "transactionStatus": 1, "paymentId": t.PaymentID, "paymentTxId": "tx" + t.PaymentID,
			"price": json.Number(t.Price), "paidPrice": json.Number(t.Price), "iyzicoFee": json.Number(t.IyziFee), "iyzicoCommission": json.Number(t.IyziRate),
			"merchantPayoutAmount": json.Number(fmt.Sprintf("%.4f", float64(gross-fee)/1e6))})
		if t.Refunded {
			all = append(all, map[string]any{"transactionType": "REFUND", "transactionStatus": 1, "paymentId": t.PaymentID, "paymentTxId": "tx" + t.PaymentID,
				"price": json.Number(t.Price), "paidPrice": json.Number(t.Price), "iyzicoFee": json.Number("0"), "iyzicoCommission": json.Number("0"),
				"merchantPayoutAmount": json.Number(fmt.Sprintf("%.4f", float64(gross-fee)/1e6))})
		}
	}
	page := 1
	_, _ = fmt.Sscan(r.URL.Query().Get("page"), &page)
	const size = 2
	pages := (len(all) + size - 1) / size
	lo, hi := (page-1)*size, page*size
	if lo > len(all) {
		lo = len(all)
	}
	if hi > len(all) {
		hi = len(all)
	}
	f.ok(w, map[string]any{"transactions": all[lo:hi], "currentPage": page, "totalPageCount": pages})
}

func parseMicro(s string) int64 {
	whole, frac, _ := strings.Cut(s, ".")
	frac = (frac + "000000")[:6]
	var w, fr int64
	_, _ = fmt.Sscan(whole, &w)
	_, _ = fmt.Sscan(frac, &fr)
	return w*1_000_000 + fr
}

// RewriteClient returns an HTTP client whose transport sends every request to base.
func RewriteClient(base string) *http.Client {
	u, _ := url.Parse(base)
	return &http.Client{Transport: rewrite{u}}
}

type rewrite struct{ to *url.URL }

func (t rewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	c.URL.Scheme, c.URL.Host = t.to.Scheme, t.to.Host
	c.Host = t.to.Host
	return http.DefaultTransport.RoundTrip(c)
}
