package identity_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/platformadmin"
	"github.com/openbekci/openbekci/server/internal/platform/db/dbtest"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
	"github.com/openbekci/openbekci/server/internal/problem"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type env struct {
	svc   *identity.Service
	admin *platformadmin.Service
	sms   *sms.Recorder
	mail  *mail.Recorder
	clk   *clock
	db    interface {
		InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
	}
}

var meta = identity.Meta{IP: "203.0.113.7", UserAgent: "test/1.0"}

func newEnv(t *testing.T, opts ...func(*identity.Config)) *env {
	t.Helper()
	d := dbtest.New(t)
	e := &env{sms: &sms.Recorder{}, mail: &mail.Recorder{}, clk: &clock{t: time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)}, db: d}
	cfg := identity.Config{
		InstanceName: "Test Site", BaseURL: "https://bekci.example.org",
		SecretKey: []byte("0123456789abcdef0123456789abcdef"),
		SMS:       e.sms, Mail: e.mail, Now: e.clk.Now,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, o := range opts {
		o(&cfg)
	}
	e.svc = identity.NewService(d, cfg)
	e.admin = platformadmin.NewService(d)
	return e
}

func (e *env) user(t *testing.T, p identity.CreateUserParams) identity.User {
	t.Helper()
	u, err := e.svc.CreateUser(context.Background(), p)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u
}

func wantProblem(t *testing.T, err error, status int, code problem.Code) {
	t.Helper()
	_ = wantProblemPE(t, err, status, code)
}

func wantProblemPE(t *testing.T, err error, status int, code problem.Code) *problem.Error {
	t.Helper()
	var pe *problem.Error
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want problem %d %s", err, status, code)
	}
	if pe.Status != status || pe.Code != code {
		t.Fatalf("problem = %d %s (%s), want %d %s", pe.Status, pe.Code, pe.Detail, status, code)
	}
	return pe
}

var sixDigits = regexp.MustCompile(`\b[0-9]{6}\b`)

func lastCode(t *testing.T, r *sms.Recorder) string {
	t.Helper()
	m, ok := r.Last()
	if !ok {
		t.Fatal("no SMS was sent")
	}
	c := sixDigits.FindString(m.Text)
	if c == "" {
		t.Fatalf("no code in %q", m.Text)
	}
	return c
}

func TestIntegrationPasswordSignIn(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Email: "Ayse@Example.org", Phone: "0532 123 45 67", Password: "correct horse battery"})
	if *u.Email != "ayse@example.org" || *u.Phone != "+905321234567" {
		t.Fatalf("contact not normalized: %v %v", *u.Email, *u.Phone)
	}

	// by email (any case), by phone (national format)
	for _, ident := range []string{"AYSE@example.org", "+90 532 123 45 67", "05321234567"} {
		got, err := e.svc.PasswordSignIn(ctx, ident, "correct horse battery", meta)
		if err != nil || got.ID != u.ID {
			t.Fatalf("sign in as %q: %v", ident, err)
		}
	}
	// everything wrong looks the same
	for name, c := range map[string][2]string{
		"wrong password": {"ayse@example.org", "wrong password!!"},
		"unknown user":   {"nobody@example.org", "correct horse battery"},
		"garbage ident":  {"???", "correct horse battery"},
	} {
		_, err := e.svc.PasswordSignIn(ctx, c[0], c[1], meta)
		pe := wantProblemPE(t, err, 401, problem.CodeInvalidGrant)
		if pe.Detail != "the credentials or code are not valid" {
			t.Errorf("%s: detail %q", name, pe.Detail)
		}
	}
	// no password set
	e.user(t, identity.CreateUserParams{Email: "nopw@example.org"})
	_, err := e.svc.PasswordSignIn(ctx, "nopw@example.org", "anything at all", meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)

	// duplicate contact
	_, err = e.svc.CreateUser(ctx, identity.CreateUserParams{Email: "ayse@EXAMPLE.org"})
	wantProblem(t, err, 409, problem.CodeConflict)
}

func TestIntegrationLockout(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.user(t, identity.CreateUserParams{Email: "lock@example.org", Password: "correct horse battery"})

	for i := range 5 {
		_, err := e.svc.PasswordSignIn(ctx, "lock@example.org", "wrong password!!", meta)
		wantProblem(t, err, 401, problem.CodeInvalidGrant)
		_ = i
	}
	// locked: even the right password is refused, with Retry-After
	_, err := e.svc.PasswordSignIn(ctx, "lock@example.org", "correct horse battery", meta)
	pe := wantProblemPE(t, err, 429, problem.CodeRateLimited)
	if pe.RetryAfter <= 0 || pe.RetryAfter > 30*time.Second {
		t.Errorf("retry after = %v, want within 30s", pe.RetryAfter)
	}
	// still locked a bit later, free after the lock ends
	e.clk.Advance(10 * time.Second)
	_, err = e.svc.PasswordSignIn(ctx, "lock@example.org", "correct horse battery", meta)
	wantProblem(t, err, 429, problem.CodeRateLimited)
	e.clk.Advance(25 * time.Second)
	if _, err := e.svc.PasswordSignIn(ctx, "lock@example.org", "correct horse battery", meta); err != nil {
		t.Fatalf("after the lock: %v", err)
	}
	// a success resets the count: four more failures do not lock
	for range 4 {
		_, err := e.svc.PasswordSignIn(ctx, "lock@example.org", "wrong password!!", meta)
		wantProblem(t, err, 401, problem.CodeInvalidGrant)
	}
	if _, err := e.svc.PasswordSignIn(ctx, "lock@example.org", "correct horse battery", meta); err != nil {
		t.Fatalf("below the threshold: %v", err)
	}

	e.clk.Advance(time.Second) // a success and failures at the very same instant are not ordered
	// the lock doubles with more failures: 6th failure -> 60s
	for range 5 {
		_, _ = e.svc.PasswordSignIn(ctx, "lock@example.org", "wrong password!!", meta)
	}
	e.clk.Advance(31 * time.Second)
	_, err = e.svc.PasswordSignIn(ctx, "lock@example.org", "wrong password!!", meta) // allowed (30s passed), fails: 6 failures
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
	_, err = e.svc.PasswordSignIn(ctx, "lock@example.org", "correct horse battery", meta)
	pe = wantProblemPE(t, err, 429, problem.CodeRateLimited)
	if pe.RetryAfter <= 30*time.Second {
		t.Errorf("second lock = %v, want longer than the first", pe.RetryAfter)
	}
}

func TestIntegrationIPRateLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var limited bool
	for range 40 {
		_, err := e.svc.PasswordSignIn(ctx, "x"+uuid.NewString()[:8]+"@example.org", "whatever password", meta)
		var pe *problem.Error
		if errors.As(err, &pe) && pe.Code == problem.CodeRateLimited {
			limited = true
			if pe.RetryAfter <= 0 {
				t.Error("rate limit without retry-after")
			}
			break
		}
	}
	if !limited {
		t.Error("40 rapid attempts from one address were never limited")
	}
}

func TestIntegrationSMSOTP(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Phone: "+905321234567", Locale: "en"})

	if err := e.requestOTP(ctx, "0532 123 45 67", meta); err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	msg, _ := e.sms.Last()
	if msg.To != "+905321234567" || !strings.Contains(msg.Text, "sign-in code") {
		t.Errorf("message = %+v", msg)
	}
	code := lastCode(t, e.sms)

	// wrong code, then the right one
	_, err := e.svc.OTPSignIn(ctx, "+905321234567", "000000"[:5]+"1", meta)
	if code == "000001" {
		t.Skip("random code collided with the wrong guess")
	}
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
	got, err := e.svc.OTPSignIn(ctx, "+905321234567", code, meta)
	if err != nil || got.ID != u.ID {
		t.Fatalf("OTPSignIn: %v", err)
	}
	// single use
	_, err = e.svc.OTPSignIn(ctx, "+905321234567", code, meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)

	// expiry (5 minutes)
	if err := e.requestOTP(ctx, "+905321234567", meta); err != nil {
		t.Fatal(err)
	}
	code = lastCode(t, e.sms)
	e.clk.Advance(5*time.Minute + time.Second)
	_, err = e.svc.OTPSignIn(ctx, "+905321234567", code, meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)

	// a new request voids the previous code
	e.clk.Advance(time.Hour) // let the abuse buckets refill
	if err := e.requestOTP(ctx, "+905321234567", meta); err != nil {
		t.Fatal(err)
	}
	first := lastCode(t, e.sms)
	if err := e.requestOTP(ctx, "+905321234567", meta); err != nil {
		t.Fatal(err)
	}
	second := lastCode(t, e.sms)
	if first != second {
		_, err = e.svc.OTPSignIn(ctx, "+905321234567", first, meta)
		wantProblem(t, err, 401, problem.CodeInvalidGrant)
	}
	if _, err = e.svc.OTPSignIn(ctx, "+905321234567", second, meta); err != nil {
		t.Fatalf("newest code: %v", err)
	}
}

func TestIntegrationOTPAttemptsAndUnknownPhone(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.user(t, identity.CreateUserParams{Phone: "+905321234567"})
	if err := e.requestOTP(ctx, "+905321234567", meta); err != nil {
		t.Fatal(err)
	}
	right := lastCode(t, e.sms)
	wrong := "000000"
	if right == wrong {
		wrong = "111111"
	}
	// five wrong guesses use up the code; the right one then fails too
	// (use different addresses so the per-address limiter and lockout do not interfere)
	for i := range 5 {
		m := identity.Meta{IP: "198.51.100." + string(rune('1'+i))}
		_, err := e.svc.OTPSignIn(ctx, "+905321234567", wrong, m)
		wantProblem(t, err, 401, problem.CodeInvalidGrant)
	}
	_, err := e.svc.OTPSignIn(ctx, "+905321234567", right, identity.Meta{IP: "198.51.100.9"})
	if pe := (*problem.Error)(nil); errors.As(err, &pe) {
		if pe.Code != problem.CodeInvalidGrant && pe.Code != problem.CodeRateLimited {
			t.Fatalf("after 5 wrong guesses: %v", err)
		}
	} else {
		t.Fatalf("code still valid after 5 wrong guesses: %v", err)
	}

	// unknown number: same answer, nothing sent
	before := len(e.sms.Messages())
	if err := e.requestOTP(ctx, "+905559998877", meta); err != nil {
		t.Fatalf("unknown phone: %v", err)
	}
	if len(e.sms.Messages()) != before {
		t.Error("an SMS went to an unknown number")
	}
	// malformed number
	wantProblem(t, e.requestOTP(ctx, "12", meta), 422, problem.CodeValidationFailed)
}

func TestIntegrationOTPSendLimitsAndDisabled(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.user(t, identity.CreateUserParams{Phone: "+905321234567"})
	for i := range 3 {
		if err := e.requestOTP(ctx, "+905321234567", meta); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	pe := wantProblemPE(t, e.requestOTP(ctx, "+905321234567", meta), 429, problem.CodeRateLimited)
	if pe.RetryAfter <= 0 {
		t.Error("no retry-after")
	}

	d := identity.NewService(nil, identity.Config{}) // no gateway
	wantProblem(t, d.RequestOTP(ctx, "+905321234567", meta), 503, problem.CodeSMSUnavailable)
	wantProblem(t, d.RequestMagicLink(ctx, "a@b.org", identity.ClientWeb, meta), 503, problem.CodeMailUnavailable)
	if m := d.AuthMethods(ctx); len(m) != 1 || m[0].ID != "password" {
		t.Errorf("methods without sms and mail = %+v", m)
	}
	if m := e.svc.AuthMethods(ctx); len(m) != 3 {
		t.Errorf("methods with sms and mail = %+v", m)
	}

	// a gateway failure is never reported to the requester (it would reveal
	// that the account exists); the operator sees it in the delivery log
	e2 := newEnv(t)
	e2.user(t, identity.CreateUserParams{Phone: "+905321234567"})
	e2.sms.Err = sms.ErrUnavailable
	if err := e2.requestOTP(ctx, "+905321234567", meta); err != nil {
		t.Fatalf("gateway failure surfaced to the requester: %v", err)
	}
	ds, err := e2.svc.RecentDeliveries(ctx, 10, true)
	if err != nil || len(ds) != 1 || ds[0].Status != "failed" || ds[0].Channel != "sms" || ds[0].Target != "+90********67" {
		t.Fatalf("delivery log = %+v, %v", ds, err)
	}
}

func tokenOf(t *testing.T, link string) string {
	t.Helper()
	i := strings.Index(link, "token=")
	if i < 0 {
		t.Fatalf("no token in %q", link)
	}
	return strings.TrimSpace(strings.SplitN(link[i+len("token="):], "\n", 2)[0])
}

func linkIn(t *testing.T, body string) string {
	t.Helper()
	re := regexp.MustCompile(`(https://|openbekci://)\S+`)
	l := re.FindString(body)
	if l == "" {
		t.Fatalf("no link in %q", body)
	}
	return l
}

func TestIntegrationMagicLink(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Email: "link@example.org"})

	if err := e.requestLink(ctx, "Link@Example.org", identity.ClientWeb, meta); err != nil {
		t.Fatal(err)
	}
	m, _ := e.mail.Last()
	if m.To != "link@example.org" {
		t.Errorf("to = %q", m.To)
	}
	link := linkIn(t, m.Body)
	if !strings.HasPrefix(link, "https://bekci.example.org/login/magic?token=") {
		t.Errorf("link = %q", link)
	}
	got, err := e.svc.MagicLinkSignIn(ctx, tokenOf(t, link), meta)
	if err != nil || got.ID != u.ID {
		t.Fatalf("consume: %v", err)
	}
	// single use
	_, err = e.svc.MagicLinkSignIn(ctx, tokenOf(t, link), meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)

	// mobile form of the link, and a newer link voids the older one
	if err := e.requestLink(ctx, "link@example.org", identity.ClientMobile, meta); err != nil {
		t.Fatal(err)
	}
	m, _ = e.mail.Last()
	old := linkIn(t, m.Body)
	if !strings.HasPrefix(old, "openbekci://login/magic?token=") {
		t.Errorf("mobile link = %q", old)
	}
	if err := e.requestLink(ctx, "link@example.org", identity.ClientWeb, meta); err != nil {
		t.Fatal(err)
	}
	m, _ = e.mail.Last()
	newer := linkIn(t, m.Body)
	_, err = e.svc.MagicLinkSignIn(ctx, tokenOf(t, old), meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)

	// expiry (15 minutes)
	e.clk.Advance(15*time.Minute + time.Second)
	_, err = e.svc.MagicLinkSignIn(ctx, tokenOf(t, newer), meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)

	// unknown address: accepted, nothing sent; garbage token rejected
	n := len(e.mail.Messages())
	if err := e.requestLink(ctx, "nobody@example.org", identity.ClientWeb, meta); err != nil {
		t.Fatal(err)
	}
	if len(e.mail.Messages()) != n {
		t.Error("mail sent to an unknown address")
	}
	_, err = e.svc.MagicLinkSignIn(ctx, strings.Repeat("x", 43), meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
}

func TestIntegrationAdminLoginLink(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Phone: "+905321234567"}) // no email: the link still works
	link, err := e.svc.CreateLoginLink(ctx, u.ID, identity.ClientWeb)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := e.svc.MagicLinkSignIn(ctx, tokenOf(t, link), meta); err != nil || got.ID != u.ID {
		t.Fatalf("consume admin link: %v", err)
	}
}

func TestIntegrationWebSessionLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Email: "web@example.org", Password: "correct horse battery"})

	ws, err := e.svc.IssueWebSession(ctx, u.ID, identity.Meta{IP: "203.0.113.7", UserAgent: "Firefox", DeviceName: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if p.ID != u.ID || p.UserID != u.ID || p.FamilyID != ws.SessionID || !p.ViaCookie || p.IsPlatformAdmin || !p.ReauthUntil.IsZero() {
		t.Errorf("principal = %+v", p)
	}
	// a cookie token is not a bearer token, and vice versa
	_, err = e.svc.Authenticate(ctx, identity.KindAccessToken, ws.Token)
	wantProblem(t, err, 401, problem.CodeUnauthenticated)
	_, err = e.svc.Authenticate(ctx, identity.KindWebCookie, "nonsense")
	wantProblem(t, err, 401, problem.CodeUnauthenticated)

	list, err := e.svc.ListSessions(ctx, u.ID, p.FamilyID)
	if err != nil || len(list) != 1 || !list[0].Current || list[0].Kind != identity.KindWebCookie || *list[0].DeviceName != "laptop" || *list[0].UserAgent != "Firefox" {
		t.Fatalf("sessions = %+v, %v", list, err)
	}

	// last_used_at is touched, but not on every request
	first, _ := e.svc.ListSessions(ctx, u.ID, p.FamilyID)
	touched := *first[0].LastUsedAt
	e.clk.Advance(time.Minute)
	if _, err := e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token); err != nil {
		t.Fatal(err)
	}
	l, _ := e.svc.ListSessions(ctx, u.ID, p.FamilyID)
	if !l[0].LastUsedAt.Equal(touched) {
		t.Error("last_used_at touched again within the throttle interval")
	}
	e.clk.Advance(6 * time.Minute)
	if _, err := e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token); err != nil {
		t.Fatal(err)
	}
	l, _ = e.svc.ListSessions(ctx, u.ID, p.FamilyID)
	if !l[0].LastUsedAt.After(touched) {
		t.Error("last_used_at not touched after the interval")
	}

	// revoke by id; revoking twice or someone else's id is not found
	other := e.user(t, identity.CreateUserParams{Email: "other@example.org"})
	if found, err := e.svc.RevokeSession(ctx, other.ID, ws.SessionID); err != nil || found {
		t.Fatalf("revoking another user's session: found=%v err=%v", found, err)
	}
	if found, err := e.svc.RevokeSession(ctx, u.ID, ws.SessionID); err != nil || !found {
		t.Fatalf("revoke: found=%v err=%v", found, err)
	}
	if found, _ := e.svc.RevokeSession(ctx, u.ID, ws.SessionID); found {
		t.Error("revoked twice")
	}
	_, err = e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token)
	wantProblem(t, err, 401, problem.CodeUnauthenticated)

	// expiry (30 days)
	ws, _ = e.svc.IssueWebSession(ctx, u.ID, meta)
	e.clk.Advance(30*24*time.Hour + time.Second)
	_, err = e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token)
	wantProblem(t, err, 401, problem.CodeUnauthenticated)
}

func TestIntegrationTokenFamilyRotationAndReuse(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Email: "mob@example.org"})

	tp1, err := e.svc.IssueTokens(ctx, u.ID, identity.Meta{IP: "203.0.113.7", DeviceName: "Pixel"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.Authenticate(ctx, identity.KindAccessToken, tp1.AccessToken)
	if err != nil || p.ViaCookie || p.FamilyID != tp1.SessionID {
		t.Fatalf("access token: %+v %v", p, err)
	}
	// the refresh token never authenticates API calls
	_, err = e.svc.Authenticate(ctx, identity.KindAccessToken, tp1.RefreshToken)
	wantProblem(t, err, 401, problem.CodeUnauthenticated)
	if d := tp1.AccessExpiresAt.Sub(e.clk.Now()); d != 15*time.Minute {
		t.Errorf("access ttl = %v", d)
	}
	if d := tp1.RefreshExpiresAt.Sub(e.clk.Now()); d != 60*24*time.Hour {
		t.Errorf("refresh ttl = %v", d)
	}

	// rotation keeps the family (session id) and lists one session
	tp2, uid, err := e.svc.Refresh(ctx, tp1.RefreshToken, meta)
	if err != nil || uid != u.ID {
		t.Fatalf("refresh: %v", err)
	}
	if tp2.SessionID != tp1.SessionID || tp2.RefreshToken == tp1.RefreshToken || tp2.AccessToken == tp1.AccessToken {
		t.Errorf("rotation did not issue new tokens in the same family")
	}
	list, _ := e.svc.ListSessions(ctx, u.ID, tp1.SessionID)
	if len(list) != 1 || list[0].ID != tp1.SessionID || !list[0].Current || list[0].Kind != identity.KindRefreshToken {
		t.Fatalf("sessions after rotation = %+v", list)
	}
	if _, err := e.svc.Authenticate(ctx, identity.KindAccessToken, tp2.AccessToken); err != nil {
		t.Fatalf("new access token: %v", err)
	}

	// reuse of the rotated refresh token (after the grace window): invalid_grant and the family dies
	e.clk.Advance(identity.RefreshGrace + time.Second)
	_, _, err = e.svc.Refresh(ctx, tp1.RefreshToken, meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
	_, err = e.svc.Authenticate(ctx, identity.KindAccessToken, tp2.AccessToken)
	wantProblem(t, err, 401, problem.CodeUnauthenticated)
	_, _, err = e.svc.Refresh(ctx, tp2.RefreshToken, meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
	if list, _ := e.svc.ListSessions(ctx, u.ID, tp1.SessionID); len(list) != 0 {
		t.Errorf("sessions after reuse = %+v", list)
	}

	// an unknown refresh token is invalid_grant too; an expired one as well
	_, _, err = e.svc.Refresh(ctx, "nonsense", meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
	tp3, _ := e.svc.IssueTokens(ctx, u.ID, meta)
	e.clk.Advance(61 * 24 * time.Hour)
	_, _, err = e.svc.Refresh(ctx, tp3.RefreshToken, meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
}

func TestIntegrationRevokeMobileSessionAndAll(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Email: "all@example.org"})
	tp, _ := e.svc.IssueTokens(ctx, u.ID, meta)
	ws, _ := e.svc.IssueWebSession(ctx, u.ID, meta)

	// revoking the family kills access and refresh token together
	if found, err := e.svc.RevokeSession(ctx, u.ID, tp.SessionID); err != nil || !found {
		t.Fatalf("revoke: %v %v", found, err)
	}
	_, err := e.svc.Authenticate(ctx, identity.KindAccessToken, tp.AccessToken)
	wantProblem(t, err, 401, problem.CodeUnauthenticated)
	_, _, err = e.svc.Refresh(ctx, tp.RefreshToken, meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
	if _, err := e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token); err != nil {
		t.Fatalf("other session affected: %v", err)
	}

	tp2, _ := e.svc.IssueTokens(ctx, u.ID, meta)
	if err := e.svc.RevokeAllSessions(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token)
	wantProblem(t, err, 401, problem.CodeUnauthenticated)
	_, err = e.svc.Authenticate(ctx, identity.KindAccessToken, tp2.AccessToken)
	wantProblem(t, err, 401, problem.CodeUnauthenticated)
}

func TestIntegrationDisabledUserLosesSessions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Email: "dis@example.org", Password: "correct horse battery"})
	ws, _ := e.svc.IssueWebSession(ctx, u.ID, meta)
	if _, err := e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token); err != nil {
		t.Fatal(err)
	}
	// disabling goes through a superuser-level path in tests (no module method yet)
	if err := e.setStatus(ctx, u.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token)
	wantProblem(t, err, 401, problem.CodeUnauthenticated)
	_, err = e.svc.PasswordSignIn(ctx, "dis@example.org", "correct horse battery", meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
}

func (e *env) setStatus(ctx context.Context, userID uuid.UUID, status string) error {
	type inApp interface {
		InAppTx(ctx context.Context, fn func(pgx.Tx) error) error
	}
	return e.db.(inApp).InAppTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE "user" SET status = $2 WHERE id = $1`, userID, status)
		return err
	})
}

func TestIntegrationReauthGateAndPasswordChange(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Email: "re@example.org", Phone: "+905321234567", Password: "correct horse battery"})
	ws, _ := e.svc.IssueWebSession(ctx, u.ID, meta)
	other, _ := e.svc.IssueWebSession(ctx, u.ID, meta)
	p, _ := e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token)

	// not re-authenticated: the gate refuses, for the service and for authz helpers
	wantProblem(t, e.svc.ChangePassword(ctx, p, "a brand new password"), 403, problem.CodeReauthRequired)
	wantProblem(t, p.RequireReauth(e.clk.Now()), 403, problem.CodeReauthRequired)

	// wrong password does not open the window
	_, err := e.svc.Reauth(ctx, p, identity.ReauthPassword, "wrong password!!", meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
	wantProblem(t, e.svc.ChangePassword(ctx, p, "a brand new password"), 403, problem.CodeReauthRequired)

	until, err := e.svc.Reauth(ctx, p, identity.ReauthPassword, "correct horse battery", meta)
	if err != nil {
		t.Fatal(err)
	}
	if d := until.Sub(e.clk.Now()); d != 10*time.Minute {
		t.Errorf("window = %v", d)
	}
	// the next request's principal carries it
	p, _ = e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token)
	if !p.Reauthenticated(e.clk.Now()) || p.RequireReauth(e.clk.Now()) != nil {
		t.Fatal("principal not re-authenticated after Reauth")
	}
	if p2, _ := e.svc.Authenticate(ctx, identity.KindWebCookie, other.Token); p2.Reauthenticated(e.clk.Now()) {
		t.Error("another session got re-authenticated")
	}

	// policy
	if err := e.svc.ChangePassword(ctx, p, "short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := e.svc.ChangePassword(ctx, p, "a brand new password"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	// new password works, old one does not, other sessions are gone, this one stays
	if _, err := e.svc.PasswordSignIn(ctx, "re@example.org", "a brand new password", meta); err != nil {
		t.Fatalf("new password: %v", err)
	}
	_, err = e.svc.PasswordSignIn(ctx, "re@example.org", "correct horse battery", meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
	if _, err := e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token); err != nil {
		t.Errorf("current session was revoked: %v", err)
	}
	_, err = e.svc.Authenticate(ctx, identity.KindWebCookie, other.Token)
	wantProblem(t, err, 401, problem.CodeUnauthenticated)

	// the window ends
	e.clk.Advance(10*time.Minute + time.Second)
	p, _ = e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token)
	wantProblem(t, e.svc.ChangePassword(ctx, p, "yet another password"), 403, problem.CodeReauthRequired)

	// re-auth by SMS code to the account's own phone
	if err := e.requestOTP(ctx, "+905321234567", meta); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Reauth(ctx, p, identity.ReauthSMSOTP, lastCode(t, e.sms), meta); err != nil {
		t.Fatalf("reauth by otp: %v", err)
	}
}

func TestIntegrationSetFirstPasswordNeedsReauthByOTP(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Phone: "+905321234567"}) // no password yet
	ws, _ := e.svc.IssueWebSession(ctx, u.ID, meta)
	p, _ := e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token)
	_, err := e.svc.Reauth(ctx, p, identity.ReauthPassword, "anything at all", meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
	if err := e.requestOTP(ctx, "+905321234567", meta); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Reauth(ctx, p, identity.ReauthSMSOTP, lastCode(t, e.sms), meta); err != nil {
		t.Fatal(err)
	}
	p, _ = e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token)
	if err := e.svc.ChangePassword(ctx, p, "my first password"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PasswordSignIn(ctx, "+905321234567", "my first password", meta); err != nil {
		t.Fatalf("sign in with the new password: %v", err)
	}
}

func TestIntegrationMe(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Email: "me@example.org"})
	stranger := e.user(t, identity.CreateUserParams{Email: "stranger@example.org"})

	// no organizations yet
	me, err := e.svc.Me(ctx, u.ID)
	if err != nil || len(me.Organizations) != 0 || me.User.ID != u.ID {
		t.Fatalf("empty me = %+v, %v", me, err)
	}

	orgA, err := e.admin.CreateOrganization(ctx, platformadmin.CreateOrgParams{Slug: "alpha", Name: "Alpha Sitesi", AdminUserID: u.ID, AdminName: "Ayşe"})
	if err != nil {
		t.Fatal(err)
	}
	orgB, err := e.admin.CreateOrganization(ctx, platformadmin.CreateOrgParams{Slug: "beta", Name: "Beta Sitesi", AdminUserID: stranger.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.admin.CreateOrganization(ctx, platformadmin.CreateOrgParams{Slug: "alpha", Name: "dup", AdminUserID: u.ID}); err == nil {
		t.Error("duplicate slug accepted")
	}

	// seed more rights through the tenant transaction: a site-scoped
	// membership, an expired one and a future one (must not show), and a
	// person without roles in a third org
	site := id.New()
	err = e.db.InTenantTx(ctx, orgA.ID, func(tx pgx.Tx) error {
		for _, m := range []struct {
			role, scope string
			scopeID     *uuid.UUID
			from, to    string
			siteOwned   bool
		}{
			{"board_member", "site", &site, "now() - interval '1 day'", "NULL", true},
			{"auditor", "site", &site, "now() - interval '30 days'", "now() - interval '1 day'", false},
			{"accountant", "org", nil, "now() + interval '1 day'", "NULL", false},
		} {
			if _, err := tx.Exec(ctx, `INSERT INTO membership (id, org_id, person_id, role, scope_type, scope_id, valid_from, valid_to, site_owned)
				VALUES ($1, $2, $3, $4, $5, $6, `+m.from+`, `+m.to+`, $7)`, id.New(), orgA.ID, orgA.PersonID, m.role, m.scope, m.scopeID, m.siteOwned); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed memberships: %v", err)
	}

	me, err = e.svc.Me(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(me.Organizations) != 1 {
		t.Fatalf("organizations = %+v (user must not see beta)", me.Organizations)
	}
	o := me.Organizations[0]
	if o.ID != orgA.ID || o.Slug != "alpha" || o.Name != "Alpha Sitesi" {
		t.Errorf("org = %+v", o)
	}
	roles := map[string]identity.RoleGrant{}
	for _, r := range o.Roles {
		roles[r.Role] = r
	}
	if len(roles) != 2 {
		t.Fatalf("roles = %+v, want org_admin and board_member only", o.Roles)
	}
	if r := roles["org_admin"]; r.ScopeType != "org" || r.ScopeID != nil || r.SiteOwned {
		t.Errorf("org_admin = %+v", r)
	}
	if r := roles["board_member"]; r.ScopeType != "site" || r.ScopeID == nil || *r.ScopeID != site || !r.SiteOwned {
		t.Errorf("board_member = %+v", r)
	}
	_ = orgB
}

func TestIntegrationPrune(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Email: "prune@example.org", Password: "correct horse battery"})
	ws, _ := e.svc.IssueWebSession(ctx, u.ID, meta)
	_, _ = e.svc.PasswordSignIn(ctx, "prune@example.org", "wrong password!!", meta)
	if n, err := e.svc.Prune(ctx); err != nil || n != 0 {
		t.Fatalf("prune of fresh rows = %d, %v", n, err)
	}
	e.clk.Advance(100 * 24 * time.Hour)
	n, err := e.svc.Prune(ctx)
	if err != nil || n < 2 {
		t.Fatalf("prune = %d, %v; want the expired session and the old attempt", n, err)
	}
	_, err = e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token)
	wantProblem(t, err, 401, problem.CodeUnauthenticated)
}

var _ = http.StatusOK
var _ = authz.Principal{}
