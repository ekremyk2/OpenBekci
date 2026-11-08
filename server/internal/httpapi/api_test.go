package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/instance"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var update = flag.Bool("update", false, "rewrite golden files")

type fixedID uuid.UUID

func (f fixedID) InstanceID(context.Context) (uuid.UUID, error) { return uuid.UUID(f), nil }

// realRouter is the production router with a fixed instance, response
// validation on, and no database.
func realRouter(auth Authenticator) http.Handler {
	methods := &instance.AuthMethodRegistry{}
	methods.Register(instance.DefaultAuthMethods())
	methods.Register(instance.StaticAuthMethods{{ID: "oidc:google", LabelKey: "auth.google", Icon: "google"}})
	svc := instance.NewService(instance.Config{
		Name: "Test Site", BaseURL: "https://bekci.example.org", ServerVersion: "0.1.0",
		MinAppVersionAndroid: "1.0.0", MinAppVersionIOS: "1.0.0",
		LogoURL: "https://bekci.example.org/logo.png", PrimaryColor: "#1a73e8",
	}, fixedID(uuid.MustParse("0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")), methods)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return NewRouter(Deps{Log: log, Instance: svc, Authenticator: auth, ValidateResponses: true}, Options{})
}

func TestDiscoveryGolden(t *testing.T) {
	rec := get(realRouter(nil), "/.well-known/openbekci.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q", ct)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, rec.Body.Bytes(), "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')

	path := filepath.Join("testdata", "discovery.golden.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pretty.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update): %v", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), pretty.Bytes()) {
		t.Errorf("discovery document differs from %s (run with -update if intended):\n%s", path, pretty.String())
	}
}

func TestInstanceUnauthenticated(t *testing.T) {
	rec := get(realRouter(nil), "/v1/instance")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var p problemBody
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Code != "unauthenticated" || p.Status != 401 || p.Instance == "" {
		t.Errorf("problem = %+v", p)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content type = %q", ct)
	}
}

type allowAll struct{}

func (allowAll) Authenticate(*http.Request) (*authz.Principal, error) {
	return &authz.Principal{ID: uuid.New()}, nil
}

func TestInstanceAuthenticated(t *testing.T) {
	rec := get(realRouter(allowAll{}), "/v1/instance")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if pol, ok := m["policies"].(map[string]any); !ok || len(pol) != 0 {
		t.Errorf("policies = %v", m["policies"])
	}
	if m["instance_name"] != "Test Site" {
		t.Errorf("instance_name = %v", m["instance_name"])
	}
}

func TestAPIUnknownPathsAreProblems(t *testing.T) {
	h := realRouter(nil)
	for _, path := range []string{"/v1/nope", "/v1/orgs/x/y", "/.well-known/nothing"} {
		rec := get(h, path)
		if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s: %d %s %s", path, rec.Code, rec.Header().Get("Content-Type"), rec.Body)
		}
	}
}

func TestAPIWrongMethodIsProblem(t *testing.T) {
	h := realRouter(nil)
	for path, allow := range map[string]string{"/v1/instance": "GET", "/.well-known/openbekci.json": "GET"} {
		for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPut} {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
			decodeProblem(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
			if got := rec.Header().Get("Allow"); got != allow {
				t.Errorf("%s %s: Allow = %q, want %q", method, path, got, allow)
			}
		}
	}
	// A wrong method on a health probe is a problem too.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	decodeProblem(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
}

func TestAPIPrefixRootIsProblem(t *testing.T) {
	h := realRouter(nil)
	for _, path := range []string{"/v1", "/v1/", "/v1/instance/extra"} {
		decodeProblem(t, get(h, path), http.StatusNotFound, "not_found")
	}
}

// TestNoopAuthenticatorRejectsEverySecuredOperation walks the real spec: with
// the default authenticator, every operation that requires credentials is 401
// before validation (the requests carry no parameters or body at all).
func TestNoopAuthenticatorRejectsEverySecuredOperation(t *testing.T) {
	spec := loadSpec(t)
	h := realRouter(nil)
	n := 0
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			if authModeOf(spec, op) != authRequired || slices.Contains(op.Tags, "agent") { // the agent transport is mounted by access.Service, not by the pipeline
				continue
			}
			n++
			url := pathParam.ReplaceAllString(path, uuid.NewString())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, url, nil))
			decodeProblem(t, rec, http.StatusUnauthorized, "unauthenticated")
			if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
				t.Errorf("%s %s: WWW-Authenticate = %q", method, path, got)
			}
		}
	}
	if n == 0 {
		t.Fatal("no secured operation in the spec")
	}
}

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

func TestWebAppStillServed(t *testing.T) {
	rec := get(realRouter(nil), "/some/ui/route")
	if rec.Header().Get("Content-Type") == "application/problem+json" {
		t.Errorf("non-API path answered with a problem: %s", rec.Body)
	}
}

// loadSpec loads the bundled artifact the way the generator input is read.
func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	spec, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load api/openapi.yaml: %v", err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("spec invalid: %v", err)
	}
	return spec
}

var operationID = regexp.MustCompile(`^[a-z]+\.[a-z][A-Za-z]+$`)

func TestSpecConformance(t *testing.T) {
	spec := loadSpec(t)
	if spec.OpenAPI != "3.0.3" {
		t.Errorf("openapi = %q, want 3.0.3", spec.OpenAPI)
	}
	problemRef := "#/components/responses/Problem"
	seen := map[string]bool{}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			where := method + " " + path
			if !operationID.MatchString(op.OperationID) {
				t.Errorf("%s: operationId %q does not match %s", where, op.OperationID, operationID)
			}
			if seen[op.OperationID] {
				t.Errorf("%s: duplicate operationId %q", where, op.OperationID)
			}
			seen[op.OperationID] = true
			if method != http.MethodGet && method != http.MethodHead {
				if v := extString(op.Extensions, "x-idempotent"); v != "optional" && v != "required" && v != "none" {
					t.Errorf("%s: mutation without x-idempotent optional|required (got %q)", where, v)
				}
			}
			if extString(op.Extensions, "x-idempotent") == "required" && authModeOf(spec, op) != authRequired {
				t.Errorf("%s: x-idempotent: required needs mandatory authentication (keys are scoped by principal)", where)
			}
			if op.Security == nil {
				t.Errorf("%s: no explicit security key (use security: [] for a public operation)", where)
			}
			if v := extString(op.Extensions, "x-stability"); v != "" && v != "stable" && v != "preview" {
				t.Errorf("%s: x-stability %q", where, v)
			}
			if op.Responses.Default() == nil {
				t.Errorf("%s: no default (Problem) response", where)
			}
			for code, ref := range op.Responses.Map() {
				if code != "default" && !strings.HasPrefix(code, "4") && !strings.HasPrefix(code, "5") {
					continue
				}
				if ref.Ref != problemRef {
					t.Errorf("%s: response %s is %q, must reference %s", where, code, ref.Ref, problemRef)
				}
			}
		}
	}
}

// TestProblemCodesMatchSpec keeps the Go constants and the Problem.code enum in sync.
func TestProblemCodesMatchSpec(t *testing.T) {
	spec := loadSpec(t)
	enum := spec.Components.Schemas["Problem"].Value.Properties["code"].Value.Enum
	inSpec := map[string]bool{}
	for _, v := range enum {
		inSpec[v.(string)] = true
	}
	for _, c := range []problem.Code{
		problem.CodeValidationFailed, problem.CodeBadRequest, problem.CodeUnauthenticated, problem.CodeNotFound,
		problem.CodeConflict, problem.CodeVersionConflict, problem.CodeIdempotencyMismatch, problem.CodeLockedPeriod,
		problem.CodeInsufficientPermission, problem.CodeReauthRequired, problem.CodeMFARequired, problem.CodeInvalidGrant, problem.CodeGuardSessionRequired, problem.CodeGuardSessionExpired,
		problem.CodeRateLimited, problem.CodePackError, problem.CodeSiteOffboarded, problem.CodeInternalError, problem.CodePayloadTooLarge, problem.CodeMethodNotAllowed, problem.CodeUnsupportedMediaType, problem.CodeTimeout,
		problem.CodeCSRFFailed, problem.CodeSMSUnavailable, problem.CodeMailUnavailable,
		problem.CodeScanUnavailable,
		problem.CodeAgentOffline, problem.CodeAgentSessionExpired,
	} {
		if !inSpec[string(c)] {
			t.Errorf("code %q is not in the spec enum", c)
		}
		delete(inSpec, string(c))
	}
	for c := range inSpec {
		t.Errorf("spec enum has %q without a Go constant", c)
	}
}
