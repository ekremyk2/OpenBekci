// Package identity is the sign-in account module: users, opaque sessions
// (web cookie, access and refresh tokens), password, SMS one-time code and
// magic-link authentication, re-authentication and GET /v1/me (backend plan
// sections 4.2, 6 and 16).
//
// The module returns *problem.Error values; the HTTP layer renders them.
package identity

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/instance"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/platform/ratelimit"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// TxRunner is the database access the module needs, satisfied by *db.DB.
// InAppTx serves the instance-wide identity tables; InPlatformTx is used only
// for the cross-organization part of GET /v1/me.
type TxRunner interface {
	InAppTx(ctx context.Context, fn func(pgx.Tx) error) error
	InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// Defaults of the session and code lifetimes (backend plan section 6).
const (
	DefaultWebSessionTTL = 30 * 24 * time.Hour
	DefaultAccessTTL     = 15 * time.Minute
	DefaultRefreshTTL    = 60 * 24 * time.Hour
	DefaultOTPTTL        = 5 * time.Minute
	DefaultOTPAttempts   = 5
	DefaultLinkTTL       = 15 * time.Minute
	DefaultReauthWindow  = 10 * time.Minute
	DefaultWebLoginTTL   = 2 * time.Minute
	// MinPasswordLength is the minimum password length in characters.
	MinPasswordLength = 10
	// MaxPasswordLength bounds the input handed to argon2.
	MaxPasswordLength = 256

	touchInterval = 5 * time.Minute
)

// Config configures the Service. Zero values take the defaults above.
type Config struct {
	// InstanceName appears in messages.
	InstanceName string
	// BaseURL is the public URL without trailing slash; link base.
	BaseURL string
	// SecretKey keys the HMAC of one-time codes.
	SecretKey []byte
	// SMS is the gateway for sign-in codes; nil disables SMS sign-in.
	SMS sms.Gateway
	// Mail sends magic links; nil disables magic links.
	Mail mail.Sender
	// DefaultCountryCode is the calling code assumed for national phone
	// numbers without a leading plus ("90" for Turkey).
	DefaultCountryCode string

	WebSessionTTL time.Duration
	AccessTTL     time.Duration
	RefreshTTL    time.Duration
	OTPTTL        time.Duration
	OTPAttempts   int
	LinkTTL       time.Duration
	ReauthWindow  time.Duration
	// WebLoginTTL is how long a web-login link stays valid (default 2 minutes).
	WebLoginTTL time.Duration

	// LockoutThreshold is the number of consecutive failures per account and
	// client address after which that address is refused for a growing time.
	LockoutThreshold int
	// LockoutGlobalThreshold is the number of consecutive failures per
	// account, from any addresses, after which everyone is refused for a
	// growing time. It is higher than LockoutThreshold so that one address
	// cannot lock the owner out (see lockout.go for the trade-off); default
	// five times LockoutThreshold.
	LockoutGlobalThreshold int

	// Send budgets (used only when Limits is nil): messages per identifier per
	// day, SMS per hour and mail per hour for the whole instance. Zero takes the
	// default (10, 120, 300), a negative value removes the limit.
	SendDailyBudget  int
	SMSHourlyBudget  int
	MailHourlyBudget int

	Limits *Limits
	Log    *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Limits are the in-memory abuse limiters (backend plan sections 8 and 16).
// All buckets are per instance, not shared between processes.
type Limits struct {
	// OTPSendIP and OTPSendTarget limit SMS code requests per client address
	// and per phone number.
	OTPSendIP, OTPSendTarget *ratelimit.Limiter
	// LinkSendIP and LinkSendTarget limit magic-link requests per address and per e-mail.
	LinkSendIP, LinkSendTarget *ratelimit.Limiter
	// AuthIP limits credential checks (password, code verification, link
	// consumption, refresh) per client address.
	AuthIP *ratelimit.Limiter
	// WebLinkUser limits web-login link issuance per signed-in user.
	WebLinkUser *ratelimit.Limiter
	// SendDaily is the daily send budget per identifier (a phone number or
	// an address, whatever the purpose), on top of the short-term buckets.
	SendDaily *ratelimit.Limiter
	// SMSHourly and MailHourly are the per-instance hourly budgets of the SMS
	// and mail gateways (a runaway or an attack costs money and gets the
	// sender blocked). They are spent only by messages that are really sent,
	// so requests for unknown accounts cannot use them up.
	SMSHourly, MailHourly *ratelimit.Limiter
}

// Default send budgets (Config.SendDailyBudget, SMSHourlyBudget, MailHourlyBudget).
const (
	DefaultSendDailyBudget  = 10
	DefaultSMSHourlyBudget  = 120
	DefaultMailHourlyBudget = 300
)

// DefaultLimits returns the production limits with the default send budgets.
func DefaultLimits(now func() time.Time) *Limits { return newLimits(Config{}, now) }

// budget resolves a configured budget: zero takes def, negative means unlimited.
func budget(v, def int) int {
	if v == 0 {
		return def
	}
	return max(v, 0)
}

func newLimits(cfg Config, now func() time.Time) *Limits {
	daily := budget(cfg.SendDailyBudget, DefaultSendDailyBudget)
	sms := budget(cfg.SMSHourlyBudget, DefaultSMSHourlyBudget)
	mailB := budget(cfg.MailHourlyBudget, DefaultMailHourlyBudget)
	// a bucket that holds `n` and refills one every period/n: n events per period
	per := func(n int, period time.Duration) *ratelimit.Limiter {
		if n <= 0 {
			return nil // unlimited
		}
		return ratelimit.NewWithClock(n, period/time.Duration(n), now)
	}
	return &Limits{
		OTPSendIP:      ratelimit.NewWithClock(10, time.Minute, now),
		OTPSendTarget:  ratelimit.NewWithClock(3, 2*time.Minute, now),
		LinkSendIP:     ratelimit.NewWithClock(10, time.Minute, now),
		LinkSendTarget: ratelimit.NewWithClock(3, 2*time.Minute, now),
		AuthIP:         ratelimit.NewWithClock(30, 2*time.Second, now),
		WebLinkUser:    ratelimit.NewWithClock(5, 30*time.Second, now),
		SendDaily:      per(daily, 24*time.Hour),
		SMSHourly:      per(sms, time.Hour),
		MailHourly:     per(mailB, time.Hour),
	}
}

// Service is the identity module.
type Service struct {
	db  TxRunner
	cfg Config
	log *slog.Logger
	now func() time.Time

	send *sender
}

// NewService returns a Service.
func NewService(db TxRunner, cfg Config) *Service {
	if cfg.WebSessionTTL == 0 {
		cfg.WebSessionTTL = DefaultWebSessionTTL
	}
	if cfg.AccessTTL == 0 {
		cfg.AccessTTL = DefaultAccessTTL
	}
	if cfg.RefreshTTL == 0 {
		cfg.RefreshTTL = DefaultRefreshTTL
	}
	if cfg.OTPTTL == 0 {
		cfg.OTPTTL = DefaultOTPTTL
	}
	if cfg.OTPAttempts == 0 {
		cfg.OTPAttempts = DefaultOTPAttempts
	}
	if cfg.LinkTTL == 0 {
		cfg.LinkTTL = DefaultLinkTTL
	}
	if cfg.WebLoginTTL == 0 {
		cfg.WebLoginTTL = DefaultWebLoginTTL
	}
	if cfg.ReauthWindow == 0 {
		cfg.ReauthWindow = DefaultReauthWindow
	}
	if cfg.LockoutThreshold == 0 {
		cfg.LockoutThreshold = 5
	}
	if cfg.LockoutGlobalThreshold < cfg.LockoutThreshold {
		cfg.LockoutGlobalThreshold = 5 * cfg.LockoutThreshold
	}
	if cfg.DefaultCountryCode == "" {
		cfg.DefaultCountryCode = "90"
	}
	if cfg.InstanceName == "" {
		cfg.InstanceName = "OpenBekci"
	}
	s := &Service{db: db, cfg: cfg, log: cfg.Log, now: cfg.Now, send: newSender()}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.cfg.Limits == nil {
		s.cfg.Limits = newLimits(cfg, s.now)
	}
	return s
}

// SMSEnabled reports whether SMS sign-in is available.
func (s *Service) SMSEnabled() bool { return s.cfg.SMS != nil }

// MailEnabled reports whether magic links are available.
func (s *Service) MailEnabled() bool { return s.cfg.Mail != nil }

// ReauthWindow is how long a re-authentication stays valid.
func (s *Service) ReauthWindow() time.Duration { return s.cfg.ReauthWindow }

// AuthMethods implements instance.AuthMethodSource: password always, SMS
// code when a gateway is configured, magic link when mail is.
func (s *Service) AuthMethods(context.Context) []instance.AuthMethod {
	out := []instance.AuthMethod{{ID: instance.MethodPassword}}
	if s.SMSEnabled() {
		out = append(out, instance.AuthMethod{ID: instance.MethodSMSOTP})
	}
	if s.MailEnabled() {
		out = append(out, instance.AuthMethod{ID: instance.MethodMagicLink})
	}
	return out
}

var _ instance.AuthMethodSource = (*Service)(nil)

// User is a sign-in account.
type User struct {
	ID              uuid.UUID
	Email           *string
	Phone           *string
	Locale          string
	Status          string
	IsPlatformAdmin bool
	HasPassword     bool
}

// Meta describes the client of a request, stored with sessions and attempts.
type Meta struct {
	IP         string
	UserAgent  string
	DeviceName string
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Do not cut a multi-byte character in half.
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Client says which kind of application asked for a link.
type Client string

// Clients. Mobile links use the app scheme instead of the web URL.
const (
	ClientWeb    Client = "web"
	ClientMobile Client = "mobile"
)

// LinkKind selects one of the link formats of backend plan section 6.
type LinkKind string

// Link kinds.
const (
	LinkMagic  LinkKind = "magic"  // {BASE_URL}/login/magic?token=...
	LinkInvite LinkKind = "invite" // {BASE_URL}/invite/{token}
	LinkReset  LinkKind = "reset"  // {BASE_URL}/reset/{token}
	// LinkWebLogin is {BASE_URL}/login/link/{token}; it is always a web URL
	// (its purpose is to open the browser).
	LinkWebLogin LinkKind = "weblogin"
)

// LinkURL builds a link for token. The web form is under baseURL; the mobile
// form is the same path under the openbekci:// scheme.
func LinkURL(baseURL string, client Client, kind LinkKind, token string) string {
	var path string
	switch kind {
	case LinkInvite:
		path = "invite/" + token
	case LinkReset:
		path = "reset/" + token
	case LinkWebLogin:
		return strings.TrimRight(baseURL, "/") + "/login/link/" + token
	default:
		path = "login/magic?token=" + token
	}
	if client == ClientMobile {
		return "openbekci://" + path
	}
	return strings.TrimRight(baseURL, "/") + "/" + path
}

// NormalizeEmail trims and lower-cases an address and rejects anything that
// is not plausibly one address.
func NormalizeEmail(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 || at != strings.LastIndexByte(s, '@') || len(s) > 254 || strings.ContainsAny(s, " \t\r\n,;<>") {
		return "", false
	}
	if !strings.Contains(s[at+1:], ".") {
		return "", false
	}
	return s, true
}

// NormalizePhone converts a phone number to E.164. A leading plus or 00 is
// taken as an international number; otherwise the number is national and
// gets defaultCC ("90"), with one leading trunk 0 removed.
func NormalizePhone(s, defaultCC string) (string, bool) {
	var b strings.Builder
	plus := false
	for i, r := range strings.TrimSpace(s) {
		switch {
		case r == '+' && i == 0:
			plus = true
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '(' || r == ')' || r == '.':
		default:
			return "", false
		}
	}
	d := b.String()
	switch {
	case plus:
	case strings.HasPrefix(d, "00"):
		d = d[2:]
	default:
		d = defaultCC + strings.TrimPrefix(d, "0")
	}
	if len(d) < 7 || len(d) > 15 || d[0] == '0' {
		return "", false
	}
	return "+" + d, true
}

// ValidatePassword enforces the password policy: at least 10 and at most 256
// characters.
func ValidatePassword(pw string) *problem.Error {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < MinPasswordLength:
		return problem.Validation(problem.Field{Field: "new_password", Code: "too_short", Params: map[string]any{"min": MinPasswordLength}})
	case n > MaxPasswordLength:
		return problem.Validation(problem.Field{Field: "new_password", Code: "too_long", Params: map[string]any{"max": MaxPasswordLength}})
	}
	return nil
}

func errInvalidGrant() *problem.Error {
	return problem.New(401, problem.CodeInvalidGrant, "the credentials or code are not valid")
}
