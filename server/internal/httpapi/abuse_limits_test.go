package httpapi

import (
	"fmt"
	"testing"
)

// M3: unauthenticated invitation endpoints are rate limited per address, and a
// join code honours max_uses.
func TestIntegrationInvitationEndpointsAreRateLimited(t *testing.T) {
	p := newPB(t)
	limited := false
	for i := 0; i < 90 && !limited; i++ {
		r := p.call("GET", "/v1/invitations/"+fmt.Sprintf("x%d", i), "", "")
		limited = r.Code == 429
	}
	if !limited {
		t.Fatal("invitation preview is not rate limited")
	}
}

func TestIntegrationJoinCodeHonoursMaxUses(t *testing.T) {
	p := newPB(t)
	f := p.w.NewSite(t, p.org)
	jc := p.must(p.call("POST", p.base()+"/sites/"+f.Site.String()+"/join-code:rotate", "", p.adm), 200).Body
	code := str(jc, "code")
	tok := p.w.Token(t, p.w.Actor("no_membership"))
	body := fmt.Sprintf(`{"unit_id":%q,"claimed_role":"owner"}`, f.Unit)
	p.w.Exec(t, `UPDATE invitation SET max_uses = 1, uses = 1 WHERE org_id = $1 AND code = $2`, p.org.ID, code)
	p.must(p.call("POST", "/v1/join/"+code+"/claims", body, tok), 404)
	p.w.Exec(t, `UPDATE invitation SET max_uses = 2 WHERE org_id = $1 AND code = $2`, p.org.ID, code)
	p.must(p.call("POST", "/v1/join/"+code+"/claims", body, tok), 201)
}
