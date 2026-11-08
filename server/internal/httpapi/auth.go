package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

const (
	// CookieName is the browser session cookie (backend plan section 6).
	CookieName = "obk_session"
	// CSRFHeader and CSRFHeaderValue are required on cookie-authenticated mutations.
	CSRFHeader      = "X-Requested-With"
	CSRFHeaderValue = "openbekci-web"
)

// SessionAuthenticator authenticates requests by opaque session token: the
// `obk_session` cookie (web) or `Authorization: Bearer` (mobile and
// integrations). Cookie-authenticated mutations also pass the CSRF check.
type SessionAuthenticator struct {
	Identity *identity.Service
	// Origins are the browser origins allowed to mutate with a cookie:
	// BASE_URL and EXTRA_ORIGINS.
	Origins []string
}

// Authenticate implements Authenticator.
func (a SessionAuthenticator) Authenticate(r *http.Request) (*authz.Principal, error) {
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, token, ok := strings.Cut(h, " ")
		token = strings.TrimSpace(token)
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
			return nil, problem.Unauthenticated("the Authorization header is not a Bearer token")
		}
		return a.Identity.Authenticate(r.Context(), identity.KindAccessToken, token)
	}
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil, problem.Unauthenticated("authentication is required")
	}
	if !safeMethod(r.Method) {
		if err := CheckCSRF(r, a.Origins); err != nil {
			return nil, err
		}
	}
	return a.Identity.Authenticate(r.Context(), identity.KindWebCookie, c.Value)
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// CheckCSRF enforces the rules for mutations made with a cookie session
// (backend plan section 6): the Origin header must be one of the allowed
// origins and X-Requested-With must be "openbekci-web". Bearer-token requests
// are not subject to it (browsers do not attach those on their own).
func CheckCSRF(r *http.Request, allowed []string) *problem.Error {
	origin := r.Header.Get("Origin")
	if origin == "" || !originAllowed(origin, allowed) {
		return problem.New(http.StatusForbidden, problem.CodeCSRFFailed, "the request origin is not allowed")
	}
	if r.Header.Get(CSRFHeader) != CSRFHeaderValue {
		return problem.New(http.StatusForbidden, problem.CodeCSRFFailed, "the "+CSRFHeader+" header is missing")
	}
	return nil
}

func originAllowed(origin string, allowed []string) bool {
	o := normalizeOrigin(origin)
	if o == "" {
		return false
	}
	for _, a := range allowed {
		if normalizeOrigin(a) == o {
			return true
		}
	}
	return false
}

// normalizeOrigin reduces a URL to scheme://host[:port] in lower case, or ""
// when it has no host (such as the literal Origin "null").
func normalizeOrigin(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// reqInfo is what handlers of the strict server need from the HTTP request,
// which the generated interface does not pass them.
type reqInfo struct {
	w  http.ResponseWriter
	ip string
	ua string
}

type reqInfoKey struct{}

// requestInfo stores the client address, user agent and response writer in the
// context of strict handlers.
func requestInfo(next gen.StrictHandlerFunc, _ string) gen.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ctx = context.WithValue(ctx, reqInfoKey{}, &reqInfo{w: w, ip: host, ua: r.UserAgent()})
		return next(ctx, w, r, request)
	}
}

func infoFrom(ctx context.Context) *reqInfo {
	ri, _ := ctx.Value(reqInfoKey{}).(*reqInfo)
	if ri == nil {
		return &reqInfo{}
	}
	return ri
}

// authCookie is the session cookie for a token valid until expires. Secure
// unless the instance runs in INSECURE_DEV (http on localhost).
func authCookie(token string, expires time.Time, insecure bool) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // Secure is on except for INSECURE_DEV on localhost
		Name: CookieName, Value: token, Path: "/", Expires: expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true, Secure: !insecure, SameSite: http.SameSiteLaxMode,
	}
}

func clearedCookie(insecure bool) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // Secure is on except for INSECURE_DEV on localhost
		Name: CookieName, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(0, 0), HttpOnly: true, Secure: !insecure, SameSite: http.SameSiteLaxMode}
}
