package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/go-chi/chi/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/idempotency"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func init() {
	// kin-openapi does not check format: uuid unless it is defined, and its
	// RFC 4122 pattern rejects UUIDv7 (version nibble 7), which is what ids are.
	openapi3.DefineStringFormatValidator("uuid", openapi3.NewRegexpFormatValidator(
		`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`))
}

// Idempotency modes of the x-idempotent extension.
const (
	idemOptional = "optional"
	idemRequired = "required"
)

const (
	headerIdempotencyKey = "Idempotency-Key"
	headerStability      = "X-OpenBekci-Stability"
)

// Authenticator resolves the caller of a request. The sessions task (A3)
// supplies the real implementation through Deps.Authenticator.
//
// Authenticate is called only for operations whose spec `security` is not
// empty. It returns the principal, or an error rendered as a problem
// (normally problem.Unauthenticated; a nil principal with a nil error is also
// treated as unauthenticated). The pipeline stores the principal in the
// request context for authz.PrincipalFromContext.
type Authenticator interface {
	Authenticate(r *http.Request) (*authz.Principal, error)
}

// NoopAuthenticator rejects every request as unauthenticated. It is the
// default until sessions are wired.
type NoopAuthenticator struct{}

// Authenticate implements Authenticator.
func (NoopAuthenticator) Authenticate(*http.Request) (*authz.Principal, error) {
	return nil, problem.Unauthenticated("authentication is required")
}

// opInfo is what the pipeline reads from one operation of the spec.
type opInfo struct {
	idempotent string // "", optional or required
	preview    bool   // x-stability: preview
	secured    bool   // needs an authenticated caller
}

// pipeline holds the per-operation middleware that is driven by the spec:
// request validation, authentication, idempotency, stability header and
// (optionally) response validation.
type pipeline struct {
	spec              *openapi3.T
	router            routers.Router
	ops               map[string]opInfo // "METHOD /chi/pattern"
	log               *slog.Logger
	auth              Authenticator
	idem              idempotency.Store
	validateResponses bool
}

func newPipeline(spec *openapi3.T, log *slog.Logger, auth Authenticator, idem idempotency.Store, validateResponses bool) (*pipeline, error) {
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		return nil, fmt.Errorf("build spec router: %w", err)
	}
	p := &pipeline{
		spec: spec, router: router, ops: map[string]opInfo{},
		log: log, auth: auth, idem: idem, validateResponses: validateResponses,
	}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			info := opInfo{
				idempotent: extString(op.Extensions, "x-idempotent"),
				preview:    extString(op.Extensions, "x-stability") == "preview",
				secured:    requiresAuth(spec, op),
			}
			p.ops[method+" "+path] = info
		}
	}
	return p, nil
}

// requiresAuth reports whether the operation has security requirements and
// none of them is the empty (anonymous) alternative.
func requiresAuth(spec *openapi3.T, op *openapi3.Operation) bool {
	reqs := spec.Security
	if op.Security != nil {
		reqs = *op.Security
	}
	if len(reqs) == 0 {
		return false
	}
	for _, r := range reqs {
		if len(r) == 0 {
			return false
		}
	}
	return true
}

// extString reads a string extension that arrives as json.RawMessage or a decoded value.
func extString(ext map[string]any, name string) string {
	switch v := ext[name].(type) {
	case string:
		return v
	case json.RawMessage:
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
	case []byte:
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
	}
	return ""
}

// op finds the operation being served from the chi route pattern. It is only
// meaningful inside a route-level middleware.
func (p *pipeline) op(r *http.Request) opInfo {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return opInfo{}
	}
	return p.ops[r.Method+" "+rctx.RoutePattern()]
}

// Middlewares returns the route-level middleware chain, outermost first.
func (p *pipeline) Middlewares() []func(http.Handler) http.Handler {
	chain := []func(http.Handler) http.Handler{}
	if p.validateResponses {
		chain = append(chain, p.responseValidator)
	}
	chain = append(chain,
		p.stability,
		requestValidator(p.spec),
		p.authenticate,
		p.idempotency,
	)
	return chain
}

// stability adds X-OpenBekci-Stability: preview to preview operations.
func (p *pipeline) stability(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.op(r).preview {
			w.Header().Set(headerStability, "preview")
		}
		next.ServeHTTP(w, r)
	})
}

// authenticate runs the Authenticator for operations that need a caller.
func (p *pipeline) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.op(r).secured {
			next.ServeHTTP(w, r)
			return
		}
		pr, err := p.auth.Authenticate(r)
		if err != nil {
			problem.Write(w, r, err)
			return
		}
		if pr == nil {
			problem.Write(w, r, problem.Unauthenticated("authentication is required"))
			return
		}
		next.ServeHTTP(w, r.WithContext(authz.WithPrincipal(r.Context(), pr)))
	})
}

// notFound renders unknown API paths as problem+json.
func notFound(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, r, problem.NotFound("no such resource"))
}

// methodNotAllowed renders chi's 405 as a problem with an Allow header. chi
// only sets Allow for its default handler, so the allowed methods are found
// by matching the path against each method.
func methodNotAllowed(routes chi.Routes) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for _, m := range []string{
			http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodOptions,
		} {
			if routes.Match(chi.NewRouteContext(), m, r.URL.Path) {
				w.Header().Add("Allow", m)
			}
		}
		problem.Write(w, r, problem.New(http.StatusMethodNotAllowed, problem.CodeMethodNotAllowed, "method not allowed"))
	}
}
