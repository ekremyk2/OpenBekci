// Package httpapi is the HTTP layer: router, middleware and health endpoints.
package httpapi

import (
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/openbekci/openbekci/server/internal/core/instance"
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
	// requirements. Nil means NoopAuthenticator (everything is 401 until the
	// sessions task wires the real one).
	Authenticator Authenticator
	// Idempotency stores Idempotency-Key requests. Nil disables enforcement
	// (the header is still validated).
	Idempotency idempotency.Store
	// Instance builds the discovery document.
	Instance *instance.Service
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
	// Ready are the probes behind /readyz.
	Ready []Check
}

// NewRouter builds the root handler.
func NewRouter(deps Deps, o Options) http.Handler {
	if o.RequestTimeout == 0 {
		o.RequestTimeout = 30 * time.Second
	}
	if o.MaxBodyBytes == 0 {
		o.MaxBodyBytes = DefaultMaxBodyBytes
	}
	r := chi.NewRouter()
	r.Use(
		middleware.RequestID,
		realIP(o.TrustedProxies),
		requestLogger(deps.Log),
		recoverer(deps.Log),
		maxBody(o.MaxBodyBytes),
		middleware.Timeout(o.RequestTimeout),
	)

	r.Get("/healthz", healthz)
	r.Get("/readyz", readyz(deps.Log, o.Ready))

	MountAPI(r, deps)

	r.Handle("/*", webassets.Handler())
	return r
}
