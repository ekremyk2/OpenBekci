package httpapi

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/platform/ratelimit"
)

var sixDigits = regexp.MustCompile(`\b[0-9]{6}\b`)

// requestIDs matches the per-request fields of a problem body, the only part that may differ.
var requestIDs = regexp.MustCompile(`"(instance|request_id)":"[^"]*"`)

// signUpEnv is a world with one site, a join code for it and a unit to claim.
type signUpEnv struct {
	*pb
	code, unit, site string
	fix              authztest.SiteFix
}

func newSignUpEnv(t *testing.T) *signUpEnv {
	t.Helper()
	p := newPB(t)
	f := p.w.NewSite(t, p.org)
	jc := p.must(p.call("POST", p.base()+"/sites/"+f.Site.String()+"/join-code:rotate", "", p.adm), 200).Body
	return &signUpEnv{pb: p, code: str(jc, "code"), unit: f.Unit.String(), site: f.Site.String(), fix: f}
}

func (s *signUpEnv) signUp(code, body string) resp {
	s.t.Helper()
	return s.call("POST", "/v1/join/"+code+":sign-up", body, "")
}

func (s *signUpEnv) drain() {
	s.t.Helper()
	if err := s.e.svc.Drain(s.t.Context()); err != nil {
		s.t.Fatal(err)
	}
}

func (s *signUpEnv) lastCode() string {
	s.t.Helper()
	s.drain()
	m, ok := s.e.sms.Last()
	if !ok {
		s.t.Fatal("no SMS was sent")
	}
	c := sixDigits.FindString(m.Text)
	if c == "" {
		s.t.Fatalf("no code in %q", m.Text)
	}
	return c
}

func (s *signUpEnv) userCount(phoneOrEmail string) int {
	s.t.Helper()
	var n int
	err := s.e.db.InPlatformTx(s.t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(s.t.Context(), `SELECT count(*) FROM "user" WHERE phone = $1 OR email = $1`, phoneOrEmail).Scan(&n)
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return n
}

func TestIntegrationJoinSignUpThroughClaim(t *testing.T) {
	s := newSignUpEnv(t)
	const phone = "+905321110022"

	// a new resident signs up with the national form of the number and a name
	r := s.signUp(s.code, `{"phone":"0532 111 00 22","name":"Ayse Yilmaz"}`)
	s.must(r, 202)
	// the account is created in the background, before the code is sent
	code := s.lastCode()
	var u identity.User
	var found bool
	var err error
	if u, found, err = s.e.svc.FindUser(t.Context(), phone); err != nil || !found || u.Status != "pending" {
		t.Fatalf("after the request: found=%v status=%q err=%v, want a pending account", found, u.Status, err)
	}
	// a pending account has no password and no other way in
	s.must(s.call("POST", "/v1/auth/token", fmt.Sprintf(`{"method":"password","identifier":%q,"password":"whatever-it-is"}`, phone), ""), 401)
	s.must(s.call("POST", "/v1/auth/token", fmt.Sprintf(`{"method":"sms_otp","identifier":%q,"code":"000000"}`, phone), ""), 401)

	// the existing OTP verify completes it into a session with no organization yet
	tok := s.must(s.call("POST", "/v1/auth/token", fmt.Sprintf(`{"method":"sms_otp","identifier":%q,"code":%q}`, phone, code), ""), 200).Body
	access := str(tok, "access_token")
	if u, _, _ = s.e.svc.FindUser(t.Context(), phone); u.Status != "active" {
		t.Fatalf("status after verify = %q, want active", u.Status)
	}
	me := s.must(s.call("GET", "/v1/me", "", access), 200).Body
	if len(me["organizations"].([]any)) != 0 || len(me["occupancies"].([]any)) != 0 {
		t.Fatalf("a new sign-up has no organization: %v", me)
	}
	// no claims yet
	if n := len(items(s.must(s.call("GET", "/v1/me/unit-claims", "", access), 200))); n != 0 {
		t.Fatalf("claims before any = %d", n)
	}

	// the claim uses the join code as today; the person carries the name from the sign-up
	claim := s.must(s.call("POST", "/v1/join/"+s.code+"/claims", fmt.Sprintf(`{"unit_id":%q,"claimed_role":"owner"}`, s.unit), access), 201).Body
	list := items(s.must(s.call("GET", "/v1/me/unit-claims", "", access), 200))
	if len(list) != 1 {
		t.Fatalf("claims = %d, want 1", len(list))
	}
	c := list[0]
	if str(c, "id") != str(claim, "id") || str(c, "status") != "pending" || str(c, "claimed_role") != "owner" ||
		str(c, "display_name") != "Ayse Yilmaz" || str(c, "site_name") == "" || str(c, "org_name") == "" || str(c, "unit_door_number") == "" ||
		str(c, "site_id") != s.site || str(c, "unit_id") != s.unit || str(c, "org_id") != s.org.ID.String() {
		t.Fatalf("pending claim = %v", c)
	}
	if c["decided_at"] != nil || c["decision_note"] != nil {
		t.Fatalf("a pending claim is undecided: %v", c)
	}

	// approved, then a second claim that is rejected: every status is listed and filterable
	s.must(s.call("POST", s.base()+"/unit-claims/"+str(claim, "id")+":approve", `{}`, s.adm), 200)
	unit2 := s.w.NewUnit(t, s.fix)
	claim2 := s.must(s.call("POST", "/v1/join/"+s.code+"/claims", fmt.Sprintf(`{"unit_id":%q,"claimed_role":"tenant"}`, unit2), access), 201).Body
	s.must(s.call("POST", s.base()+"/unit-claims/"+str(claim2, "id")+":reject", `{"reason":"not a resident"}`, s.adm), 200)
	byStatus := map[string]int{}
	for _, c := range items(s.must(s.call("GET", "/v1/me/unit-claims", "", access), 200)) {
		byStatus[str(c, "status")]++
	}
	if byStatus["approved"] != 1 || byStatus["rejected"] != 1 || len(byStatus) != 2 {
		t.Fatalf("claims by status = %v", byStatus)
	}
	for status, want := range map[string]int{"approved": 1, "rejected": 1, "pending": 0} {
		got := items(s.must(s.call("GET", "/v1/me/unit-claims?status="+status, "", access), 200))
		if len(got) != want {
			t.Errorf("status=%s: %d claims, want %d", status, len(got), want)
		}
		for _, c := range got {
			if str(c, "status") != status {
				t.Errorf("status=%s returned %v", status, c)
			}
		}
	}
	rej := items(s.must(s.call("GET", "/v1/me/unit-claims?status=rejected", "", access), 200))[0]
	if str(rej, "decision_note") == "" || rej["decided_at"] == nil {
		t.Errorf("a rejected claim shows when and why: %v", rej)
	}
	s.must(s.call("GET", "/v1/me/unit-claims?status=bogus", "", access), 422)
	first := s.must(s.call("GET", "/v1/me/unit-claims?limit=1", "", access), 200).Body
	if len(first["items"].([]any)) != 1 || first["next_cursor"] == nil {
		t.Fatalf("paging: %v", first)
	}
	// claims are private: another account sees none of them, an anonymous caller is refused
	other := s.w.Token(t, s.w.Actor("no_membership"))
	if n := len(items(s.must(s.call("GET", "/v1/me/unit-claims", "", other), 200))); n != 0 {
		t.Errorf("another account sees %d claims", n)
	}
	s.must(s.call("GET", "/v1/me/unit-claims", "", ""), 401)

	// the sign-up is audited against the site, without the contact
	var n int
	var diff string
	err = s.e.db.InPlatformTx(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `SELECT count(*), COALESCE(max(diff::text), '') FROM audit_event WHERE org_id = $1 AND site_id = $2 AND action = 'join.sign_up' AND target_type = 'site'`,
			s.org.ID, s.site).Scan(&n, &diff)
	})
	if err != nil || n != 1 || strings.Contains(diff, "532") || strings.Contains(diff, "Ayse") || !strings.Contains(diff, "channel") {
		t.Errorf("audit: %d events (%v), diff %q", n, err, diff)
	}
}

func TestIntegrationJoinSignUpExistingContactIsIndistinguishable(t *testing.T) {
	s := newSignUpEnv(t)
	known := s.e.user(identity.CreateUserParams{Phone: "+905321234567", Email: "known@example.org"})

	k := s.signUp(s.code, `{"phone":"+905321234567","name":"Someone Else"}`)
	u := s.signUp(s.code, `{"phone":"+905559998877","name":"Someone Else"}`)
	if k.Code != 202 || u.Code != 202 || k.Raw != u.Raw {
		t.Fatalf("known %d %q, unknown %d %q", k.Code, k.Raw, u.Code, u.Raw)
	}
	s.drain()
	msgs := s.e.sms.Messages()
	if len(msgs) != 2 {
		t.Fatalf("%d messages, want one each", len(msgs))
	}
	// the sends run in the background, in no fixed order: put the known number's first
	if msgs[0].To != "+905321234567" {
		msgs[0], msgs[1] = msgs[1], msgs[0]
	}
	// the same message either way: the known account got its ordinary sign-in code
	mask := func(s string) string { return sixDigits.ReplaceAllString(s, "NNNNNN") }
	if mask(msgs[0].Text) != mask(msgs[1].Text) {
		t.Errorf("messages differ: %q vs %q", msgs[0].Text, msgs[1].Text)
	}
	// no second account for the known contact, its name and status untouched
	if n := s.userCount("+905321234567"); n != 1 {
		t.Errorf("accounts for the known number = %d", n)
	}
	if got, _, _ := s.e.svc.FindUser(t.Context(), "+905321234567"); got.ID != known.ID || got.Status != "active" {
		t.Errorf("known account changed: %+v", got)
	}
	// and its code signs in the existing account
	tok := s.must(s.call("POST", "/v1/auth/token", fmt.Sprintf(`{"method":"sms_otp","identifier":"+905321234567","code":%q}`, sixDigits.FindString(msgs[0].Text)), ""), 200).Body
	if str(tok["user"].(map[string]any), "id") != known.ID.String() {
		t.Errorf("signed in as %v, want the existing account", tok["user"])
	}

	// the same through email: a known address and a new one answer alike
	k = s.signUp(s.code, `{"email":"known@example.org"}`)
	u = s.signUp(s.code, `{"email":"fresh@example.org"}`)
	if k.Code != 202 || u.Code != 202 || k.Raw != u.Raw {
		t.Fatalf("email: known %d %q, unknown %d %q", k.Code, k.Raw, u.Code, u.Raw)
	}
	s.drain()
	if n := len(s.e.mail.Messages()); n != 2 {
		t.Fatalf("%d mails, want 2", n)
	}
	if n := s.userCount("known@example.org"); n != 1 {
		t.Errorf("accounts for the known address = %d", n)
	}
	// a repeated sign-up of a new contact does not duplicate it either
	s.signUp(s.code, `{"phone":"+905550001122"}`)
	s.signUp(s.code, `{"phone":"+905550001122"}`)
	s.drain()
	if n := s.userCount("+905550001122"); n != 1 {
		t.Errorf("accounts for a twice signed up number = %d", n)
	}

	// a disabled account gets nothing, and still answers 202
	dis := s.e.user(identity.CreateUserParams{Phone: "+905320000099"})
	if err := s.e.db.InPlatformTx(t.Context(), func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `UPDATE "user" SET status = 'disabled' WHERE id = $1`, dis.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before := len(s.e.sms.Messages())
	s.must(s.signUp(s.code, `{"phone":"+905320000099"}`), 202)
	s.drain()
	if len(s.e.sms.Messages()) != before {
		t.Error("a disabled account was sent a code")
	}
}

func TestIntegrationJoinSignUpEmailMobileLink(t *testing.T) {
	s := newSignUpEnv(t)
	s.must(s.signUp(s.code, `{"email":"New.Resident@Example.org","name":"Veli","client":"mobile"}`), 202)
	s.drain()
	m, ok := s.e.mail.Last()
	if !ok {
		t.Fatal("no mail")
	}
	link := regexp.MustCompile(`openbekci://\S+`).FindString(m.Body)
	if link == "" {
		t.Fatalf("no mobile link in %q", m.Body)
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "openbekci" || u.Host != "bekci.example.org" || u.Path != "/login/magic" || u.Query().Get("token") == "" {
		t.Fatalf("link %q parses to %+v (%v)", link, u, err)
	}
	// the link completes the sign-up
	tok := s.must(s.call("POST", "/v1/auth/magic-link/consume", fmt.Sprintf(`{"token":%q,"client":"mobile"}`, u.Query().Get("token")), ""), 200).Body
	if str(tok, "access_token") == "" {
		t.Fatalf("no session: %v", tok)
	}
	got, found, _ := s.e.svc.FindUser(t.Context(), "new.resident@example.org")
	if !found || got.Status != "active" {
		t.Errorf("account after the link: found=%v %+v", found, got)
	}
}

func TestIntegrationJoinSignUpBadCodeIsIdentical404(t *testing.T) {
	s := newSignUpEnv(t)
	old := s.code
	rot := s.must(s.call("POST", s.base()+"/sites/"+s.site+"/join-code:rotate", "", s.adm), 200).Body
	if str(rot, "code") == old {
		t.Fatal("rotation did not change the code")
	}
	// a disabled code
	s.must(s.call("PUT", s.base()+"/sites/"+s.site+"/join-code", `{"enabled":false}`, s.adm), 200)

	body := `{"phone":"+905321110033"}`
	var bodies []string
	for _, code := range []string{"not-a-code", "x.y", old, str(rot, "code"), s.code[:strings.Index(s.code, ".")+1] + "AAAAAAAAAAAAAAAAAAAAAA"} {
		r := s.signUp(code, body)
		if r.Code != 404 {
			t.Fatalf("code %q: %d %s", code, r.Code, r.Raw)
		}
		bodies = append(bodies, requestIDs.ReplaceAllString(r.Raw, `"$1":"-"`))
	}
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Errorf("404 bodies differ:\n%s\n%s", bodies[0], b)
		}
	}
	// the same answer as the join read, and nothing was sent or created
	read := s.call("GET", "/v1/join/"+old, "", "")
	if read.Code != 404 || requestIDs.ReplaceAllString(read.Raw, `"$1":"-"`) != bodies[0] {
		t.Errorf("join read: %d %s, want the sign-up's %s", read.Code, read.Raw, bodies[0])
	}
	s.drain()
	if len(s.e.sms.Messages()) != 0 || s.userCount("+905321110033") != 0 {
		t.Error("a refused sign-up sent a message or created an account")
	}
	// a valid code with a bad body is a validation error, not a 404
	s.must(s.call("PUT", s.base()+"/sites/"+s.site+"/join-code", `{"enabled":true}`, s.adm), 200)
	for _, bad := range []string{`{}`, `{"phone":"abc"}`, `{"email":"nope"}`, `{"phone":"+905321110033","email":"a@example.org"}`, `{"phone":"+905321110033","locale":"xx"}`} {
		s.must(s.signUp(str(rot, "code"), bad), 422)
	}
}

func TestIntegrationJoinSignUpRateLimits(t *testing.T) {
	s := newSignUpEnv(t)
	// per contact: the budget of one number is that of requestOtp (3 in 2 minutes)
	var codes []int
	for i := 0; i < 5; i++ {
		codes = append(codes, s.signUp(s.code, `{"phone":"+905321110044"}`).Code)
	}
	if fmt.Sprint(codes) != "[202 202 202 429 429]" {
		t.Errorf("one number: %v, want three accepted then limited", codes)
	}
	// and the budget is shared with requestOtp, so the two cannot be alternated to get more
	s.must(s.call("POST", "/v1/auth/otp", `{"phone":"+905321110044"}`, ""), 429)
	// per client address: 10 a minute across contacts
	limited := 0
	for i := 0; i < 14; i++ {
		if s.signUp(s.code, fmt.Sprintf(`{"phone":"+9053222200%02d"}`, i)).Code == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Error("one address signed up 14 numbers without being limited")
	}
}

// The limit per join code holds whoever asks: other addresses and contacts do not reset it.
func TestIntegrationJoinSignUpPerCodeLimit(t *testing.T) {
	s := newSignUpEnv(t)
	lim := identity.Limits{SignUpCode: ratelimit.New(2, time.Hour)}
	svc := identity.NewService(s.e.db, identity.Config{InstanceName: "Test", BaseURL: testBase, SecretKey: []byte("0123456789abcdef0123456789abcdef"), SMS: s.e.sms, Limits: &lim})
	for i, want := range []bool{true, true, false} {
		err := svc.RequestSignUp(t.Context(), identity.SignUpInput{Code: s.code, Phone: fmt.Sprintf("+90533333330%d", i)}, identity.Meta{IP: fmt.Sprintf("198.51.100.%d", i)})
		if (err == nil) != want {
			t.Fatalf("sign-up %d: err = %v, want accepted=%v", i, err, want)
		}
	}
	if err := svc.RequestSignUp(t.Context(), identity.SignUpInput{Code: "another.code", Phone: "+905334444440"}, identity.Meta{IP: "198.51.100.9"}); err != nil {
		t.Errorf("another code has its own budget: %v", err)
	}
	if err := svc.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
}
