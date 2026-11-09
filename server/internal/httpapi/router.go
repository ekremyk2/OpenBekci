// Package httpapi is the HTTP layer: router, middleware and health endpoints.
package httpapi

import (
	"github.com/openbekci/openbekci/server/internal/platform/obs"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	chatcore "github.com/openbekci/openbekci/server/internal/core/chat"
	"github.com/openbekci/openbekci/server/internal/core/community"
	"github.com/openbekci/openbekci/server/internal/core/devices"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/instance"
	"github.com/openbekci/openbekci/server/internal/core/integrations"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/privacy"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/packs"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/idempotency"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/webassets"
)

// Deps is everything handlers may need. It grows as modules arrive.
type Deps struct {
	Log  *slog.Logger
	DB   *db.DB
	Jobs *jobs.Client

	// Authenticator resolves the caller for operations with security
	// requirements. Nil means NoopAuthenticator (everything is 401); the
	// server wires SessionAuthenticator.
	Authenticator Authenticator
	// Idempotency stores Idempotency-Key requests. Nil disables enforcement
	// (the header is still validated).
	Idempotency idempotency.Store
	// Instance builds the discovery document.
	Instance *instance.Service
	// Identity serves /v1/auth and /v1/me.
	Identity *identity.Service
	// Devices serves /v1/me/devices (push registration). Nil answers with an
	// internal error (unit tests only).
	Devices *devices.Service
	// Authz resolves effective permissions; with Tenancy and Audit it serves
	// the /v1/orgs/{org_id} operations. Nil leaves those operations unresolved
	// (unit tests only).
	Authz   *authz.Resolver
	Tenancy *tenancy.Service
	People  *people.Service
	// Governance serves the 0.2 assembly operations.
	Governance *governance.Service
	// Community serves the community operations (bookings, visitors, guard console, parcels).
	Community *community.Service
	// Money are the money services (ledger, billing, payments, ...).
	Money MoneyDeps
	Audit *audit.Service
	// Access serves /v1/orgs/{org_id}/access-points, agents, credentials and events,
	// and the agent transport under /v1/agent. Nil leaves them unwired (unit tests).
	Access *access.Service
	// Integrations serves API keys and webhook endpoints. Nil answers their
	// operations with an internal error (unit tests only).
	Integrations *integrations.Service
	// Files serves the /v1/orgs/{org_id}/files operations. Nil answers them
	// with an internal error (unit tests only).
	Files *files.Service
	// Privacy serves consents, data export and erase. Nil answers those operations
	// with an internal error (unit tests only).
	Privacy *privacy.Service
	// Chat serves the chat channel (links, bot settings, provider webhooks). Nil
	// answers those operations with an internal error (unit tests only).
	Chat *chatcore.Service
	// Ops serves the daily-operations operations (tickets, announcements,
	// documents, notifications, dashboards, codes). Nil fields answer their
	// operations with an internal error (unit tests only).
	Ops OpsDeps
	// Packs serves the pack administration operations and the pack routes (webhooks and
	// callbacks); nil leaves them unwired (unit tests).
	Packs *packs.Host
	// InsecureCookies drops the Secure flag of the session cookie (INSECURE_DEV).
	InsecureCookies bool
	// ValidateResponses checks every response against the spec (dev aid).
	ValidateResponses bool
}

// Options configures NewRouter.
type Options struct {
	TrustedProxies []netip.Prefix
	// RequestTimeout bounds each request; zero means 30 seconds.
	RequestTimeout time.Duration
	// MaxBodyBytes caps request bodies; zero means DefaultMaxBodyBytes.
	MaxBodyBytes int64
	// MaxUploadBytes caps the body of the file upload request (the multipart
	// framing included); zero means DefaultMaxUploadBytes. The file module
	// enforces its own per-file limit while streaming.
	MaxUploadBytes int64
	// Ready are the probes behind /readyz.
	Ready []Check
	// MetricsToken is the bearer token of /metrics; empty leaves /metrics unmounted.
	MetricsToken string
}

// NewRouter builds the root handler.
func NewRouter(deps Deps, o Options) http.Handler {
	if o.RequestTimeout == 0 {
		o.RequestTimeout = 30 * time.Second
	}
	if o.MaxBodyBytes == 0 {
		o.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if o.MaxUploadBytes == 0 {
		o.MaxUploadBytes = DefaultMaxUploadBytes
	}
	r := chi.NewRouter()
	r.Use(
		middleware.RequestID,
		obs.HTTP,
		realIP(o.TrustedProxies),
		requestLogger(deps.Log),
		recoverer(deps.Log),
		maxBody(o.MaxBodyBytes, o.MaxUploadBytes, MaxUploadBytes+1<<20),
		requestTimeout(o.RequestTimeout),
		noStoreSensitive,
		securityHeaders,
	)

	r.Get("/healthz", healthz)
	r.Get("/readyz", readyz(deps.Log, o.Ready))
	if o.MetricsToken != "" {
		r.Method(http.MethodGet, "/metrics", obs.Handler(o.MetricsToken))
	}

	r.MethodNotAllowed(methodNotAllowed(r))
	MountAPI(r, deps)

	// Anything no route claims: unknown API paths are problems, the rest is the web app.
	web := webassets.Handler()
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if isAPIPath(req.URL.Path) {
			notFound(w, req)
			return
		}
		web.ServeHTTP(w, req)
	})
	return r
}
