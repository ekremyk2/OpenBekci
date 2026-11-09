package httpapi

import (
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Matrix rows of the card checkout operations (release 0.4). The matrix environment compiles no pack in, so a
// permitted caller who starts a checkout or refunds a provider payment is stopped at "no provider": 409.

func (s c2Site) checkoutRow(w *authztest.World, t *testing.T) uuid.UUID {
	cid := id.New()
	w.Exec(t, `INSERT INTO checkout (id, org_id, site_id, unit_id, debtor_kind, provider, amount, currency, status, return_url, expires_at, provider_txn_id, checkout_url)
		VALUES ($1,$2,$3,$4,'owner','example',500,'TRY','pending','https://app.example.test/return', now() + interval '1 hour', $5, 'https://pay.example.test/x')`,
		cid, s.f.Org.ID, s.f.Site, s.f.Unit, "ex_"+cid.String())
	return cid
}

func (s c2Site) providerPayment(w *authztest.World, t *testing.T) uuid.UUID {
	unit := s.f.Unit
	res, err := payments.NewService(w.DB, nil, nil).RecordProvider(sysCtx(s.f.Org), s.f.Org.ID, payments.ProviderInput{Provider: "example", ProviderTxnID: "ex_" + id.New().String(),
		SiteID: s.f.Site, Debtor: billing.Debtor{UnitID: &unit, Kind: "owner"}, Gross: money.New(500, "TRY"), Fee: money.New(10, "TRY")})
	if err != nil {
		t.Fatal(err)
	}
	return res.Payment.ID
}

func init() {
	pay := perms(ledger.PermPaymentsCheckout)
	read := perms(ledger.PermMoneyRead)
	siteGet := func(path string, ps []authz.Permission) mrow {
		return mrow{perms: ps, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			s := c2NewSite(w, t, org)
			return jsonReq("GET", orgPath(org, path+"?site_id="+s.f.Site.String()), "")
		}}
	}
	rows := map[string]mrow{
		"money.createCheckout": {perms: pay, status: 409, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			s := c2NewSite(w, t, org)
			return jsonReq("POST", orgPath(org, "/checkouts"), fmt.Sprintf(`{"unit_id":"%s","debtor_kind":"owner","amount":{"amount":500,"currency":"TRY"}}`, s.f.Unit), moneyIdem()...)
		}},
		"money.getCheckout": {perms: pay, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			s := c2NewSite(w, t, org)
			return jsonReq("GET", orgPath(org, "/checkouts/"+s.checkoutRow(w, t).String()), "")
		}},
		"money.listMyCheckouts": {perms: pay, status: 200, build: mget("/v1/orgs/{org_id}/me/checkouts")},
		"money.listCheckouts":   siteGet("/checkouts", read),
		"money.refundPayment": {perms: perms(ledger.PermMoneyPost), status: 409, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			s := c2NewSite(w, t, org)
			return jsonReq("POST", orgPath(org, "/payments/"+s.providerPayment(w, t).String()+":refund"), `{"reason":"matrix"}`, append(moneyIdem(), "If-Match", `"1"`)...)
		}},
		// no client money is owed in the matrix world: an authorized caller gets the 422 of an amount above the balance
		"money.payoutClientMoney": {perms: perms(ledger.PermMoneyPost), status: 422, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			s := c2NewSite(w, t, org)
			return jsonReq("POST", orgPath(org, "/payment-clearing:payout"), fmt.Sprintf(`{"site_id":"%s","amount":{"amount":100,"currency":"TRY"},"reference":"matrix"}`, s.f.Site), moneyIdem()...)
		}},
		"money.listPaymentSettlements": siteGet("/payment-settlements", read),
		"money.getPaymentClearing":     siteGet("/payment-clearing", read),
	}
	for k, v := range rows {
		authzMatrixRows[k] = v
	}
}
