package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"

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

// Idempotency modes of the x-idempotent extension. "none" marks a read-only
// POST (a preview or dry run): it takes no Idempotency-Key.
const (
	idemOptional = "optional"
	idemRequired = "required"
	idemNone     = "none"
)

// idemMode turns the extension value into the mode the middleware acts on:
// none behaves like an operation without the extension.
func idemMode(v string) string {
	if v == idemNone {
		return ""
	}
	return v
}

const (
	headerIdempotencyKey = "Idempotency-Key"
	headerStability      = "X-OpenBekci-Stability"
)

// Authenticator resolves the caller of a request. SessionAuthenticator (auth.go)
// is the production implementation, supplied through Deps.Authenticator.
//
// Authenticate is called for every operation whose spec `security` is not
// the empty list: required for operations that demand credentials, and
// best-effort for optional-auth operations (`security: [{}, ...]`), where a
// failure just means the caller is anonymous. It returns the principal, or an
// error rendered as a problem (normally problem.Unauthenticated; a nil
// principal with a nil error is also treated as unauthenticated). The pipeline
// stores the principal in the request context for authz.PrincipalFromContext.
type Authenticator interface {
	Authenticate(r *http.Request) (*authz.Principal, error)
}

// NoopAuthenticator rejects every request as unauthenticated. It is the
// default when no Authenticator is configured (unit tests).
type NoopAuthenticator struct{}

// Authenticate implements Authenticator.
func (NoopAuthenticator) Authenticate(*http.Request) (*authz.Principal, error) {
	return nil, problem.Unauthenticated("authentication is required")
}

// authMode is how an operation treats credentials.
type authMode int

const (
	// authRequired is the zero value on purpose: an operation without a
	// `security` key fails closed. (Every operation must declare one; the
	// conformance test and the lint rule enforce it.)
	authRequired authMode = iota
	// authNone: `security: []`, anonymous callers only.
	authNone
	// authOptional: `security: [{}, ...]`, credentials are used when valid.
	authOptional
)

// opInfo is what the pipeline reads from one operation of the spec.
type opInfo struct {
	idempotent string // "", optional or required
	preview    bool   // x-stability: preview
	auth       authMode
	orgScoped  bool               // path under /v1/orgs/{org_id}: membership is resolved first
	perms      []authz.Permission // x-openbekci-roles: checked against the grants before the handler or an idempotent replay
	query      map[string]bool    // declared query parameter names
	// stream is x-openbekci-stream: the operation streams a request body
	// (multipart upload) or a binary response (download). Its request body is
	// not buffered for validation or hashed for idempotency, and a binary
	// response is not buffered by the response validator.
	stream  bool
	hasBody bool // the operation declares a request body
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
	// resolver and tenantDB are set by MountAPI; nil (unit tests) skips the
	// authorization context.
	resolver *authz.Resolver
	tenantDB tenantRunner
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
				idempotent: idemMode(extString(op.Extensions, "x-idempotent")),
				preview:    extString(op.Extensions, "x-stability") == "preview",
				auth:       authModeOf(spec, op),
				query:      queryParams(item, op),
				orgScoped:  isOrgPath(path),
				perms:      extPermissions(op.Extensions),
				stream:     extBool(op.Extensions, "x-openbekci-stream"),
				hasBody:    op.RequestBody != nil,
			}
			p.ops[method+" "+path] = info
		}
	}
	return p, nil
}

// queryParams are the names of the query parameters an operation declares
// (its own and its path item's).
func queryParams(item *openapi3.PathItem, op *openapi3.Operation) map[string]bool {
	names := map[string]bool{}
	for _, params := range []openapi3.Parameters{item.Parameters, op.Parameters} {
		for _, ref := range params {
			if ref.Value != nil && ref.Value.In == openapi3.ParameterInQuery {
				names[ref.Value.Name] = true
			}
		}
	}
	return names
}

// authModeOf reads the operation's `security`. A missing key (and no
// top-level default) means authRequired, never anonymous.
func authModeOf(spec *openapi3.T, op *openapi3.Operation) authMode {
	var reqs openapi3.SecurityRequirements
	switch {
	case op.Security != nil:
		reqs = *op.Security
	case len(spec.Security) > 0:
		reqs = spec.Security
	default:
		return authRequired
	}
	if len(reqs) == 0 {
		return authNone
	}
	for _, r := range reqs {
		if len(r) == 0 {
			return authOptional
		}
	}
	return authRequired
}

// extPermissions reads the x-openbekci-roles list of an operation.
func extPermissions(ext map[string]any) []authz.Permission {
	var raw []byte
	switch v := ext["x-openbekci-roles"].(type) {
	case nil:
		return nil
	case json.RawMessage:
		raw = v
	case []byte:
		raw = v
	default:
		raw, _ = json.Marshal(v)
	}
	var names []string
	if json.Unmarshal(raw, &names) != nil {
		return nil
	}
	out := make([]authz.Permission, len(names))
	for i, n := range names {
		out[i] = authz.Permission(n)
	}
	return out
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

// extBool reads a boolean extension.
func extBool(ext map[string]any, name string) bool {
	switch v := ext[name].(type) {
	case bool:
		return v
	case json.RawMessage:
		var b bool
		return json.Unmarshal(v, &b) == nil && b
	}
	return false
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
	// Authentication comes before request validation: a caller who is not
	// allowed in learns nothing about the parameters or body.
	chain = append(chain,
		p.stability,
		p.authenticate,
		p.authzContext,
		p.rejectUnknownQuery,
		p.validation,
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

// rejectUnknownQuery answers validation_failed for query parameters the
// operation does not declare (kin-openapi ignores them), so a misspelt filter
// is an error instead of silently returning unfiltered data.
func (p *pipeline) rejectUnknownQuery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := p.op(r)
		var unknown []string
		for name := range r.URL.Query() {
			if !info.query[name] {
				unknown = append(unknown, name)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			fields := make([]problem.Field, len(unknown))
			for i, name := range unknown {
				fields[i] = problem.Field{Field: name, Code: "unknown_field"}
			}
			problem.Write(w, r, problem.Validation(fields...))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authenticate runs the Authenticator for operations that take credentials.
func (p *pipeline) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := p.op(r).auth
		if mode == authNone {
			next.ServeHTTP(w, r)
			return
		}
		pr, err := p.auth.Authenticate(r)
		if mode == authOptional {
			if err == nil && pr != nil {
				r = r.WithContext(authz.WithPrincipal(r.Context(), pr))
			}
			next.ServeHTTP(w, r)
			return
		}
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
