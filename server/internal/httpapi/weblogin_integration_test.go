package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/identity"
)

type webLinkJSON struct {
	Token     string `json:"token"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

func (e *idEnv) mobileToken(email string) (identity.User, string) {
	e.t.Helper()
	u := e.user(identity.CreateUserParams{Email: email})
	tp, err := e.svc.IssueTokens(context.Background(), u.ID, identity.Meta{IP: "198.51.100.7"})
	if err != nil {
		e.t.Fatal(err)
	}
	return u, tp.AccessToken
}

func bearerH(token string) []string { return []string{"Authorization", "Bearer " + token} }

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == HostCookieName && c.Value != "" {
			return c
		}
	}
	return nil
}

func (e *idEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if err := e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), sql, args...)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
}

func TestIntegrationWebLoginLinkIsSingleUseAndBoundToTheSession(t *testing.T) {
	e := newIDEnv(t, false, false)
	u, tok := e.mobileToken("weblink@example.org")
	org := uuid.NewString()
	next := "/orgs/" + org + "/tickets?status=open"

	var l webLinkJSON
	e.mustJSON(e.do("POST", "/v1/me/web-login-link", fmt.Sprintf(`{"next":%q}`, next), false, bearerH(tok)...), 201, &l)
	if len(l.Token) < 40 || l.ExpiresAt == "" {
		t.Fatalf("link = %+v", l)
	}
	if want := testBase + "/login/link/" + l.Token + "?next="; !strings.HasPrefix(l.URL, want) || !strings.Contains(l.URL, "%2Forgs%2F"+org) {
		t.Fatalf("url = %q, want prefix %q with the encoded next", l.URL, want)
	}
	// stored hashed: the token is nowhere in the table
	var leaked int
	if err := e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM web_login_link WHERE token_hash = convert_to($1, 'UTF8') OR encode(token_hash, 'escape') = $1`, l.Token).Scan(&leaked)
	}); err != nil || leaked != 0 {
		t.Fatalf("token stored in clear? leaked=%d err=%v", leaked, err)
	}

	// exchange: a cookie session for the issuing user
	rec := e.do("POST", "/v1/auth/web-login-link:exchange", fmt.Sprintf(`{"token":%q}`, l.Token), false)
	var res struct {
		Client string `json:"client"`
		User   struct {
			ID uuid.UUID `json:"id"`
		} `json:"user"`
	}
	e.mustJSON(rec, 200, &res)
	c := sessionCookie(rec)
	if c == nil || res.Client != "web" || res.User.ID != u.ID {
		t.Fatalf("exchange result %s, cookie %v", rec.Body, c)
	}
	if !c.HttpOnly {
		t.Error("the session cookie is not HttpOnly")
	}
	e.cookie = c
	e.mustJSON(e.do("GET", "/v1/me", "", true), 200, nil)
	e.cookie = nil

	// single use
	e.problem(e.do("POST", "/v1/auth/web-login-link:exchange", fmt.Sprintf(`{"token":%q}`, l.Token), false), 401, "invalid_grant")
	// unknown token
	e.problem(e.do("POST", "/v1/auth/web-login-link:exchange", `{"token":"`+strings.Repeat("a", 43)+`"}`, false), 401, "invalid_grant")
	// a magic-link token is not a web-login token and vice versa
	e.problem(e.do("POST", "/v1/auth/magic-link/consume", fmt.Sprintf(`{"token":%q}`, l.Token), false), 401, "invalid_grant")

	// expiry
	e.mustJSON(e.do("POST", "/v1/me/web-login-link", `{}`, false, bearerH(tok)...), 201, &l)
	e.exec(`UPDATE web_login_link SET expires_at = now() - interval '1 second' WHERE user_id = $1 AND consumed_at IS NULL`, u.ID)
	e.problem(e.do("POST", "/v1/auth/web-login-link:exchange", fmt.Sprintf(`{"token":%q}`, l.Token), false), 401, "invalid_grant")

	// wrong session: signing out the issuing session kills its open links
	e.mustJSON(e.do("POST", "/v1/me/web-login-link", `{}`, false, bearerH(tok)...), 201, &l)
	if rec := e.do("DELETE", "/v1/me/sessions/current", "", false, bearerH(tok)...); rec.Code != 204 {
		t.Fatalf("sign out: %d %s", rec.Code, rec.Body)
	}
	e.problem(e.do("POST", "/v1/auth/web-login-link:exchange", fmt.Sprintf(`{"token":%q}`, l.Token), false), 401, "invalid_grant")

	// a new link voids the earlier open one of the same session
	_, tok2 := e.mobileToken("weblink2@example.org")
	var first, second webLinkJSON
	e.mustJSON(e.do("POST", "/v1/me/web-login-link", `{}`, false, bearerH(tok2)...), 201, &first)
	e.mustJSON(e.do("POST", "/v1/me/web-login-link", `{}`, false, bearerH(tok2)...), 201, &second)
	e.problem(e.do("POST", "/v1/auth/web-login-link:exchange", fmt.Sprintf(`{"token":%q}`, first.Token), false), 401, "invalid_grant")
	e.mustJSON(e.do("POST", "/v1/auth/web-login-link:exchange", fmt.Sprintf(`{"token":%q}`, second.Token), false), 200, nil)
}

func TestIntegrationWebLoginLinkConcurrentExchangeSucceedsOnce(t *testing.T) {
	e := newIDEnv(t, false, false)
	_, tok := e.mobileToken("weblink-race@example.org")
	var l webLinkJSON
	e.mustJSON(e.do("POST", "/v1/me/web-login-link", `{}`, false, bearerH(tok)...), 201, &l)
	results := make(chan int, 6)
	for i := 0; i < 6; i++ {
		go func() {
			r := httptest.NewRequest("POST", "/v1/auth/web-login-link:exchange", strings.NewReader(fmt.Sprintf(`{"token":%q}`, l.Token)))
			r.Header.Set("Content-Type", "application/json")
			r.RemoteAddr = fmt.Sprintf("203.0.113.%d:1", 10+i)
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, r)
			results <- rec.Code
		}()
	}
	ok := 0
	for i := 0; i < 6; i++ {
		if <-results == 200 {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d exchanges succeeded, want exactly 1", ok)
	}
}

func TestIntegrationWebLoginLinkNextMustStayInTheApp(t *testing.T) {
	e := newIDEnv(t, false, false)
	_, tok := e.mobileToken("weblink-next@example.org")
	org := uuid.NewString()
	for _, bad := range []string{
		"https://evil.example/orgs/" + org,
		"//evil.example/orgs/" + org,
		"/\\evil.example",
		"/login",
		"/app/orgs/" + org,
		"/orgs/not-a-uuid/tickets",
		"/orgs/" + org + "/../../login",
		"/orgs/" + org + "/%2e%2e/login",
		"/orgs/" + org + "%2Fx",
		"/orgs/" + org + "/x\\y",
		"/orgs/" + org + "/x\r\nSet-Cookie: a=b",
		"/orgs/" + org + "/x#frag",
		"javascript:alert(1)",
		"orgs/" + org,
	} {
		body, _ := json.Marshal(map[string]string{"next": bad})
		if rec := e.do("POST", "/v1/me/web-login-link", string(body), false, bearerH(tok)...); rec.Code != 422 {
			t.Errorf("next %q: status %d, want 422: %s", bad, rec.Code, rec.Body)
		}
	}
	for _, good := range []string{"/orgs/" + org, "/orgs/" + org + "/", "/orgs/" + org + "/tickets/" + uuid.NewString() + "?tab=comments"} {
		body, _ := json.Marshal(map[string]string{"next": good})
		// the limiter allows five issuances in a burst; stay within it
		if rec := e.do("POST", "/v1/me/web-login-link", string(body), false, bearerH(tok)...); rec.Code != 201 {
			t.Errorf("next %q: status %d: %s", good, rec.Code, rec.Body)
		}
	}
}

func TestIntegrationWebLoginLinkIssuanceIsAuthenticatedAndRateLimited(t *testing.T) {
	e := newIDEnv(t, false, false)
	e.problem(e.do("POST", "/v1/me/web-login-link", `{}`, false), 401, "unauthenticated")
	_, tok := e.mobileToken("weblink-rate@example.org")
	got429 := false
	for i := 0; i < 8; i++ {
		rec := e.do("POST", "/v1/me/web-login-link", `{}`, false, bearerH(tok)...)
		if rec.Code == 429 {
			got429 = true
			if rec.Header().Get("Retry-After") == "" {
				t.Error("429 without Retry-After")
			}
			break
		}
		if rec.Code != 201 {
			t.Fatalf("issuance %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	if !got429 {
		t.Fatal("issuance is not rate limited")
	}
	// another user is not affected by this user's bucket
	_, tok2 := e.mobileToken("weblink-rate2@example.org")
	if rec := e.do("POST", "/v1/me/web-login-link", `{}`, false, bearerH(tok2)...); rec.Code != 201 {
		t.Fatalf("other user: %d", rec.Code)
	}
}
