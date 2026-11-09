// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/openbekci/openbekci/sdk"
)

// The iyzico HTTP client. Everything here follows the public iyzico
// documentation (docs.iyzico.com); every assumption is listed in PAYMENTS.md.
// The client takes an *http.Client so that the in-process fake of package
// trtest, or a go-vcr recorder, can stand in for iyzico.

const (
	iyzicoProdBase    = "https://api.iyzipay.com"
	iyzicoSandboxBase = "https://sandbox-api.iyzipay.com"

	pathCFInit    = "/payment/iyzipos/checkoutform/initialize/auth/ecom"
	pathCFDetail  = "/payment/iyzipos/checkoutform/auth/ecom/detail"
	pathRefundV2  = "/v2/payment/refund"
	pathPayDetail = "/v2/reporting/payment/details"
	pathDailyTx   = "/v2/reporting/payment/transactions"
)

// iyzicoCreds are the credentials of one iyzico merchant account.
type iyzicoCreds struct {
	APIKey, SecretKey string
	Sandbox           bool
	CallbackBase      string
}

func (c iyzicoCreds) base() string {
	if c.Sandbox {
		return iyzicoSandboxBase
	}
	return iyzicoProdBase
}

// iyzicoClient is a signed JSON client for one merchant account.
type iyzicoClient struct {
	http  *http.Client
	creds iyzicoCreds
	// rnd returns the random key of a request (tests make it deterministic).
	rnd func() string
}

// authHeader builds the IYZWSv2 Authorization value: HMAC-SHA256 (hex) over
// randomKey + uriPath + body keyed with the secret key, then
// base64("apiKey:<key>&randomKey:<rnd>&signature:<hmac>").
func authHeader(apiKey, secretKey, randomKey, uriPath string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secretKey))
	m.Write([]byte(randomKey))
	m.Write([]byte(uriPath))
	m.Write(body)
	sig := hex.EncodeToString(m.Sum(nil))
	raw := "apiKey:" + apiKey + "&randomKey:" + randomKey + "&signature:" + sig
	return "IYZWSv2 " + base64.StdEncoding.EncodeToString([]byte(raw))
}

func randomKey() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return strconv.FormatInt(time.Now().UnixMilli(), 10) + hex.EncodeToString(b[:])
}

// iyzicoError is an iyzico business failure (status "failure").
type iyzicoError struct {
	HTTPStatus int
	Code       string
	Message    string
}

func (e *iyzicoError) Error() string {
	return fmt.Sprintf("iyzico: %s (code %s, http %d)", e.Message, e.Code, e.HTTPStatus)
}

// do sends one signed request. in is marshalled once and signed as sent; out
// receives the decoded body of a "success" answer. A failure answer is
// returned as *iyzicoError; transport and 5xx failures wrap sdk.ErrUnavailable
// and 401 and 403 wrap sdk.ErrCredentials. No error text ever contains the
// Authorization header or the keys.
func (c *iyzicoClient) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return fmt.Errorf("iyzico: encode request: %w", err)
		}
	}
	target := c.creds.base() + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("iyzico: build request: %w", err)
	}
	rnd := randomKey
	if c.rnd != nil {
		rnd = c.rnd
	}
	rk := rnd()
	// assumption: the query string is not part of the signed uri path
	req.Header.Set("Authorization", authHeader(c.creds.APIKey, c.creds.SecretKey, rk, path, body))
	req.Header.Set("x-iyzi-rnd", rk)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// url.Error would quote the URL, which holds no secret, but keep the message short anyway
		return fmt.Errorf("%w: iyzico request failed: %s", sdk.ErrUnavailable, errors.Unwrap(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("%w: iyzico: read response: %w", sdk.ErrUnavailable, err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: iyzico answered http %d", sdk.ErrCredentials, resp.StatusCode)
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: iyzico answered http %d", sdk.ErrUnavailable, resp.StatusCode)
	}
	var head struct {
		Status       string `json:"status"`
		ErrorCode    string `json:"errorCode"`
		ErrorMessage string `json:"errorMessage"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return fmt.Errorf("%w: iyzico: unreadable answer (http %d)", sdk.ErrUnavailable, resp.StatusCode)
	}
	if head.Status != "success" {
		return &iyzicoError{HTTPStatus: resp.StatusCode, Code: head.ErrorCode, Message: head.ErrorMessage}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%w: iyzico: unreadable answer: %v", sdk.ErrUnavailable, err)
		}
	}
	return nil
}

// amount is a decimal money value in millionths of the currency unit; iyzico
// sends prices as numbers or as strings ("100.0", 100.5, "2.9835").
type amount int64

func (a *amount) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*a = 0
		return nil
	}
	v, err := parseDecimalMicro(s)
	if err != nil {
		return err
	}
	*a = amount(v)
	return nil
}

func parseDecimalMicro(s string) (int64, error) {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 6 {
		frac = frac[:6]
	}
	frac += strings.Repeat("0", 6-len(frac))
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a decimal amount", s)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a decimal amount", s)
	}
	v := w*1_000_000 + f
	if neg {
		v = -v
	}
	return v, nil
}

// minor rounds half up to kurus.
func (a amount) minor() int64 {
	if a < 0 {
		return -((-int64(a) + 5000) / 10000)
	}
	return (int64(a) + 5000) / 10000
}

// decimalString formats minor units as the decimal string iyzico takes ("100.00").
func decimalString(minor int64) string {
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	return fmt.Sprintf("%s%d.%02d", sign, minor/100, minor%100)
}

// The wire shapes of the Checkout Form API.

type iyzBuyer struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Surname             string `json:"surname"`
	IdentityNumber      string `json:"identityNumber"`
	Email               string `json:"email"`
	GSMNumber           string `json:"gsmNumber"`
	RegistrationAddress string `json:"registrationAddress"`
	IP                  string `json:"ip,omitempty"`
	City                string `json:"city"`
	Country             string `json:"country"`
}

type iyzAddress struct {
	ContactName string `json:"contactName"`
	City        string `json:"city"`
	Country     string `json:"country"`
	Address     string `json:"address"`
}

type iyzItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Category1 string `json:"category1"`
	ItemType  string `json:"itemType"`
	Price     string `json:"price"`
}

type cfInitRequest struct {
	Locale              string     `json:"locale"`
	ConversationID      string     `json:"conversationId"`
	Price               string     `json:"price"`
	PaidPrice           string     `json:"paidPrice"`
	Currency            string     `json:"currency"`
	BasketID            string     `json:"basketId"`
	PaymentGroup        string     `json:"paymentGroup"`
	CallbackURL         string     `json:"callbackUrl"`
	EnabledInstallments []int      `json:"enabledInstallments"`
	Buyer               iyzBuyer   `json:"buyer"`
	ShippingAddress     iyzAddress `json:"shippingAddress"`
	BillingAddress      iyzAddress `json:"billingAddress"`
	BasketItems         []iyzItem  `json:"basketItems"`
}

type cfInitResponse struct {
	Token          string `json:"token"`
	PaymentPageURL string `json:"paymentPageUrl"`
}

type cfDetailRequest struct {
	Locale         string `json:"locale"`
	ConversationID string `json:"conversationId,omitempty"`
	Token          string `json:"token"`
}

type iyzItemTx struct {
	PaymentTransactionID string `json:"paymentTransactionId"`
	ItemID               string `json:"itemId"`
	PaidPrice            amount `json:"paidPrice"`
	MerchantPayout       amount `json:"merchantPayoutAmount"`
}

type cfDetailResponse struct {
	PaymentStatus                string      `json:"paymentStatus"`
	PaymentID                    string      `json:"paymentId"`
	Currency                     string      `json:"currency"`
	BasketID                     string      `json:"basketId"`
	ConversationID               string      `json:"conversationId"`
	Token                        string      `json:"token"`
	Price                        amount      `json:"price"`
	PaidPrice                    amount      `json:"paidPrice"`
	IyziCommissionFee            amount      `json:"iyziCommissionFee"`
	IyziCommissionRateAmount     amount      `json:"iyziCommissionRateAmount"`
	MerchantCommissionRateAmount amount      `json:"merchantCommissionRateAmount"`
	ItemTransactions             []iyzItemTx `json:"itemTransactions"`
}

type refundV2Request struct {
	Locale         string `json:"locale"`
	ConversationID string `json:"conversationId"`
	PaymentID      string `json:"paymentId"`
	Price          string `json:"price"`
}

type refundResponse struct {
	PaymentID string `json:"paymentId"`
	Price     amount `json:"price"`
}

type payDetailResponse struct {
	PaymentID           string `json:"paymentId"`
	PaymentStatus       string `json:"paymentStatus"`
	PaymentRefundStatus string `json:"paymentRefundStatus"`
}

type dailyTx struct {
	TransactionType      string `json:"transactionType"`
	TransactionStatus    any    `json:"transactionStatus"`
	PaymentID            string `json:"paymentId"`
	PaymentTxID          string `json:"paymentTxId"`
	PaidPrice            amount `json:"paidPrice"`
	IyzicoFee            amount `json:"iyzicoFee"`
	IyzicoCommission     amount `json:"iyzicoCommission"`
	MerchantPayoutAmount amount `json:"merchantPayoutAmount"`
}

type dailyResponse struct {
	Transactions   []dailyTx `json:"transactions"`
	CurrentPage    int       `json:"currentPage"`
	TotalPageCount int       `json:"totalPageCount"`
}

// webhookSignature is the X-IYZ-SIGNATURE-V3 of an HPP (Checkout Form)
// notification: HMAC-SHA256 hex, keyed with the secret key, over
// secretKey + iyziEventType + iyziPaymentId + token + paymentConversationId + status.
func webhookSignature(secret, eventType, iyziPaymentID, token, conversationID, status string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(secret + eventType + iyziPaymentID + token + conversationID + status))
	return hex.EncodeToString(m.Sum(nil))
}
