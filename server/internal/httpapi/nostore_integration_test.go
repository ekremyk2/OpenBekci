package httpapi

import (
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/identity"
)

// Tokens, session lists and the caller's identity must never be cached.
func TestIntegrationHTTPSensitiveResponsesAreNoStore(t *testing.T) {
	e := newIDEnv(t, false, false)
	e.user(identity.CreateUserParams{Email: "ns@example.org", Password: "correct horse battery"})

	check := func(name, method, path, body string, withCookie bool, headers ...string) {
		t.Helper()
		rec := e.do(method, path, body, withCookie, headers...)
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s (%d): Cache-Control = %q, want no-store", name, rec.Code, cc)
		}
	}
	creds := `{"method":"password","identifier":"ns@example.org","password":"correct horse battery"}`
	check("session login", "POST", "/v1/auth/session", creds, false)
	check("token", "POST", "/v1/auth/token", creds, false)
	check("failed login", "POST", "/v1/auth/session", `{"method":"password","identifier":"ns@example.org","password":"wrong wrong wrong"}`, false)
	check("refresh", "POST", "/v1/auth/token/refresh", `{"refresh_token":"nope"}`, false)
	check("me unauthenticated", "GET", "/v1/me", "", false)
	e.login("ns@example.org", "correct horse battery")
	check("me", "GET", "/v1/me", "", true)
	check("sessions", "GET", "/v1/me/sessions", "", true)
}
