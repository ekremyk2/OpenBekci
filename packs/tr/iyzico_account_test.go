// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"context"
	"errors"
	"testing"

	"github.com/openbekci/openbekci/packs/tr/trtest"
	"github.com/openbekci/openbekci/sdk"
)

// memAccounts is the in-memory account store of the unit tests.
type memAccounts map[string]account

func (m memAccounts) put(_ context.Context, token string, a account) error { m[token] = a; return nil }

func (m memAccounts) get(_ context.Context, token string) (account, bool, error) {
	a, ok := m[token]
	return a, ok, nil
}

// The merchant-of-record mode decides whose iyzico account every call uses, and
// Status, Refund and the webhook (which do not carry the mode) find the same
// account again through the checkout.
func TestMerchantOfRecordSelectsTheAccountEndToEnd(t *testing.T) {
	siteFake := trtest.NewIyzico("site-key", "site-secret")
	defer siteFake.Close()
	p := New(WithHTTPClient(siteFake.Client()))
	p.svc = stubServices{set: stubSettings{
		"org":                   orgIyzico(nil), // the organization's account: other keys, the fake would answer 401
		"site:" + string(tSite): {"tr_payments_iyzico_enabled": true, "tr_payments_iyzico_api_key": "site-key", "tr_payments_iyzico_secret_key": "site-secret"},
	}}
	p.accounts = memAccounts{}
	pr := &iyzico{p: p}
	ctx := sdk.WithOrg(context.Background(), tOrg)

	// organization mode: the organization's keys are used (and refused by this fake)
	if _, err := pr.CreateCheckout(ctx, checkoutReq()); !errors.Is(err, sdk.ErrCredentials) {
		t.Fatalf("organization mode used the wrong account: %v", err)
	}
	// site mode: the site's own account, end to end
	req := checkoutReq()
	req.MerchantOfRecord = sdk.MerchantSite
	co, err := pr.CreateCheckout(ctx, req)
	if err != nil {
		t.Fatalf("a site with its own iyzico account is checked out with it: %v", err)
	}
	if a := p.accounts.(memAccounts)[co.ProviderTxnID]; a.Site != tSite || a.Merchant != sdk.MerchantSite {
		t.Fatalf("the checkout's account was not remembered: %+v", a)
	}
	payNow(siteFake, co.ProviderTxnID)
	if st, err := pr.Status(ctx, sdk.ProviderRef{ProviderTxnID: co.ProviderTxnID}); err != nil || st.State != sdk.PaymentPaid {
		t.Fatalf("Status with a site-owned account: %+v %v", st, err)
	}
	// the webhook arrives without any query: the token finds the account whose secret verifies it
	res, err := pr.HandleWebhook(ctx, siteFake.Webhook(co.ProviderTxnID))
	if err != nil || len(res.Events) != 1 {
		t.Fatalf("webhook for a site-owned checkout: %+v %v", res, err)
	}
	if _, err := pr.Refund(ctx, sdk.RefundRequest{ProviderTxnID: co.ProviderTxnID, RefundRef: "r"}); err != nil {
		t.Fatalf("Refund with a site-owned account: %v", err)
	}
	// settlements: asked per site in site mode
	if _, err := pr.Settlements(ctx, sdk.SettlementRequest{From: "2026-02-03", To: "2026-02-03", Site: tSite, MerchantOfRecord: sdk.MerchantSite}); err != nil {
		t.Fatalf("settlements of the site account: %v", err)
	}
	if _, err := pr.Settlements(ctx, sdk.SettlementRequest{From: "2026-02-03", To: "2026-02-03", MerchantOfRecord: sdk.MerchantOrganization}); !errors.Is(err, sdk.ErrCredentials) {
		t.Fatalf("settlements of the organization account used the site's keys: %v", err)
	}
	// no site account configured: a site-mode checkout is refused, never silently paid into the organization's account
	p.svc = stubServices{set: stubSettings{"org": orgIyzico(nil)}}
	if _, err := pr.CreateCheckout(ctx, req); !errors.Is(err, sdk.ErrCredentials) {
		t.Fatalf("site mode without a site account: %v", err)
	}
}
