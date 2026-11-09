package httpapi

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/identity"
)

var specPathParam = regexp.MustCompile(`\{[^}]+\}`)

// requires reports whether op lists the security scheme.
func requires(op *openapi3.Operation, scheme string) bool {
	if op.Security == nil {
		return false
	}
	for _, req := range *op.Security {
		if _, ok := req[scheme]; ok {
			return true
		}
	}
	return false
}

// mutating returns every operation of the spec with an unsafe method that
// accepts the given security scheme.
func mutating(t *testing.T, scheme string) map[string]*openapi3.Operation {
	t.Helper()
	out := map[string]*openapi3.Operation{}
	for path, item := range loadSpec(t).Paths.Map() {
		for method, op := range item.Operations() {
			if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
				continue
			}
			if requires(op, scheme) {
				out[method+" "+specPathParam.ReplaceAllString(path, uuid.NewString())] = op
			}
		}
	}
	return out
}

func allowsAnonymous(op *openapi3.Operation) bool {
	if op.Security == nil {
		return false
	}
	for _, req := range *op.Security {
		if len(req) == 0 {
			return true
		}
	}
	return false
}

func (e *idEnv) raw(method, path, body string, headers ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	r := httptest.NewRequest(method, path, rd)
	r.RemoteAddr = "203.0.113.9:4000"
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] != "" {
			r.Header.Set(headers[i], headers[i+1])
		}
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

// Every cookie-authenticated mutating operation of the spec refuses a request
// without a matching Origin and X-Requested-With (403 csrf_failed, and nothing
// is executed), while a bearer token needs neither.
func TestIntegrationHTTPCSRFSweepOverTheSpec(t *testing.T) {
	e := newIDEnv(t, true, true)
	u := e.user(identity.CreateUserParams{Email: "csrf@example.org", Password: "correct horse battery"})
	ctx := t.Context()

	cookieOps := mutating(t, "cookieAuth")
	if len(cookieOps) < 5 {
		t.Fatalf("the spec has only %d cookie-authenticated mutating operations; the sweep would be vacuous", len(cookieOps))
	}
	body := func(op *openapi3.Operation) string {
		if op.RequestBody != nil {
			return `{}`
		}
		return ""
	}
	badHeaders := map[string][]string{
		"no headers":            {},
		"evil origin":           {"Origin", "https://evil.example.com", CSRFHeader, CSRFHeaderValue},
		"null origin":           {"Origin", "null", CSRFHeader, CSRFHeaderValue},
		"suffix trick":          {"Origin", testBase + ".evil.example.com", CSRFHeader, CSRFHeaderValue},
		"right origin, no XRW":  {"Origin", testBase},
		"right origin, wrong X": {"Origin", testBase, CSRFHeader, "true"},
		"XRW only":              {CSRFHeader, CSRFHeaderValue},
	}
	for opName, op := range cookieOps {
		method, path, _ := strings.Cut(opName, " ")
		ws, err := e.svc.IssueWebSession(ctx, u.ID, identity.Meta{IP: "203.0.113.9"})
		if err != nil {
			t.Fatal(err)
		}
		cookie := "__Host-obk_session=" + ws.Token
		for name, h := range badHeaders {
			rec := e.raw(method, path, body(op), append([]string{"Content-Type", "application/json", "Cookie", cookie}, h...)...)
			if rec.Code != 403 || !strings.Contains(rec.Body.String(), `"csrf_failed"`) {
				t.Errorf("%s with %s: got %d %s, want 403 csrf_failed", opName, name, rec.Code, rec.Body)
			}
		}
		// nothing was executed: the session is alive
		if rec := e.raw("GET", "/v1/me", "", "Cookie", cookie); rec.Code != 200 {
			t.Errorf("%s: the session did not survive the rejected requests (%d)", opName, rec.Code)
		}
	}

	// bearer needs no CSRF headers, whatever the origin
	for opName, op := range mutating(t, "bearerAuth") {
		method, path, _ := strings.Cut(opName, " ")
		tp, err := e.svc.IssueTokens(ctx, u.ID, identity.Meta{IP: "203.0.113.9"})
		if err != nil {
			t.Fatal(err)
		}
		rec := e.raw(method, path, body(op), "Content-Type", "application/json", "Authorization", "Bearer "+tp.AccessToken, "Origin", "https://evil.example.com")
		if rec.Code == 403 && strings.Contains(rec.Body.String(), "csrf_failed") {
			t.Errorf("%s: a bearer request was subjected to the CSRF check: %s", opName, rec.Body)
		}
		if rec.Code == 401 {
			t.Errorf("%s: a valid bearer token was refused: %s", opName, rec.Body)
		}
	}
}

// Authentication comes before body validation: an operation that needs a
// session answers 401 to an unauthenticated request whatever its body is, so a
// caller learns nothing about the schema without credentials.
func TestIntegrationHTTPAuthPrecedesValidationOverTheSpec(t *testing.T) {
	e := newIDEnv(t, true, true)
	withBody := 0
	for _, scheme := range []string{"cookieAuth", "bearerAuth"} {
		for opName, op := range mutating(t, scheme) {
			if op.RequestBody == nil {
				continue
			}
			if allowsAnonymous(op) {
				continue // optional authentication: anonymous callers reach the handler (and its own errors)
			}
			withBody++
			method, path, _ := strings.Cut(opName, " ")
			for name, c := range map[string]struct{ ct, body string }{
				"malformed JSON":  {"application/json", `{"new_password":`},
				"invalid values":  {"application/json", `{"method":"nonsense","new_password":"x","code":"1"}`},
				"empty body":      {"application/json", ""},
				"wrong type":      {"text/plain", "hello"},
				"unknown members": {"application/json", `{"zzz":1}`},
			} {
				rec := e.raw(method, path, c.body, "Content-Type", c.ct)
				if rec.Code != 401 {
					t.Errorf("%s with %s: unauthenticated request got %d, want 401 before validation: %s", opName, name, rec.Code, rec.Body)
				}
			}
		}
	}
	if withBody == 0 {
		t.Fatal("no authenticated operation with a request body was found")
	}
	// and with a session the same bad bodies are validation errors
	e.user(identity.CreateUserParams{Email: "v@example.org", Password: "correct horse battery"})
	e.login("v@example.org", "correct horse battery")
	e.problem(e.do("POST", "/v1/auth/password", `{"new_password":`, true, web...), 400, "bad_request")
	e.problem(e.do("POST", "/v1/auth/reauth", `{"method":"nonsense"}`, true, web...), 422, "validation_failed")
}
