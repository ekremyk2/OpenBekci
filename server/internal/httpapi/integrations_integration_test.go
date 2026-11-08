package httpapi

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/integrations"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// reauth opens the re-authentication window on the org admin's sessions (what
// POST /v1/auth/reauth does).
func (p *pb) reauth() {
	p.t.Helper()
	p.w.Exec(p.t, `UPDATE session SET reauth_until = now() + interval '10 minutes' WHERE user_id = $1`, p.w.Actor("org_admin").UserID)
}

func (p *pb) forgetReauth() {
	p.t.Helper()
	p.w.Exec(p.t, `UPDATE session SET reauth_until = NULL WHERE user_id = $1`, p.w.Actor("org_admin").UserID)
}

func (p *pb) keysURL(more string) string { return "/v1/orgs/" + p.org.ID.String() + "/api-keys" + more }

// newKey creates a key as the org admin (re-authenticated) and returns its
// resource and token.
func (p *pb) newKey(name, scopes string, extra string) (map[string]any, string) {
	p.t.Helper()
	p.reauth()
	r := p.must(p.call("POST", p.keysURL(""), fmt.Sprintf(`{"name":%q,"scopes":%s%s}`, name, scopes, extra), p.adm), 201)
	key, _ := r.Body["key"].(map[string]any)
	secret := str(r.Body, "secret")
	if key == nil || !strings.HasPrefix(secret, "obk_"+str(key, "prefix")+"_") {
		p.t.Fatalf("created key = %s", r.Raw)
	}
	return key, secret
}

func (p *pb) problemCode(r resp, status int, code string) {
	p.t.Helper()
	if r.Code != status || str(r.Body, "code") != code {
		p.t.Fatalf("got %d %s, want %d %s: %s", r.Code, str(r.Body, "code"), status, code, r.Raw)
	}
}

func TestIntegrationAPIKeyLifecycle(t *testing.T) {
	p := newPB(t)
	auditURL := "/v1/orgs/" + p.org.ID.String() + "/audit"

	// creating a key needs a recent re-authentication
	p.problemCode(p.call("POST", p.keysURL(""), `{"name":"ci","scopes":["audit.read"]}`, p.adm), 403, "reauth_required")
	p.reauth()

	// scopes: unknown, held by no role, instance-only, apikeys.*, duplicate
	for scopes, want := range map[string]string{
		`["nope.nothing"]`:            "unknown_permission",
		`["documents.upload_own"]`:    "not_",
		`["platform.manage"]`:         "not_allowed",
		`["apikeys.manage"]`:          "not_allowed",
		`["webhooks.manage"]`:         "not_allowed", // a key never manages webhook endpoints
		`["audit.read","audit.read"]`: "duplicate",
	} {
		r := p.must(p.call("POST", p.keysURL(""), `{"name":"x","scopes":`+scopes+`}`, p.adm), 422)
		if !strings.Contains(r.Raw, want) {
			t.Errorf("scopes %s: %s, want %s", scopes, r.Raw, want)
		}
	}
	p.must(p.call("POST", p.keysURL(""), `{"name":"x","scopes":[]}`, p.adm), 422)
	p.must(p.call("POST", p.keysURL(""), `{"name":"x","scopes":["audit.read"],"expires_at":"2020-01-01T00:00:00Z"}`, p.adm), 422)

	key, secret := p.newKey("reporting", `["audit.read","org.manage"]`, "")
	keyID, prefix := str(key, "id"), str(key, "prefix")
	if str(key, "status") != "active" || key["last_used_at"] != nil || len(prefix) != 8 {
		t.Errorf("key = %+v", key)
	}

	// the list shows prefix and metadata, never a secret
	list := p.must(p.call("GET", p.keysURL(""), "", p.adm), 200)
	if strings.Contains(list.Raw, secret) || strings.Contains(list.Raw, "secret") || len(items(list)) != 1 || str(items(list)[0], "prefix") != prefix {
		t.Errorf("list = %s", list.Raw)
	}

	// the key authenticates, with exactly its scopes
	p.must(p.call("GET", auditURL, "", secret), 200)
	hook := `{"url":"https://93.184.216.34/h","events":["ticket.created"]}`
	p.problemCode(p.call("POST", "/v1/orgs/"+p.org.ID.String()+"/webhook-endpoints", hook, secret), 403, "insufficient_permission") // webhooks.manage cannot be a scope
	p.problemCode(p.call("GET", "/v1/orgs/"+p.org.ID.String()+"/webhook-endpoints", "", secret), 403, "insufficient_permission")  // webhooks.read was not granted
	orgURL := "/v1/orgs/" + p.org.ID.String()
	p.must(p.call("PATCH", orgURL, `{"name":"Renamed by a key"}`, secret, "If-Match", `"`+strconv.Itoa(p.w.OrgVersion(t, p.org))+`"`), 200)
	p.problemCode(p.call("GET", "/v1/me", "", secret), 403, "insufficient_permission")                                           // no account permissions
	p.problemCode(p.call("GET", "/v1/orgs/"+p.w.OrgB.ID.String()+"/audit", "", secret), 404, "not_found")                        // another organization
	p.problemCode(p.call("POST", p.keysURL(""), `{"name":"y","scopes":["audit.read"]}`, secret), 403, "insufficient_permission") // a key cannot manage keys
	p.reauth()
	p.problemCode(p.call("POST", p.keysURL(""), `{"name":"y","scopes":["audit.read"]}`, secret), 403, "insufficient_permission")

	// last_used_at is set, and written at most every five minutes
	first := str(items(p.must(p.call("GET", p.keysURL(""), "", p.adm), 200))[0], "last_used_at")
	if first == "" {
		t.Fatal("last_used_at was not set")
	}
	p.must(p.call("GET", auditURL, "", secret), 200)
	if again := str(items(p.must(p.call("GET", p.keysURL(""), "", p.adm), 200))[0], "last_used_at"); again != first {
		t.Errorf("last_used_at moved within the throttle window: %s then %s", first, again)
	}

	// the audit trail names the key as the actor, never the person who made it
	var rows []map[string]any
	rows = items(p.must(p.call("GET", auditURL+"?action=org.update", "", p.adm), 200))
	if len(rows) != 1 || str(rows[0], "actor_kind") != "api_key" || str(rows[0], "actor_key_id") != keyID || rows[0]["actor_user_id"] != nil {
		t.Errorf("org.update audit = %+v", rows)
	}
	rows = items(p.must(p.call("GET", auditURL+"?action=apikey.create", "", p.adm), 200))
	if len(rows) != 1 || str(rows[0], "actor_kind") != "user" || str(rows[0], "actor_user_id") == "" {
		t.Errorf("apikey.create audit = %+v", rows)
	}
	if strings.Contains(fmt.Sprint(rows), secret) {
		t.Error("the secret is in the audit trail")
	}

	// a wrong secret under a real prefix is an ordinary 401
	p.problemCode(p.call("GET", auditURL, "", "obk_"+prefix+"_"+strings.Repeat("x", 43)), 401, "unauthenticated")
	p.problemCode(p.call("GET", auditURL, "", "obk_garbage"), 401, "unauthenticated")

	// rotating: the old token stops, the new one works, the id stays
	p.forgetReauth()
	p.problemCode(p.call("POST", p.keysURL("/"+keyID+":rotate"), "", p.adm), 403, "reauth_required")
	p.reauth()
	rot := p.must(p.call("POST", p.keysURL("/"+keyID+":rotate"), "", p.adm), 200)
	newSecret := str(rot.Body, "secret")
	rk, _ := rot.Body["key"].(map[string]any)
	if str(rk, "id") != keyID || str(rk, "prefix") == prefix || !strings.HasPrefix(newSecret, "obk_"+str(rk, "prefix")+"_") || rk["rotated_at"] == nil {
		t.Errorf("rotated = %s", rot.Raw)
	}
	p.problemCode(p.call("GET", auditURL, "", secret), 401, "unauthenticated")
	p.must(p.call("GET", auditURL, "", newSecret), 200)

	// revoking: stable code, repeatable, no more rotation
	p.forgetReauth()
	p.problemCode(p.call("POST", p.keysURL("/"+keyID+":revoke"), "", p.adm), 403, "reauth_required")
	p.reauth()
	rev := p.must(p.call("POST", p.keysURL("/"+keyID+":revoke"), "", p.adm), 200)
	if str(rev.Body, "status") != "revoked" || rev.Body["revoked_at"] == nil {
		t.Errorf("revoked = %s", rev.Raw)
	}
	p.problemCode(p.call("GET", auditURL, "", newSecret), 401, "api_key_revoked")
	p.must(p.call("POST", p.keysURL("/"+keyID+":revoke"), "", p.adm), 200)
	p.problemCode(p.call("POST", p.keysURL("/"+keyID+":rotate"), "", p.adm), 409, "conflict")
	if n := len(items(p.must(p.call("GET", p.keysURL("?status=revoked"), "", p.adm), 200))); n != 1 {
		t.Errorf("revoked keys listed = %d", n)
	}
	if n := len(items(p.must(p.call("GET", p.keysURL("?status=active"), "", p.adm), 200))); n != 0 {
		t.Errorf("active keys listed = %d", n)
	}
	p.problemCode(p.call("POST", p.keysURL("/"+p.w.OrgB.ID.String()+":revoke"), "", p.adm), 404, "not_found")

	// expiry
	_, exp := p.newKey("short", `["audit.read"]`, `,"expires_at":"`+time.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`"`)
	p.must(p.call("GET", auditURL, "", exp), 200)
	p.w.Exec(t, `UPDATE api_key SET expires_at = now() - interval '1 minute' WHERE name = 'short'`)
	p.problemCode(p.call("GET", auditURL, "", exp), 401, "api_key_expired")
}

// A key never exceeds its creator: when the administrator loses the role, the
// key loses it with them, at once.
func TestIntegrationAPIKeyBoundByCreatorGrants(t *testing.T) {
	p := newPB(t)
	auditURL := "/v1/orgs/" + p.org.ID.String() + "/audit"
	_, secret := p.newKey("bound", `["audit.read"]`, "")
	p.must(p.call("GET", auditURL, "", secret), 200)

	p.w.Exec(t, `UPDATE membership SET valid_to = now() - interval '1 second' WHERE org_id = $1 AND person_id = $2`, p.org.ID, p.w.Actor("org_admin").PersonID)
	if r := p.call("GET", auditURL, "", secret); r.Code == 200 {
		t.Fatalf("the key still works after its creator lost the role: %s", r.Raw)
	}

	// a creator who is no longer active takes the key down too
	p2 := newPB(t)
	_, secret2 := p2.newKey("bound2", `["audit.read"]`, "")
	p2.w.Exec(t, `UPDATE "user" SET status = 'disabled' WHERE id = $1`, p2.w.Actor("org_admin").UserID)
	p2.problemCode(p2.call("GET", "/v1/orgs/"+p2.org.ID.String()+"/audit", "", secret2), 401, "unauthenticated")
}

// A site-restricted key reaches that site only.
func TestIntegrationAPIKeySiteRestriction(t *testing.T) {
	p := newPB(t)
	a, b := p.w.NewSite(t, p.org), p.w.NewSite(t, p.org)
	_, secret := p.newKey("site a", `["sites.read"]`, fmt.Sprintf(`,"site_id":%q`, a.Site))
	site := func(s string) string { return "/v1/orgs/" + p.org.ID.String() + "/sites/" + s }
	p.must(p.call("GET", site(a.Site.String()), "", secret), 200)
	if r := p.call("GET", site(b.Site.String()), "", secret); r.Code != 403 && r.Code != 404 {
		t.Errorf("another site = %d %s", r.Code, r.Raw)
	}
	// an unrestricted key reaches both
	_, all := p.newKey("all sites", `["sites.read"]`, "")
	p.must(p.call("GET", site(a.Site.String()), "", all), 200)
	p.must(p.call("GET", site(b.Site.String()), "", all), 200)
	// the site must be the organization's own
	p.reauth()
	p.must(p.call("POST", p.keysURL(""), fmt.Sprintf(`{"name":"z","scopes":["sites.read"],"site_id":%q}`, p.w.NewSite(t, p.w.OrgB).Site), p.adm), 422)
}

// The per-key rate limit answers 429 with a Retry-After.
func TestIntegrationAPIKeyRateLimit(t *testing.T) {
	p := newPB(t)
	_, secret := p.newKey("limited", `["audit.read"]`, "")
	svc, err := integrations.NewService(p.e.db, integrations.Config{Registry: p.e.deps.Authz.Registry(), KeyRatePerMinute: 6})
	if err != nil {
		t.Fatal(err)
	}
	var limited *problem.Error
	for i := 0; i < 15 && limited == nil; i++ {
		_, found, err := svc.AuthenticateAPIKey(context.Background(), secret)
		if !found {
			t.Fatal("key not found")
		}
		if err != nil {
			var pe *problem.Error
			ok := errors.As(err, &pe)
			if !ok {
				t.Fatalf("error = %v", err)
			}
			limited = pe
		}
	}
	if limited == nil || limited.Status != 429 || limited.Code != problem.CodeRateLimited || limited.RetryAfter <= 0 {
		t.Fatalf("limited = %+v", limited)
	}
}
