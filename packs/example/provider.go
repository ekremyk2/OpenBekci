// SPDX-License-Identifier: AGPL-3.0-or-later

package example

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/openbekci/openbekci/sdk"
)

// SignatureHeader carries the hex HMAC-SHA256 of the webhook body, keyed with
// the organization's example_webhook_secret setting.
const SignatureHeader = "X-Example-Signature"

// Sign computes the signature the example provider puts on a webhook.
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func verify(secret string, body []byte, sig string) bool {
	return hmac.Equal([]byte(Sign(secret, body)), []byte(sig))
}

// provider is the fake payment provider. It keeps a mirror of its
// transactions in pack_example_txn so that Status and Refund have something
// to answer from.
type provider struct{ p *Pack }

var _ sdk.PaymentProvider = (*provider)(nil)

func (*provider) ID() string { return ID }

func (pr *provider) CreateCheckout(ctx context.Context, req sdk.CheckoutRequest) (sdk.Checkout, error) {
	txn := "ex_" + req.Reference
	err := pr.p.svc.DB().InTx(ctx, func(tx sdk.Tx) error {
		org, _ := sdk.OrgFrom(ctx)
		_, err := tx.Exec(ctx, `INSERT INTO pack_example_txn (org_id, txn, state, gross, currency) VALUES ($1::uuid, $2, 'pending', $3, $4) ON CONFLICT (org_id, txn) DO NOTHING`,
			string(org), txn, req.Gross.Amount, req.Gross.Currency)
		return err
	})
	if err != nil {
		return sdk.Checkout{}, err
	}
	exp := time.Now().Add(time.Hour).UTC()
	return sdk.Checkout{ProviderTxnID: txn, CheckoutURL: "https://pay.example.test/checkout/" + txn, ExpiresAt: &exp}, nil
}

type txnRow struct {
	state      string
	gross, fee int64
	currency   string
}

func (pr *provider) load(ctx context.Context, txn string) (txnRow, error) {
	var r txnRow
	err := pr.p.svc.DB().InTx(ctx, func(tx sdk.Tx) error {
		return tx.QueryRow(ctx, `SELECT state, gross, fee, currency FROM pack_example_txn WHERE txn = $1`, txn).Scan(&r.state, &r.gross, &r.fee, &r.currency)
	})
	if err == sdk.ErrNoRows {
		return r, fmt.Errorf("transaction %q: %w", txn, sdk.ErrNotFound)
	}
	return r, err
}

func (pr *provider) Status(ctx context.Context, ref sdk.ProviderRef) (sdk.PaymentStatus, error) {
	r, err := pr.load(ctx, ref.ProviderTxnID)
	if err != nil {
		return sdk.PaymentStatus{}, err
	}
	st := sdk.PaymentStatus{State: sdk.PaymentState(r.state)}
	if r.state == "paid" || r.state == "refunded" {
		st.Gross = sdk.Money{Amount: r.gross, Currency: r.currency}
		st.Fee = sdk.Money{Amount: r.fee, Currency: r.currency}
		st.Net = sdk.Money{Amount: r.gross - r.fee, Currency: r.currency}
	}
	return st, nil
}

// wire is the example provider's webhook body.
type wire struct {
	Event      string `json:"event"` // paid, failed, refunded, settled
	Txn        string `json:"txn"`
	RefundID   string `json:"refund_id"`
	Site       string `json:"site"`
	Unit       string `json:"unit"`
	DebtorKind string `json:"debtor_kind"`
	Gross      int64  `json:"gross"`
	Fee        int64  `json:"fee"`
	Currency   string `json:"currency"`
	Reference  string `json:"reference"`
	OccurredOn string `json:"occurred_on"`
	Settlement *struct {
		Ref       string `json:"ref"`
		SettledOn string `json:"settled_on"`
		Net       int64  `json:"net"`
	} `json:"settlement"`
}

func (pr *provider) HandleWebhook(ctx context.Context, req sdk.WebhookRequest) (sdk.WebhookResult, error) {
	secret, err := sdk.SettingString(ctx, pr.p.svc.Settings(), sdk.OrgScope(), "example_webhook_secret")
	if err != nil {
		return sdk.WebhookResult{}, err
	}
	// verify first: nothing is parsed or stored before the signature checks out
	if secret == "" || !verify(secret, req.Body, req.Header.Get(SignatureHeader)) {
		return sdk.WebhookResult{}, fmt.Errorf("webhook: %w", sdk.ErrInvalidSignature)
	}
	var w wire
	if err := json.Unmarshal(req.Body, &w); err != nil {
		return sdk.WebhookResult{}, fmt.Errorf("webhook body: %w", sdk.ErrRejected)
	}
	money := func(n int64) sdk.Money { return sdk.Money{Amount: n, Currency: w.Currency} }
	target := sdk.PaymentTarget{SiteID: sdk.ID(w.Site), UnitID: sdk.ID(w.Unit), DebtorKind: w.DebtorKind}
	occurred, _ := time.Parse("2006-01-02", w.OccurredOn)
	ev := sdk.PaymentEvent{ProviderTxnID: w.Txn, RefundID: w.RefundID, Target: target, Gross: money(w.Gross), Fee: money(w.Fee), Net: money(w.Gross - w.Fee), OccurredAt: occurred, Reference: w.Reference}
	switch w.Event {
	case "paid":
		ev.Kind = sdk.EventKindPaid
		err = pr.mirror(ctx, w.Txn, "paid", w.Gross, w.Fee, w.Currency)
	case "failed":
		ev.Kind = sdk.EventKindFailed
		err = pr.mirror(ctx, w.Txn, "failed", w.Gross, w.Fee, w.Currency)
	case "refunded":
		ev.Kind = sdk.EventKindRefunded
		err = pr.mirror(ctx, w.Txn, "refunded", 0, 0, "")
	case "settled":
		if w.Settlement == nil {
			return sdk.WebhookResult{}, fmt.Errorf("settlement missing: %w", sdk.ErrRejected)
		}
		ev.Kind = sdk.EventKindSettled
		ev.Settlement = &sdk.Settlement{Ref: w.Settlement.Ref, SettledOn: w.Settlement.SettledOn, Net: money(w.Settlement.Net)}
	case "ping":
		return sdk.WebhookResult{Response: &sdk.HTTPResponse{Status: 200, Body: []byte("pong")}}, nil
	default:
		return sdk.WebhookResult{}, fmt.Errorf("unknown event %q: %w", w.Event, sdk.ErrRejected)
	}
	if err != nil {
		return sdk.WebhookResult{}, err
	}
	return sdk.WebhookResult{Events: []sdk.PaymentEvent{ev}, Response: &sdk.HTTPResponse{Status: 200, Body: []byte("ok")}}, nil
}

// mirror keeps pack_example_txn in step with what the provider reported.
func (pr *provider) mirror(ctx context.Context, txn, state string, gross, fee int64, currency string) error {
	org, _ := sdk.OrgFrom(ctx)
	return pr.p.svc.DB().InTx(ctx, func(tx sdk.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO pack_example_txn (org_id, txn, state, gross, fee, currency) VALUES ($1::uuid, $2, $3, $4, $5, COALESCE(NULLIF($6, ''), 'TRY'))
			ON CONFLICT (org_id, txn) DO UPDATE SET state = EXCLUDED.state,
			  gross = CASE WHEN EXCLUDED.gross > 0 THEN EXCLUDED.gross ELSE pack_example_txn.gross END,
			  fee = CASE WHEN EXCLUDED.gross > 0 THEN EXCLUDED.fee ELSE pack_example_txn.fee END`,
			string(org), txn, state, gross, fee, currency)
		return err
	})
}

func (pr *provider) Refund(ctx context.Context, req sdk.RefundRequest) (sdk.RefundResult, error) {
	r, err := pr.load(ctx, req.ProviderTxnID)
	if err != nil {
		return sdk.RefundResult{}, err
	}
	if r.state != "paid" && r.state != "refunded" {
		return sdk.RefundResult{}, fmt.Errorf("transaction is %s, not paid: %w", r.state, sdk.ErrRejected)
	}
	if req.Amount.Amount > r.gross {
		return sdk.RefundResult{}, fmt.Errorf("refund of %d exceeds the %d paid: %w", req.Amount.Amount, r.gross, sdk.ErrRejected)
	}
	if err := pr.mirror(ctx, req.ProviderTxnID, "refunded", 0, 0, ""); err != nil {
		return sdk.RefundResult{}, err
	}
	// the same RefundRef always yields the same provider refund id (idempotent)
	return sdk.RefundResult{ProviderRefundID: "rf_" + req.RefundRef, Amount: sdk.Money{Amount: r.gross, Currency: r.currency}}, nil
}

func (pr *provider) Settlements(ctx context.Context, req sdk.SettlementRequest) ([]sdk.Settlement, error) {
	var net, gross, fee int64
	var ids []string
	var cur string
	err := pr.p.svc.DB().InTx(ctx, func(tx sdk.Tx) error {
		rows, err := tx.Query(ctx, `SELECT txn, gross, fee, currency FROM pack_example_txn WHERE state = 'paid' AND created_at::date BETWEEN $1::date AND $2::date ORDER BY txn`, req.From, req.To)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t, c string
			var g, f int64
			if err := rows.Scan(&t, &g, &f, &c); err != nil {
				return err
			}
			ids, gross, fee, net, cur = append(ids, t), gross+g, fee+f, net+g-f, c
		}
		return rows.Err()
	})
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	m := func(n int64) sdk.Money { return sdk.Money{Amount: n, Currency: cur} }
	return []sdk.Settlement{{Ref: "st_" + req.From + "_" + req.To, SettledOn: req.To, Gross: m(gross), Fee: m(fee), Net: m(net), ProviderTxnIDs: ids}}, nil
}
