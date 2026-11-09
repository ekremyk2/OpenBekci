// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"testing"

	"github.com/openbekci/openbekci/sdk"
)

func TestRefCodePeriodMustStandAlone(t *testing.T) {
	if _, _, ok := ParseRefCode("12026-03"); ok {
		t.Error("a period glued to the unit was accepted")
	}
	if u, p, ok := ParseRefCode("A-12-2026-03"); !ok || u != "A-12" || p != "2026-03" {
		t.Errorf("A-12-2026-03: %q %q %v", u, p, ok)
	}
}

func TestBalancesThatChainBothWaysAreNotGuessed(t *testing.T) {
	b := func(n int64) *int64 { return &n }
	cancelling := []sdk.StatementLine{{Amount: 50, Balance: b(100)}, {Amount: -50, Balance: b(50)}}
	if o, c := balances(cancelling); o != nil || c != nil {
		t.Errorf("an ambiguous order was guessed: %v %v", o, c)
	}
	if o, c := balances([]sdk.StatementLine{{Amount: 50, Balance: b(100)}}); o == nil || *o != 50 || *c != 100 {
		t.Errorf("one line: %v %v", o, c)
	}
}

func TestParseMoneyRefusesOverflow(t *testing.T) {
	if _, err := parseMoney("99999999999999999999,00"); err == nil {
		t.Error("an amount beyond int64 was accepted")
	}
}

func TestCallbackBaseMustBeHTTPSOrLocal(t *testing.T) {
	for raw, want := range map[string]bool{"https://a.example": true, "http://localhost:8080": true, "http://localhost.evil.test": false, "http://a.example": false} {
		if got := validCallbackBase(raw); got != want {
			t.Errorf("%s: %v", raw, got)
		}
	}
}

// Two sites on the organization's account: the payout of the day names the tokens of both, so the core can split it.
func TestSettlementsNameTheTokensOfTheirPayments(t *testing.T) {
	pr, fake, ctx := newIyzico(t, orgIyzico(nil))
	var tokens []string
	for _, site := range []sdk.ID{tSite, "55555555-5555-5555-5555-555555555555"} {
		req := checkoutReq()
		req.Target.SiteID, req.Reference = site, "chk-"+string(site)
		co, err := pr.CreateCheckout(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		fake.Pay(co.ProviderTxnID, "2026-03-05", "0.25", "2.25")
		if _, err := pr.Status(ctx, sdk.ProviderRef{ProviderTxnID: co.ProviderTxnID}); err != nil { // the retrieve stores the paymentId
			t.Fatal(err)
		}
		tokens = append(tokens, co.ProviderTxnID)
	}
	got, err := pr.Settlements(ctx, sdk.SettlementRequest{From: "2026-03-05", To: "2026-03-05"})
	if err != nil || len(got) != 1 {
		t.Fatalf("settlements %+v %v", got, err)
	}
	if len(got[0].ProviderTxnIDs) != 2 {
		t.Fatalf("ProviderTxnIDs = %v, want the tokens %v", got[0].ProviderTxnIDs, tokens)
	}
	for _, tok := range tokens {
		found := false
		for _, g := range got[0].ProviderTxnIDs {
			found = found || g == tok
		}
		if !found {
			t.Errorf("token %s missing from %v", tok, got[0].ProviderTxnIDs)
		}
	}
}
