package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/oapi-codegen/nullable"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/instance"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// MountAPI mounts the generated strict server and the spec-driven middleware
// (validation, authentication, idempotency, stability) on r. Paths come from
// the spec: /v1/... and /.well-known/openbekci.json. Unknown paths under those
// prefixes answer with a problem+json 404 instead of falling through to the
// web app.
func MountAPI(r chi.Router, deps Deps) {
	spec, err := gen.GetSpec()
	if err != nil {
		panic("httpapi: embedded OpenAPI spec is invalid: " + err.Error()) // programmer error, caught by tests
	}
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	auth := deps.Authenticator
	if auth == nil {
		auth = NoopAuthenticator{}
	}
	p, err := newPipeline(spec, log, auth, deps.Idempotency, deps.ValidateResponses)
	if err != nil {
		panic("httpapi: " + err.Error())
	}
	if deps.Idempotency == nil {
		for _, info := range p.ops {
			if info.idempotent != "" {
				log.Warn("idempotency store not configured; Idempotency-Key is validated but not enforced")
				break
			}
		}
	}

	p.resolver = deps.Authz
	if deps.DB != nil {
		p.tenantDB = deps.DB
	}
	srv := &apiServer{instance: deps.Instance, identity: deps.Identity, tenancy: deps.Tenancy, audit: deps.Audit, files: deps.Files, resolver: deps.Authz, insecureCookies: deps.InsecureCookies}
	strict := gen.NewStrictHandlerWithOptions(srv, []gen.StrictMiddlewareFunc{requestInfo}, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			problem.Write(w, r, requestBindError(err))
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			pe := problem.From(err)
			if pe.Status >= http.StatusInternalServerError {
				log.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
			}
			problem.Write(w, r, err)
		},
	})
	r.NotFound(notFound)
	r.MethodNotAllowed(methodNotAllowed(r))
	gen.HandlerWithOptions(strict, gen.ChiServerOptions{
		BaseRouter:       r,
		Middlewares:      genMiddlewares(p.Middlewares()),
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) { problem.Write(w, r, requestBindError(err)) },
	})
	// No catch-all route for /v1/* or /.well-known/*: a catch-all matches every
	// method and would hide chi's 405. Unknown paths reach NotFound instead
	// (NewRouter sends API prefixes there to a problem, the rest to the web app).
}

// isAPIPath reports whether path belongs to the API: /v1, /v1/... and /.well-known/...
func isAPIPath(path string) bool {
	return path == "/v1" || strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/.well-known/")
}

// apiServer implements gen.StrictServerInterface by delegating to the modules.
type apiServer struct {
	instance        *instance.Service
	identity        *identity.Service
	tenancy         *tenancy.Service
	audit           *audit.Service
	files           *files.Service
	resolver       *authz.Resolver
	insecureCookies bool // INSECURE_DEV: no Secure flag on the session cookie
}

var _ gen.StrictServerInterface = (*apiServer)(nil)

func (s *apiServer) InstanceGetDiscovery(ctx context.Context, _ gen.InstanceGetDiscoveryRequestObject) (gen.InstanceGetDiscoveryResponseObject, error) {
	if s.instance == nil {
		return nil, problem.Internal(errNoInstance)
	}
	doc, err := s.instance.Discovery(ctx)
	if err != nil {
		return nil, problem.Internal(err)
	}
	return gen.InstanceGetDiscovery200JSONResponse(discoveryDTO(doc)), nil
}

func (s *apiServer) InstanceGetInstance(ctx context.Context, _ gen.InstanceGetInstanceRequestObject) (gen.InstanceGetInstanceResponseObject, error) {
	if s.instance == nil {
		return nil, problem.Internal(errNoInstance)
	}
	info, err := s.instance.Info(ctx)
	if err != nil {
		return nil, problem.Internal(err)
	}
	d := discoveryDTO(info.Document)
	return gen.InstanceGetInstance200JSONResponse(gen.Instance{
		ApiBaseUrl: d.ApiBaseUrl, ApiVersions: d.ApiVersions, AuthMethods: d.AuthMethods,
		Branding: d.Branding, InstanceId: d.InstanceId, InstanceName: d.InstanceName,
		MinAppVersion: d.MinAppVersion, Packs: d.Packs, Push: d.Push,
		RelayEnrollment: d.RelayEnrollment, SdkVersion: d.SdkVersion, ServerVersion: d.ServerVersion,
		Policies: gen.InstancePolicies(info.Policies),
	}), nil
}

func discoveryDTO(d instance.Document) gen.DiscoveryDocument {
	methods := make([]gen.AuthMethod, 0, len(d.AuthMethods))
	for _, m := range d.AuthMethods {
		am := gen.AuthMethod{Id: m.ID}
		if m.LabelKey != "" {
			am.LabelKey = &m.LabelKey
		}
		if m.Icon != "" {
			am.Icon = &m.Icon
		}
		methods = append(methods, am)
	}
	out := gen.DiscoveryDocument{
		InstanceId:      d.InstanceID,
		InstanceName:    d.Name,
		ApiBaseUrl:      d.APIBaseURL,
		ApiVersions:     d.APIVersions,
		ServerVersion:   d.ServerVersion,
		SdkVersion:      nullable.NewNullNullable[string](),
		MinAppVersion:   gen.MinAppVersion{Android: d.MinAppAndroid, Ios: d.MinAppIOS},
		AuthMethods:     methods,
		Push:            gen.PushInfo{RelayIds: d.PushRelayIDs, UnifiedPush: d.UnifiedPush},
		Packs:           d.Packs,
		Branding:        gen.Branding{LogoUrl: d.LogoURL, PrimaryColor: d.PrimaryColor},
		RelayEnrollment: gen.RelayEnrollment{Challenge: nullable.NewNullNullable[string]()},
	}
	if d.SDKVersion != nil {
		out.SdkVersion = nullable.NewNullableWithValue(*d.SDKVersion)
	}
	if d.RelayChallenge != nil {
		out.RelayEnrollment.Challenge = nullable.NewNullableWithValue(*d.RelayChallenge)
	}
	return out
}

var errNoInstance = errors.New("instance service not configured")

// genMiddlewares adapts the chain (outermost first, as pipeline.Middlewares
// documents) to the generated wrapper, which wraps the handler in list order,
// so the LAST entry ends up outermost: the list is reversed here.
func genMiddlewares(in []func(http.Handler) http.Handler) []gen.MiddlewareFunc {
	out := make([]gen.MiddlewareFunc, len(in))
	for i, m := range in {
		out[len(in)-1-i] = m
	}
	return out
}
