// Package httpapi is the HTTP layer: router, middleware and health endpoints.
package httpapi

import (
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/webassets"
)

// Deps is everything handlers may need. It grows as modules arrive.
type Deps struct {
	Log  *slog.Logger
	DB   *db.DB
	Jobs *jobs.Client
}

// Options configures NewRouter.
type Options struct {
	TrustedProxies []netip.Prefix
	// RequestTimeout bounds each request; zero means 30 seconds.
	RequestTimeout time.Duration
	// Ready are the probes behind /readyz.
	Ready []Check
}

// NewRouter builds the root handler.
func NewRouter(deps Deps, o Options) http.Handler {
	if o.RequestTimeout == 0 {
		o.RequestTimeout = 30 * time.Second
	}
	r := chi.NewRouter()
	r.Use(
		middleware.RequestID,
		realIP(o.TrustedProxies),
		requestLogger(deps.Log),
		middleware.Recoverer,
		middleware.Timeout(o.RequestTimeout),
	)

	r.Get("/healthz", healthz)
	r.Get("/readyz", readyz(deps.Log, o.Ready))

	MountAPI(r, deps)

	r.Handle("/*", webassets.Handler())
	return r
}

// MountAPI is the mount point for the generated OpenAPI server. The next task
// attaches the oapi-codegen strict server here (under /v1); phase A adds no
// business endpoints.
func MountAPI(r chi.Router, deps Deps) {
	_ = r
	_ = deps
}
