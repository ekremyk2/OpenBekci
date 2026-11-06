package httpapi

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
)

// waitMail waits for the n-th mail to arrive (mail from the sign-in request
// endpoints is sent in the background).
func (e *idEnv) waitMail(n int) mail.Message {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if msgs := e.mail.Messages(); len(msgs) >= n {
			return msgs[n-1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("mail %d never arrived", n)
	return mail.Message{}
}

// An email-only user (what `admin create-org` produces: no password, no phone)
// signs in by magic link, passes the re-authentication gate with an email code
// and sets a password. This was a dead end before the email_code method.
func TestIntegrationHTTPEmailOnlyUserReauthsAndSetsPassword(t *testing.T) {
	e := newIDEnv(t, false, true)
	e.user(identity.CreateUserParams{Email: "bob@example.org"})

	if rec := e.do("POST", "/v1/auth/magic-link", `{"email":"bob@example.org"}`, false); rec.Code != 202 {
		t.Fatalf("request link: %d %s", rec.Code, rec.Body)
	}
	link := regexp.MustCompile(`https://\S+`).FindString(e.waitMail(1).Body)
	token := link[strings.Index(link, "token=")+6:]
	rec := e.do("POST", "/v1/auth/magic-link/consume", `{"token":"`+token+`"}`, false)
	e.mustJSON(rec, 200, nil)
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			e.cookie = c
		}
	}
	if e.cookie == nil {
		t.Fatal("no session cookie")
	}

	change := `{"new_password":"my first password"}`
	e.problem(e.do("POST", "/v1/auth/password", change, true, web...), 403, "reauth_required")
	// neither of the other methods can work for this user
	e.problem(e.do("POST", "/v1/auth/reauth", `{"method":"password","password":"anything at all"}`, true, web...), 401, "invalid_grant")
	e.problem(e.do("POST", "/v1/auth/reauth", `{"method":"sms_otp","code":"123456"}`, true, web...), 401, "invalid_grant")

	// the email code
	e.problem(e.do("POST", "/v1/auth/reauth", `{"method":"email_code","code":"123456"}`, true, web...), 401, "invalid_grant")
	if rec := e.do("POST", "/v1/auth/reauth/email-code", "", true, web...); rec.Code != 202 {
		t.Fatalf("request reauth code: %d %s", rec.Code, rec.Body)
	}
	msg := e.waitMail(2)
	if msg.To != "bob@example.org" {
		t.Errorf("code sent to %q", msg.To)
	}
	code := regexp.MustCompile(`\b[0-9]{6}\b`).FindString(msg.Body)
	if code == "" {
		t.Fatalf("no code in %q", msg.Body)
	}
	e.problem(e.do("POST", "/v1/auth/reauth", `{"method":"email_code","code":"`+flip(code)+`"}`, true, web...), 401, "invalid_grant")
	e.mustJSON(e.do("POST", "/v1/auth/reauth", `{"method":"email_code","code":"`+code+`"}`, true, web...), 200, nil)
	// single use
	e.problem(e.do("POST", "/v1/auth/reauth", `{"method":"email_code","code":"`+code+`"}`, true, web...), 401, "invalid_grant")

	if rec := e.do("POST", "/v1/auth/password", change, true, web...); rec.Code != 204 {
		t.Fatalf("set password: %d %s", rec.Code, rec.Body)
	}
	e.cookie = nil
	e.login("bob@example.org", "my first password")
}

// The reauth code endpoint needs a session and a mail gateway, and the code
// has the SMS code's attempt limit.
func TestIntegrationHTTPEmailReauthRules(t *testing.T) {
	e := newIDEnv(t, true, false)
	e.user(identity.CreateUserParams{Email: "nomail@example.org", Password: "correct horse battery"})
	e.problem(e.do("POST", "/v1/auth/reauth/email-code", "", false, web...), 401, "unauthenticated")
	e.login("nomail@example.org", "correct horse battery")
	e.problem(e.do("POST", "/v1/auth/reauth/email-code", "", true, web...), 503, "mail_unavailable")

	e2 := newIDEnv(t, false, true)
	e2.user(identity.CreateUserParams{Phone: "+905321234567", Password: "correct horse battery"})
	e2.login("+905321234567", "correct horse battery")
	e2.problem(e2.do("POST", "/v1/auth/reauth/email-code", "", true, web...), 422, "validation_failed")

	e3 := newIDEnv(t, false, true)
	e3.user(identity.CreateUserParams{Email: "att@example.org", Password: "correct horse battery"})
	e3.login("att@example.org", "correct horse battery")
	if rec := e3.do("POST", "/v1/auth/reauth/email-code", "", true, web...); rec.Code != 202 {
		t.Fatalf("request: %d %s", rec.Code, rec.Body)
	}
	code := regexp.MustCompile(`\b[0-9]{6}\b`).FindString(e3.waitMail(1).Body)
	for range 5 {
		e3.problem(e3.do("POST", "/v1/auth/reauth", `{"method":"email_code","code":"`+flip(code)+`"}`, true, web...), 401, "invalid_grant")
	}
	// the code is burnt after five wrong tries (the account is also locked by then)
	if rec := e3.do("POST", "/v1/auth/reauth", `{"method":"email_code","code":"`+code+`"}`, true, web...); rec.Code == 200 {
		t.Fatal("a code with 5 failed attempts was accepted")
	}
}
