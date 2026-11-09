package httpapi

import (
	"strconv"
	"strings"
	"testing"

	"github.com/openbekci/openbekci/server/internal/platform/crypto"
)

// forgeKey inserts a key row directly (as a database edit or an older version
// could leave one) and returns its token.
func (p *pb) forgeKey(prefix, scopes string) string {
	p.t.Helper()
	secret := strings.Repeat("s", 43)
	p.w.Exec(p.t, `INSERT INTO api_key (id, org_id, name, prefix, secret_hash, scopes, created_by) VALUES (gen_random_uuid(), $1, 'forged', $2, $3, `+scopes+`, $4)`,
		p.org.ID, prefix, crypto.HashToken(secret), p.w.Actor("org_admin").UserID)
	return "obk_" + prefix + "_" + secret
}

// A key never manages webhook endpoints, even when its row lists the scope.
func TestIntegrationKeyCannotManageWebhooks(t *testing.T) {
	p := newPB(t)
	tok := p.forgeKey("forge001", `ARRAY['webhooks.manage','webhooks.read']`)
	hooks := "/v1/orgs/" + p.org.ID.String() + "/webhook-endpoints"
	p.must(p.call("GET", hooks, "", tok), 200) // reading is allowed
	p.problemCode(p.call("POST", hooks, `{"url":"https://93.184.216.34/h","events":["ticket.created"]}`, tok), 403, "insufficient_permission")
}

// A key reaches only operations that name a permission: the ones that need
// nothing but a signed-in person (sessions, password, re-authentication,
// instance policies) belong to the creator.
func TestIntegrationKeyOnlyCallsPermissionedOperations(t *testing.T) {
	p := newPB(t)
	_, secret := p.newKey("narrow", `["audit.read"]`, "")
	for _, c := range []struct{ method, path, body string }{
		{"DELETE", "/v1/auth/session", ""},
		{"POST", "/v1/auth/reauth", `{"password":"whatever-it-is-123"}`},
		{"POST", "/v1/auth/password", `{"current_password":"a","new_password":"bbbbbbbbbbbb"}`},
		{"GET", "/v1/instance", ""},
		{"GET", "/v1/me", ""},
	} {
		if r := p.call(c.method, c.path, c.body, secret); r.Code != 403 {
			t.Errorf("%s %s with a key = %d, want 403: %s", c.method, c.path, r.Code, r.Raw)
		}
	}
	// the permissioned operation still works
	p.must(p.call("GET", "/v1/orgs/"+p.org.ID.String()+"/audit", "", secret), 200)
}

// Idempotency keys are scoped to the key, not to the creator behind it: a key
// does not replay a response stored for the administrator's own request.
func TestIntegrationKeyIdempotencyIsNotSharedWithCreator(t *testing.T) {
	p := newPB(t)
	_, secret := p.newKey("idem", `["org.manage"]`, "")
	org := "/v1/orgs/" + p.org.ID.String()
	ifm := `"` + strconv.Itoa(p.w.OrgVersion(t, p.org)) + `"`
	body := `{"name":"Renamed once"}`
	p.must(p.call("PATCH", org, body, p.adm, "If-Match", ifm, "Idempotency-Key", "shared-key-0001"), 200)
	// the same request and key from the administrator replays
	p.must(p.call("PATCH", org, body, p.adm, "If-Match", ifm, "Idempotency-Key", "shared-key-0001"), 200)
	// from the key it is a new request: the stale If-Match is judged, not a stored answer replayed
	if r := p.call("PATCH", org, body, secret, "If-Match", ifm, "Idempotency-Key", "shared-key-0001"); r.Code == 200 {
		t.Fatalf("the key replayed the administrator's stored response: %s", r.Raw)
	}
}
