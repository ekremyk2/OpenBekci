package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/checkout"
	"github.com/openbekci/openbekci/server/internal/core/collections"
	"github.com/openbekci/openbekci/server/internal/core/community"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/instance"
	"github.com/openbekci/openbekci/server/internal/core/integrations"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/moneydocs"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/payments/bankimport"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/platformadmin"
	"github.com/openbekci/openbekci/server/internal/core/reports"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/core/treasury"
	"github.com/openbekci/openbekci/server/internal/packs"
	"github.com/openbekci/openbekci/server/internal/permissions"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/db/dbtest"
	"github.com/openbekci/openbekci/server/internal/platform/idempotency"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
	"github.com/openbekci/openbekci/server/internal/platform/storage"
)

const testBase = "https://bekci.example.org"

// testScope is a files scope type any member may read and attach to.
const testScope = "test.members"

type idEnv struct {
	t      *testing.T
	h      http.Handler
	svc    *identity.Service
	db     *db.DB
	admin  *platformadmin.Service
	sms    *sms.Recorder
	mail   *mail.Recorder
	people *people.Service
	tenSvc *tenancy.Service
	gov    *governance.Service
	comm   *community.Service
	notes  *recNotifier
	charge *recCharger
	hooks  *recHooks
	acc    *access.Service
	integ  *integrations.Service
	jobRec *jobRecorder
	cookie *http.Cookie
	files  *files.Service
	ops    *opsTest
	deps   Deps
}

func newIDEnv(t *testing.T, withSMS, withMail bool) *idEnv {
	t.Helper()
	d := dbtest.New(t)
	e := &idEnv{t: t, db: d, sms: &sms.Recorder{}, mail: &mail.Recorder{}, admin: platformadmin.NewService(d)}
	reg, err := permissions.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	cfg := identity.Config{InstanceName: "Test", BaseURL: testBase, SecretKey: []byte("0123456789abcdef0123456789abcdef"),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if withSMS {
		cfg.SMS = e.sms
	}
	if withMail {
		cfg.Mail = e.mail
	}
	e.svc = identity.NewService(d, cfg)
	authz.SetScopeLoader(tenancy.ScopeLoader{})
	t.Cleanup(func() { authz.SetScopeLoader(nil) })
	var perr error
	e.people, perr = people.NewService(d, e.svc, people.Config{EncryptionKey: []byte("0123456789abcdef0123456789abcdef-enc"), BaseURL: testBase, InstanceName: "Test", SMS: e.sms, Mail: e.mail, Log: cfg.Log,
		Hooks: people.Hooks{OnOccupancyEnded: func(ctx context.Context, tx pgx.Tx, ev people.OccupancyEnded) error {
			return e.comm.OnOccupancyEnded(ctx, tx, ev.OrgID, ev.UnitID, ev.PersonID)
		}}})
	if perr != nil {
		t.Fatal(perr)
	}
	e.tenSvc = tenancy.NewService(d).WithArchiveKey([]byte("test-secret-key-for-archives-0123456789"))
	signer := storage.NewSigner(cfg.SecretKey)
	e.files = files.NewService(d, storage.NewLocal(t.TempDir(), signer), files.Config{Signer: signer, Log: cfg.Log})
	// a scope type any member may read and attach to, for the files tests
	allowAll := func(context.Context, pgx.Tx, files.ScopeRef) (bool, error) { return true, nil }
	e.files.RegisterResolver(testScope, files.ResolverFuncs{Read: allowAll, Attach: allowAll}, files.WithMaxFiles(3))
	e.ops = newOpsTest(d, e.files)
	e.gov = governance.NewService(d, d, governance.Config{BaseURL: testBase, InstanceName: "Test", Log: cfg.Log})
	e.notes, e.charge, e.hooks = &recNotifier{}, &recCharger{}, &recHooks{}
	e.comm, perr = community.NewService(d, community.Config{EncryptionKey: []byte("0123456789abcdef0123456789abcdef-enc"), BaseURL: testBase, Platform: d, Log: cfg.Log,
		Notifier: e.notes, Charger: e.charge, Hooks: e.hooks.hooks()})
	if perr != nil {
		t.Fatal(perr)
	}
	e.acc = newTestAccess(t, d, cfg.Log)
	e.jobRec = &jobRecorder{}
	e.integ = newTestIntegrations(t, d, reg, cfg.Log, e.jobRec, e.notes)
	methods := &instance.AuthMethodRegistry{}
	methods.Register(e.svc)
	inst := instance.NewService(instance.Config{Name: "Test", BaseURL: testBase, ServerVersion: "0.1.0", MinAppVersionAndroid: "1.0.0", MinAppVersionIOS: "1.0.0"},
		instance.PGIDSource{DB: d}, methods)
	e.deps = Deps{
		Log: cfg.Log, DB: d, Idempotency: idempotency.NewPG(d), Instance: inst, Identity: e.svc,
		Authz: authz.NewResolver(d, reg, nil), Tenancy: e.tenSvc, Files: e.files, Ops: e.ops.deps, People: e.people, Governance: e.gov, Community: e.comm, Audit: audit.NewService(d), Access: e.acc, Integrations: e.integ,
		Money:         e.moneyDeps(d),
		Authenticator: SessionAuthenticator{Identity: e.svc, Keys: e.integ, Origins: []string{testBase}}, ValidateResponses: true,
	}
	host, herr := packs.New(packs.Config{DB: d, Log: cfg.Log}, nil)
	if herr == nil {
		herr = host.Start(t.Context())
	}
	if herr != nil {
		t.Fatal(herr)
	}
	e.deps.Packs = host
	e.h = NewRouter(e.deps, Options{})
	return e
}

func (e *idEnv) user(p identity.CreateUserParams) identity.User {
	e.t.Helper()
	u, err := e.svc.CreateUser(e.t.Context(), p)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

// do sends a request. Header pairs are name, value, ... ; the stored session
// cookie is attached when withCookie is set.
func (e *idEnv) do(method, path, body string, withCookie bool, headers ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, rd)
	r.RemoteAddr = "203.0.113.9:4000"
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	if withCookie && e.cookie != nil {
		r.AddCookie(e.cookie)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	// code and link requests send in the background; let them finish
	if err := e.svc.Drain(e.t.Context()); err != nil {
		e.t.Fatal(err)
	}
	return rec
}

var web = []string{"Origin", testBase, "X-Requested-With", "openbekci-web"}

func (e *idEnv) mustJSON(rec *httptest.ResponseRecorder, status int, into any) {
	e.t.Helper()
	if rec.Code != status {
		e.t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body)
	}
	if into != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
			e.t.Fatalf("decode %s: %v", rec.Body, err)
		}
	}
}

func (e *idEnv) problem(rec *httptest.ResponseRecorder, status int, code string) problemBody {
	e.t.Helper()
	if rec.Code != status {
		e.t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body)
	}
	var p problemBody
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		e.t.Fatalf("decode problem %s: %v", rec.Body, err)
	}
	if p.Code != code {
		e.t.Fatalf("code = %q, want %q: %s", p.Code, code, rec.Body)
	}
	return p
}

func (e *idEnv) login(identifier, password string) {
	e.t.Helper()
	rec := e.do("POST", "/v1/auth/session", `{"method":"password","identifier":"`+identifier+`","password":"`+password+`"}`, false)
	if rec.Code != 200 {
		e.t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	e.cookie = nil
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			e.cookie = c
		}
	}
	if e.cookie == nil {
		e.t.Fatal("no session cookie")
	}
}

func TestIntegrationHTTPCookieSession(t *testing.T) {
	e := newIDEnv(t, true, true)
	u := e.user(identity.CreateUserParams{Email: "ayse@example.org", Password: "correct horse battery"})

	// wrong credentials: 401 invalid_grant, with WWW-Authenticate and request_id
	rec := e.do("POST", "/v1/auth/session", `{"method":"password","identifier":"ayse@example.org","password":"nope nope nope"}`, false)
	p := e.problem(rec, 401, "invalid_grant")
	if rec.Header().Get("WWW-Authenticate") == "" || p.Instance == "" {
		t.Errorf("401 headers = %v, problem = %+v", rec.Header(), p)
	}
	var raw map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	if raw["request_id"] != raw["instance"] || raw["request_id"] == "" {
		t.Errorf("request_id = %v, instance = %v", raw["request_id"], raw["instance"])
	}
	// field problems name identifier/password/code
	rec = e.do("POST", "/v1/auth/session", `{"method":"password","identifier":"ayse@example.org"}`, false)
	e.problem(rec, 422, "validation_failed")
	if !strings.Contains(rec.Body.String(), `"field":"password"`) {
		t.Errorf("422 body = %s", rec.Body)
	}
	rec = e.do("POST", "/v1/auth/session", `{"identifier":"x"}`, false)
	e.problem(rec, 422, "validation_failed")
	rec = e.do("POST", "/v1/auth/session", `{"method":"sms_otp","identifier":"+905321234567","code":"12"}`, false)
	e.problem(rec, 422, "validation_failed")

	// success: cookie flags
	rec = e.do("POST", "/v1/auth/session", `{"method":"password","identifier":"ayse@example.org","password":"correct horse battery"}`, false)
	var sess struct {
		SessionID string `json:"session_id"`
		User      struct{ ID string }
	}
	e.mustJSON(rec, 200, &sess)
	if sess.User.ID != u.ID.String() {
		t.Errorf("user = %+v", sess.User)
	}
	setCookie := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"obk_session=", "HttpOnly", "Secure", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(setCookie, want) {
			t.Errorf("Set-Cookie %q lacks %s", setCookie, want)
		}
	}
	e.cookie = rec.Result().Cookies()[0]

	// GET needs no CSRF headers
	var me struct {
		User struct {
			ID              string  `json:"id"`
			DisplayName     string  `json:"display_name"`
			Email           *string `json:"email"`
			Phone           *string `json:"phone"`
			IsPlatformAdmin bool    `json:"is_platform_admin"`
		} `json:"user"`
		Organizations []any          `json:"organizations"`
		Occupancies   []any          `json:"occupancies"`
		Policies      map[string]any `json:"policies"`
	}
	e.mustJSON(e.do("GET", "/v1/me", "", true), 200, &me)
	if me.User.ID != u.ID.String() || me.User.DisplayName != "ayse@example.org" || *me.User.Email != "ayse@example.org" || me.User.Phone != nil ||
		me.User.IsPlatformAdmin || len(me.Organizations) != 0 || me.Occupancies == nil || me.Policies == nil {
		t.Errorf("me = %+v", me)
	}
	// /v1/instance is now reachable, and lists the configured methods
	var inst struct {
		AuthMethods []struct{ ID string } `json:"auth_methods"`
	}
	e.mustJSON(e.do("GET", "/v1/instance", "", true), 200, &inst)
	if len(inst.AuthMethods) != 3 {
		t.Errorf("auth methods = %+v", inst.AuthMethods)
	}
	e.problem(e.do("GET", "/v1/instance", "", false), 401, "unauthenticated")
	e.problem(e.do("GET", "/v1/me", "", false, "Authorization", "Bearer nonsense"), 401, "unauthenticated")

	// sessions list shows this one as current
	var list struct {
		Items []struct {
			ID      string
			Current bool
			Client  string
		}
	}
	e.mustJSON(e.do("GET", "/v1/me/sessions", "", true), 200, &list)
	if len(list.Items) != 1 || !list.Items[0].Current || list.Items[0].ID != sess.SessionID || list.Items[0].Client != "web" {
		t.Errorf("sessions = %+v", list)
	}

	// CSRF: a mutation with the cookie needs Origin and X-Requested-With
	e.problem(e.do("DELETE", "/v1/auth/session", "", true), 403, "csrf_failed")
	e.problem(e.do("DELETE", "/v1/auth/session", "", true, "Origin", testBase), 403, "csrf_failed")
	e.problem(e.do("DELETE", "/v1/auth/session", "", true, "Origin", "https://evil.example.com", "X-Requested-With", "openbekci-web"), 403, "csrf_failed")
	rec = e.do("DELETE", "/v1/auth/session", "", true, web...)
	if rec.Code != 204 {
		t.Fatalf("logout: %d %s", rec.Code, rec.Body)
	}
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].Name != CookieName || c[0].MaxAge >= 0 {
		t.Errorf("logout did not clear the cookie: %v", rec.Header().Get("Set-Cookie"))
	}
	e.problem(e.do("GET", "/v1/me", "", true), 401, "unauthenticated")
}

func TestIntegrationHTTPBearerTokens(t *testing.T) {
	e := newIDEnv(t, true, true)
	e.user(identity.CreateUserParams{Email: "mob@example.org", Password: "correct horse battery"})

	var tok struct {
		TokenType        string `json:"token_type"`
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		SessionID        string `json:"session_id"`
		AccessExpiresAt  string `json:"access_expires_at"`
		RefreshExpiresAt string `json:"refresh_expires_at"`
	}
	e.mustJSON(e.do("POST", "/v1/auth/token", `{"method":"password","identifier":"mob@example.org","password":"correct horse battery","device_name":"Pixel 9"}`, false), 200, &tok)
	if tok.TokenType != "Bearer" || tok.AccessToken == "" || tok.RefreshToken == "" {
		t.Fatalf("tokens = %+v", tok)
	}
	bearer := func(t string) []string { return []string{"Authorization", "Bearer " + t} }

	// bearer needs no CSRF headers; the refresh token is not an access token
	e.mustJSON(e.do("GET", "/v1/me", "", false, bearer(tok.AccessToken)...), 200, nil)
	e.problem(e.do("GET", "/v1/me", "", false, bearer(tok.RefreshToken)...), 401, "unauthenticated")
	var list struct {
		Items []struct{ Client, DeviceName string }
	}
	e.mustJSON(e.do("GET", "/v1/me/sessions", "", false, bearer(tok.AccessToken)...), 200, &list)
	if len(list.Items) != 1 || list.Items[0].Client != "mobile" {
		t.Errorf("sessions = %s", e.do("GET", "/v1/me/sessions", "", false, bearer(tok.AccessToken)...).Body)
	}

	// rotation, then reuse of the old refresh token kills the family
	var tok2 struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		SessionID    string `json:"session_id"`
	}
	e.mustJSON(e.do("POST", "/v1/auth/token/refresh", `{"refresh_token":"`+tok.RefreshToken+`"}`, false), 200, &tok2)
	if tok2.SessionID != tok.SessionID || tok2.RefreshToken == tok.RefreshToken {
		t.Errorf("rotation = %+v", tok2)
	}
	e.mustJSON(e.do("GET", "/v1/me", "", false, bearer(tok2.AccessToken)...), 200, nil)
	// a retry inside the 30 s grace window (the response was lost) gets the same pair, once ...
	var again struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		SessionID    string `json:"session_id"`
	}
	e.mustJSON(e.do("POST", "/v1/auth/token/refresh", `{"refresh_token":"`+tok.RefreshToken+`"}`, false), 200, &again)
	if again != tok2 {
		t.Errorf("grace replay = %+v, want the same pair %+v", again, tok2)
	}
	e.mustJSON(e.do("GET", "/v1/me", "", false, bearer(tok2.AccessToken)...), 200, nil)
	// ... the next reuse is a leak: the family dies
	e.problem(e.do("POST", "/v1/auth/token/refresh", `{"refresh_token":"`+tok.RefreshToken+`"}`, false), 401, "invalid_grant")
	e.problem(e.do("GET", "/v1/me", "", false, bearer(tok2.AccessToken)...), 401, "unauthenticated")

	// logout by id with a fresh pair
	e.mustJSON(e.do("POST", "/v1/auth/token", `{"method":"password","identifier":"mob@example.org","password":"correct horse battery"}`, false), 200, &tok)
	if rec := e.do("DELETE", "/v1/me/sessions/"+tok.SessionID, "", false, bearer(tok.AccessToken)...); rec.Code != 204 {
		t.Fatalf("revoke by id: %d %s", rec.Code, rec.Body)
	}
	e.problem(e.do("GET", "/v1/me", "", false, bearer(tok.AccessToken)...), 401, "unauthenticated")

	// unknown session id, and "current" shape
	e.mustJSON(e.do("POST", "/v1/auth/token", `{"method":"password","identifier":"mob@example.org","password":"correct horse battery"}`, false), 200, &tok)
	e.problem(e.do("DELETE", "/v1/me/sessions/"+uuid.NewString(), "", false, bearer(tok.AccessToken)...), 404, "not_found")
	e.problem(e.do("DELETE", "/v1/me/sessions/not-a-uuid", "", false, bearer(tok.AccessToken)...), 422, "validation_failed")
	if rec := e.do("DELETE", "/v1/me/sessions/current", "", false, bearer(tok.AccessToken)...); rec.Code != 204 {
		t.Fatalf("revoke current: %d %s", rec.Code, rec.Body)
	}
	// sign out everywhere
	e.mustJSON(e.do("POST", "/v1/auth/token", `{"method":"password","identifier":"mob@example.org","password":"correct horse battery"}`, false), 200, &tok)
	if rec := e.do("DELETE", "/v1/me/sessions", "", false, bearer(tok.AccessToken)...); rec.Code != 204 {
		t.Fatalf("revoke all: %d %s", rec.Code, rec.Body)
	}
	e.problem(e.do("GET", "/v1/me", "", false, bearer(tok.AccessToken)...), 401, "unauthenticated")
}

func TestIntegrationHTTPReauthAndPassword(t *testing.T) {
	e := newIDEnv(t, true, true)
	e.user(identity.CreateUserParams{Email: "re@example.org", Password: "correct horse battery"})
	e.login("re@example.org", "correct horse battery")

	change := `{"new_password":"a brand new password"}`
	e.problem(e.do("POST", "/v1/auth/password", change, true, web...), 403, "reauth_required")
	e.problem(e.do("POST", "/v1/auth/password", `{"new_password":"short"}`, true, web...), 422, "validation_failed")
	e.problem(e.do("POST", "/v1/auth/reauth", `{"method":"password","password":"wrong wrong wrong"}`, true, web...), 401, "invalid_grant")
	var re struct {
		ReauthUntil string `json:"reauth_until"`
	}
	e.mustJSON(e.do("POST", "/v1/auth/reauth", `{"method":"password","password":"correct horse battery"}`, true, web...), 200, &re)
	if re.ReauthUntil == "" {
		t.Error("no reauth_until")
	}
	if rec := e.do("POST", "/v1/auth/password", change, true, web...); rec.Code != 204 {
		t.Fatalf("change password: %d %s", rec.Code, rec.Body)
	}
	e.cookie = nil
	e.login("re@example.org", "a brand new password")
}

func TestIntegrationHTTPOTPAndMagicLink(t *testing.T) {
	e := newIDEnv(t, true, true)
	e.user(identity.CreateUserParams{Email: "x@example.org", Phone: "+905321234567"})

	// sms_otp without code sends; with code signs in
	if rec := e.do("POST", "/v1/auth/session", `{"method":"sms_otp","identifier":"0532 123 45 67"}`, false); rec.Code != 202 || rec.Body.Len() != 0 {
		t.Fatalf("request code: %d %q", rec.Code, rec.Body)
	}
	m, ok := e.sms.Last()
	code := regexp.MustCompile(`[0-9]{6}`).FindString(m.Text)
	if !ok || code == "" {
		t.Fatalf("no code sent: %+v", m)
	}
	e.problem(e.do("POST", "/v1/auth/session", `{"method":"sms_otp","identifier":"+905321234567","code":"`+flip(code)+`"}`, false), 401, "invalid_grant")
	rec := e.do("POST", "/v1/auth/session", `{"method":"sms_otp","identifier":"+905321234567","code":"`+code+`"}`, false)
	e.mustJSON(rec, 200, nil)
	// the dedicated endpoint behaves the same
	if rec := e.do("POST", "/v1/auth/otp", `{"phone":"+905321234567"}`, false); rec.Code != 202 {
		t.Fatalf("auth.requestOtp: %d %s", rec.Code, rec.Body)
	}

	// magic link via the session endpoint, web form, consumed into a cookie
	if rec := e.do("POST", "/v1/auth/session", `{"method":"magic_link","identifier":"x@example.org"}`, false); rec.Code != 202 {
		t.Fatalf("request link: %d %s", rec.Code, rec.Body)
	}
	mm, _ := e.mail.Last()
	link := regexp.MustCompile(`https://\S+`).FindString(mm.Body)
	if !strings.HasPrefix(link, testBase+"/login/magic?token=") {
		t.Fatalf("link = %q", link)
	}
	token := link[strings.Index(link, "token=")+6:]
	rec = e.do("POST", "/v1/auth/magic-link/consume", `{"token":"`+token+`"}`, false)
	var res struct{ Client string }
	e.mustJSON(rec, 200, &res)
	if res.Client != "web" || len(rec.Result().Cookies()) != 1 {
		t.Errorf("consume web: %s cookies=%v", rec.Body, rec.Result().Cookies())
	}
	e.problem(e.do("POST", "/v1/auth/magic-link/consume", `{"token":"`+token+`"}`, false), 401, "invalid_grant")

	// mobile form of the link, consumed into tokens (no cookie)
	if rec := e.do("POST", "/v1/auth/magic-link", `{"email":"x@example.org","client":"mobile"}`, false); rec.Code != 202 {
		t.Fatalf("auth.requestMagicLink: %d %s", rec.Code, rec.Body)
	}
	mm, _ = e.mail.Last()
	link = regexp.MustCompile(`openbekci://\S+`).FindString(mm.Body)
	if !strings.HasPrefix(link, "openbekci://login/magic?token=") {
		t.Fatalf("mobile link = %q", link)
	}
	token = link[strings.Index(link, "token=")+6:]
	rec = e.do("POST", "/v1/auth/magic-link/consume", `{"token":"`+token+`","client":"mobile"}`, false)
	var res2 struct {
		Client       string `json:"client"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	e.mustJSON(rec, 200, &res2)
	if res2.Client != "mobile" || res2.AccessToken == "" || res2.RefreshToken == "" || len(rec.Result().Cookies()) != 0 {
		t.Errorf("consume mobile: %s", rec.Body)
	}
	e.mustJSON(e.do("GET", "/v1/me", "", false, "Authorization", "Bearer "+res2.AccessToken), 200, nil)
}

func flip(code string) string {
	if code[0] == '9' {
		return "0" + code[1:]
	}
	return string(code[0]+1) + code[1:]
}

func TestIntegrationHTTPUnavailableMethodsAndLimits(t *testing.T) {
	e := newIDEnv(t, false, false)
	e.user(identity.CreateUserParams{Email: "p@example.org", Phone: "+905321234567", Password: "correct horse battery"})

	e.problem(e.do("POST", "/v1/auth/session", `{"method":"sms_otp","identifier":"+905321234567"}`, false), 503, "sms_unavailable")
	e.problem(e.do("POST", "/v1/auth/otp", `{"phone":"+905321234567"}`, false), 503, "sms_unavailable")
	e.problem(e.do("POST", "/v1/auth/session", `{"method":"magic_link","identifier":"p@example.org"}`, false), 503, "mail_unavailable")
	e.login("p@example.org", "correct horse battery")
	var inst struct {
		AuthMethods []struct{ ID string } `json:"auth_methods"`
	}
	e.mustJSON(e.do("GET", "/v1/instance", "", true), 200, &inst)
	if len(inst.AuthMethods) != 1 || inst.AuthMethods[0].ID != "password" {
		t.Errorf("auth methods = %+v", inst.AuthMethods)
	}

	// lockout answers 429 with Retry-After
	var last *httptest.ResponseRecorder
	for range 6 {
		last = e.do("POST", "/v1/auth/session", `{"method":"password","identifier":"p@example.org","password":"wrong wrong wrong"}`, false)
	}
	e.problem(last, 429, "rate_limited")
	if last.Header().Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}
	// the right password is refused while locked
	e.problem(e.do("POST", "/v1/auth/session", `{"method":"password","identifier":"p@example.org","password":"correct horse battery"}`, false), 429, "rate_limited")
}

func TestIntegrationHTTPMeWithOrganization(t *testing.T) {
	e := newIDEnv(t, true, true)
	u := e.user(identity.CreateUserParams{Email: "boss@example.org", Password: "correct horse battery"})
	org, err := e.admin.CreateOrganization(t.Context(), platformadmin.CreateOrgParams{Slug: "gul-sitesi", Name: "Gül Sitesi", AdminUserID: u.ID, AdminName: "Ayşe Yılmaz"})
	if err != nil {
		t.Fatal(err)
	}
	e.login("boss@example.org", "correct horse battery")
	rec := e.do("GET", "/v1/me", "", true)
	var me struct {
		User struct {
			DisplayName string `json:"display_name"`
		} `json:"user"`
		Organizations []struct {
			ID            string `json:"id"`
			Slug          string
			Name          string
			DefaultLocale string `json:"default_locale"`
			Roles         []struct {
				Role      string
				ScopeType string  `json:"scope_type"`
				ScopeID   *string `json:"scope_id"`
			}
			Permissions []any
			Sites       []any
		}
	}
	e.mustJSON(rec, 200, &me)
	if me.User.DisplayName != "Ayşe Yılmaz" || len(me.Organizations) != 1 {
		t.Fatalf("me = %s", rec.Body)
	}
	o := me.Organizations[0]
	if o.ID != org.ID.String() || o.Slug != "gul-sitesi" || o.DefaultLocale != "tr" || len(o.Roles) != 1 || o.Roles[0].Role != "org_admin" ||
		o.Roles[0].ScopeType != "org" || o.Roles[0].ScopeID != nil || len(o.Permissions) == 0 || o.Sites == nil {
		t.Errorf("org = %+v", o)
	}
}

// moneyDeps builds the money services of the test environment, with the file scopes of their documents registered.
func (e *idEnv) moneyDeps(d *db.DB) MoneyDeps {
	bill := billing.NewService(d, e.tenSvc, nil)
	pay := payments.NewService(d, nil, nil)
	rep := reports.NewService(d, nil)
	e.files.RegisterResolver(treasury.FileScope, treasury.FileResolver(), files.WithMaxFiles(5))
	e.files.RegisterResolver(moneydocs.ScopePayment, moneydocs.PaymentFileResolver())
	return MoneyDeps{Ledger: ledger.NewService(d, nil), Billing: bill, Payments: pay, Reports: rep,
		Treasury: treasury.NewService(d, e.files, nil), Docs: moneydocs.NewService(d, e.files, pay, rep, nil, nil),
		BankImport: bankimport.NewService(d, pay, nil), Collections: collections.NewService(d, bill, nil, nil),
		Checkout: checkout.NewService(checkout.Config{DB: d, Platform: d, Payments: pay, BaseURL: "https://app.example.test"})}
}
