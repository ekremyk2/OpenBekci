package httpapi

import (
	"fmt"
	"testing"
)

// Guard operations need a guard session; resetting a PIN ends the guard's live sessions.
func TestIntegrationCommunityGuardOperationsNeedASession(t *testing.T) {
	c := newCommEnv(t)
	site := c.f.Site.String()
	// no X-Guard-Session: 401 guard_session_required on every guard operation
	for _, r := range []struct{ method, path, body string }{
		{"GET", "/gate/console?site_id=" + site, ""},
		{"GET", "/gate/visitors?site_id=" + site, ""},
		{"GET", "/gate/deliveries?site_id=" + site, ""},
		{"GET", "/gate/plates?site_id=" + site + "&q=34TST", ""},
		{"POST", "/gate/visitors:scan", `{"token":"obk1.0123456789012345678901234567890123456789012"}`},
		{"POST", "/gate/visitors:walk-in", fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"X"}`, site, c.f.Unit)},
		{"POST", "/gate/deliveries", fmt.Sprintf(`{"site_id":%q,"unit_id":%q}`, site, c.f.Unit)},
	} {
		// the helper adds a session for the guard's login unless a header is passed; an empty one is dropped, so go through the raw request
		rec := c.rawReq(c.guard, r.method, c.path(r.path), r.body)
		c.problemCode(rec, 401, "guard_session_required")
	}
	// the exceptions: the booth picker and the PIN switch itself
	c.want(c.rawReq(c.guard, "GET", c.path("/gate/guards?site_id="+site), ""), 200)

	// a PIN reset ends the live session: guard_session_expired, the login stays valid
	session := c.ensureGuardSession()
	c.want(c.req(c.guard, "GET", c.path("/gate/console?site_id="+site), ""), 200)
	g := c.w.Actor("guard")
	c.want(c.req(c.manager, "PUT", c.path("/persons/"+g.PersonID.String()+"/gate-pin"), `{"pin":"9753"}`), 204)
	c.problemCode(c.req(c.guard, "GET", c.path("/gate/console?site_id="+site), "", "X-Guard-Session", session), 401, "guard_session_expired")
	c.problemCode(c.req(c.guard, "POST", c.path("/gate/visitors:scan"), `{"token":"obk1.0123456789012345678901234567890123456789012"}`, "X-Guard-Session", session), 401, "guard_session_expired")
	c.want(c.rawReq(c.guard, "GET", c.path("/gate/guards?site_id="+site), ""), 200)
}
