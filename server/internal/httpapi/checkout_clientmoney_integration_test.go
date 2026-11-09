package httpapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/openbekci/openbekci/sdk"
)

// Client money: the organization collects centrally, passes the money on with a payout, and cannot switch the
// merchant of record while money is outstanding.
func TestIntegrationClientMoneyPayoutAndMerchantChangeGuard(t *testing.T) {
	c := newCoEnv(t)
	c.enablePack()
	orgPath := c.base()
	sitePath := orgPath + "/sites/" + c.f.Site.String() + "/settings"
	clearingURL := orgPath + "/payment-clearing?site_id=" + c.f.Site.String()
	patchOrg := func(mor string) resp {
		return c.call("PATCH", orgPath, `{"settings":{"payments.merchant_of_record":"`+mor+`"}}`, c.manager, "If-Match", fmt.Sprintf(`"%d"`, c.w.OrgVersion(t, c.w.OrgA)))
	}
	siteVersion := func() string {
		return fmt.Sprintf(`"%d"`, num(c.must(c.call("GET", sitePath, "", c.manager), 200).Body, "version"))
	}
	payout := func(token, body string) resp {
		return c.call("POST", orgPath+"/payment-clearing:payout", body, token, "Idempotency-Key", "po-"+fmt.Sprint(time.Now().UnixNano()))
	}
	amount := func(a int) string {
		return fmt.Sprintf(`{"site_id":"%s","amount":{"amount":%d,"currency":"TRY"},"reference":"EFT-1"}`, c.f.Site, a)
	}
	owed := func(r resp, field string) float64 { return r.Body[field].(map[string]any)["amount"].(float64) }

	c.must(patchOrg("organization"), 200)
	co := c.must(c.create(c.tenant, c.side("tenant", "")), 201)
	if rec := c.paid(str(co.Body, "id"), 20000, 600); rec.Code != 200 {
		t.Fatalf("webhook: %d", rec.Code)
	}
	today := time.Now().UTC().Format("2006-01-02")

	// money on its way (clearing): the merchant cannot change
	blocked := c.call("PATCH", orgPath, `{"settings":{"payments.merchant_of_record":"site"}}`, c.manager, "If-Match", fmt.Sprintf(`"%d"`, c.w.OrgVersion(t, c.w.OrgA)))
	c.problemCode(blocked, 409, "merchant_change_blocked")
	errs := blocked.Body["errors"].([]any)
	bal := errs[0].(map[string]any)["params"].(map[string]any)["balances"].([]any)
	if len(bal) != 1 || bal[0].(map[string]any)["site_id"] != c.f.Site.String() || bal[0].(map[string]any)["balance"] != 19400.0 {
		t.Fatalf("balances in params = %s", blocked.Raw)
	}
	c.problemCode(c.call("PATCH", sitePath, `{"merchant_of_record":"site"}`, c.manager, "If-Match", siteVersion()), 409, "merchant_change_blocked")

	// settle into client money; a payout needs a balance
	if n, err := c.e.deps.Packs.SyncSettlements(context.Background(), c.org.ID, "example", "example", sdk.ID(c.f.Site.String()),
		sdk.SettlementRequest{From: today, To: today, MerchantOfRecord: sdk.MerchantOrganization}); err != nil || n != 1 {
		t.Fatalf("SyncSettlements = %d, %v", n, err)
	}
	c.problemCode(c.call("PATCH", orgPath, `{"settings":{"payments.merchant_of_record":"site"}}`, c.manager, "If-Match", fmt.Sprintf(`"%d"`, c.w.OrgVersion(t, c.w.OrgA))), 409, "merchant_change_blocked")

	c.must(payout(c.tenant, amount(100)), 403)
	c.must(payout(c.manager, `{"site_id":"`+c.f.Site.String()+`","amount":{"amount":100,"currency":"TRY"}}`), 422)
	c.must(payout(c.manager, amount(0)), 422)
	c.must(payout(c.manager, amount(19401)), 422)
	c.must(payout(c.manager, fmt.Sprintf(`{"site_id":"%s","amount":{"amount":100,"currency":"EUR"},"reference":"x"}`, c.f.Site)), 422)

	r := c.must(payout(c.siteMgr, amount(5000)), 201)
	if owed(r, "client_money_owed") != 14400 || owed(r, "client_money_paid_out") != 5000 {
		t.Fatalf("clearing after a partial payout = %s", r.Raw)
	}
	if got := c.must(c.call("GET", clearingURL, "", c.siteMgr), 200); owed(got, "client_money_owed") != 14400 || owed(got, "client_money_paid_out") != 5000 {
		t.Fatalf("clearing view = %s", got.Raw)
	}
	// the site's bank received it, the books balance
	if n := c.qInt(`SELECT COALESCE(SUM(l.debit - l.credit), 0) FROM journal_line l JOIN account a ON a.id = l.account_id AND a.org_id = l.org_id WHERE l.org_id = $1 AND a.site_id = $2 AND a.code = '1100'`, c.org.ID, c.f.Site); n != 5000 {
		t.Errorf("bank = %d, want 5000", n)
	}
	if n := c.qInt(`SELECT count(*) FROM audit_event WHERE org_id = $1 AND action = 'payment.client_money_payout' AND actor_user_id IS NOT NULL`, c.org.ID); n != 1 {
		t.Errorf("payout audit events = %d", n)
	}
	c.must(payout(c.manager, amount(14401)), 422)
	r = c.must(payout(c.manager, amount(14400)), 201)
	if owed(r, "client_money_owed") != 0 {
		t.Fatalf("clearing after the full payout = %s", r.Raw)
	}
	// nothing outstanding: now the organization may collect on the site's behalf no more
	c.must(patchOrg("site"), 200)
	c.invariants()
}
