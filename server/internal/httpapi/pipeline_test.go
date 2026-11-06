package httpapi

import (
	"bytes"
	"context"
	"errors"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/idempotency"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// testSpec is a small API that exercises what the real spec does not use yet
// (phase A has no mutations): required and optional idempotency, request
// bodies and the preview stability marker.
const testSpec = `
openapi: 3.0.3
info: {title: test, version: "1"}
servers: [{url: /}]
paths:
  /v1/things:
    get:
      operationId: test.listThings
      security: []
      parameters:
        - {name: limit, in: query, schema: {type: integer, minimum: 1, maximum: 200, default: 50}}
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                required: [items, next_cursor]
                properties:
                  items: {type: array, items: {type: string}}
                  next_cursor: {type: string, nullable: true}
        default: {$ref: "#/components/responses/Problem"}
    post:
      operationId: test.createThing
      security: [{bearerAuth: []}]
      x-idempotent: required
      parameters:
        - {name: Idempotency-Key, in: header, required: true, schema: {type: string, minLength: 8}}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name: {type: string, minLength: 1, maxLength: 5}
                qty: {type: integer, minimum: 0}
      responses:
        "201":
          description: created
          content:
            application/json:
              schema:
                type: object
                required: [id]
                properties:
                  id: {type: integer}
        default: {$ref: "#/components/responses/Problem"}
  /v1/things-optional:
    post:
      operationId: test.createOptional
      security: [{}, {bearerAuth: []}]
      x-idempotent: optional
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                required: [id]
                properties:
                  id: {type: integer}
        default: {$ref: "#/components/responses/Problem"}
  /v1/orgs/{org_id}/secret:
    get:
      operationId: test.getSecret
      security: [{bearerAuth: []}]
      parameters:
        - {name: org_id, in: path, required: true, schema: {type: string, format: uuid}}
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: object, required: [who], properties: {who: {type: string}}}
        default: {$ref: "#/components/responses/Problem"}
  /v1/ping:
    post:
      operationId: test.ping
      security: [{bearerAuth: []}]
      x-idempotent: required
      parameters:
        - {name: Idempotency-Key, in: header, required: true, schema: {type: string, minLength: 8}}
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                required: [id]
                properties:
                  id: {type: integer}
        default: {$ref: "#/components/responses/Problem"}
  /v1/boom:
    post:
      operationId: test.boom
      security: [{bearerAuth: []}]
      x-idempotent: required
      parameters:
        - {name: Idempotency-Key, in: header, required: true, schema: {type: string, minLength: 8}}
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                required: [id]
                properties:
                  id: {type: integer}
        default: {$ref: "#/components/responses/Problem"}
  /v1/fail:
    post:
      operationId: test.fail
      security: [{bearerAuth: []}]
      x-idempotent: required
      parameters:
        - {name: Idempotency-Key, in: header, required: true, schema: {type: string, minLength: 8}}
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                required: [id]
                properties:
                  id: {type: integer}
        default: {$ref: "#/components/responses/Problem"}
  /v1/reject:
    post:
      operationId: test.reject
      security: [{bearerAuth: []}]
      x-idempotent: required
      parameters:
        - {name: Idempotency-Key, in: header, required: true, schema: {type: string, minLength: 8}}
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                required: [id]
                properties:
                  id: {type: integer}
        default: {$ref: "#/components/responses/Problem"}
  /v1/optional:
    get:
      operationId: test.getOptional
      security: [{}, {bearerAuth: []}]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: object, required: [who], properties: {who: {type: string}}}
        default: {$ref: "#/components/responses/Problem"}
  /v1/unannotated:
    get:
      operationId: test.getUnannotated
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: object}
        default: {$ref: "#/components/responses/Problem"}
  /v1/preview:
    get:
      operationId: test.getPreview
      security: []
      x-stability: preview
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: object}
        default: {$ref: "#/components/responses/Problem"}
  /v1/broken:
    get:
      operationId: test.getBroken
      security: []
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: object, required: [must_have], properties: {must_have: {type: string}}}
        default: {$ref: "#/components/responses/Problem"}
components:
  securitySchemes:
    bearerAuth: {type: http, scheme: bearer}
  responses:
    Problem:
      description: error
      content:
        application/problem+json:
          schema: {$ref: "#/components/schemas/Problem"}
  schemas:
    Problem:
      type: object
      required: [type, title, status, code, instance]
      properties:
        type: {type: string}
        title: {type: string}
        status: {type: integer}
        code: {type: string}
        detail: {type: string}
        instance: {type: string}
        errors:
          type: array
          items:
            type: object
            required: [field, code]
            properties:
              field: {type: string}
              code: {type: string}
              params: {type: object, additionalProperties: true}
`

type testAPI struct {
	h       http.Handler
	creates atomic.Int32
	gate    chan struct{} // when non-nil, createThing waits on it
	delay   time.Duration // createThing sleeps this long (to widen race windows)
	log     *bytes.Buffer
}

// fixedAuth maps bearer tokens to principals.
type fixedAuth map[string]*authz.Principal

func (f fixedAuth) Authenticate(r *http.Request) (*authz.Principal, error) {
	if p, ok := f[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]; ok {
		return p, nil
	}
	return nil, problem.Unauthenticated("no token")
}

func newTestAPI(t *testing.T, store idempotency.Store) *testAPI {
	t.Helper()
	spec, err := openapi3.NewLoader().LoadFromData([]byte(testSpec))
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ta := &testAPI{log: &bytes.Buffer{}}
	log := slog.New(slog.NewTextHandler(&lockedWriter{w: ta.log}, nil))
	auth := fixedAuth{"ok": &authz.Principal{ID: uuid.New()}, "other": &authz.Principal{ID: uuid.New()}}
	p, err := newPipeline(spec, log, auth, store, true)
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID, recoverer(log))
	r.NotFound(notFound)
	g := r.With(p.Middlewares()...)
	g.Get("/v1/things", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"next_cursor":null}`))
	})
	g.Post("/v1/things", func(w http.ResponseWriter, _ *http.Request) {
		n := ta.creates.Add(1)
		if ta.gate != nil {
			<-ta.gate
		}
		time.Sleep(ta.delay)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", "/v1/things/"+itoa(int(n)))
		w.Header().Set("Set-Cookie", "per-request="+itoa(int(n)))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":` + itoa(int(n)) + `}`))
	})
	g.Post("/v1/ping", func(w http.ResponseWriter, _ *http.Request) {
		n := ta.creates.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":` + itoa(int(n)) + `}`))
	})
	g.Post("/v1/boom", func(http.ResponseWriter, *http.Request) {
		ta.creates.Add(1)
		panic("kaboom")
	})
	g.Post("/v1/fail", func(w http.ResponseWriter, r *http.Request) {
		ta.creates.Add(1)
		problem.Write(w, r, problem.Internal(errors.New("db down")))
	})
	g.Post("/v1/reject", func(w http.ResponseWriter, r *http.Request) {
		ta.creates.Add(1)
		problem.Write(w, r, problem.Conflict("name taken"))
	})
	g.Post("/v1/things-optional", func(w http.ResponseWriter, _ *http.Request) {
		n := ta.creates.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":` + itoa(int(n)) + `}`))
	})
	g.Get("/v1/orgs/{org_id}/secret", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		who := "anonymous"
		if pr := authz.PrincipalFromContext(r.Context()); pr != nil {
			who = pr.ID.String()
		}
		_, _ = w.Write([]byte(`{"who":"` + who + `"}`))
	})
	g.Get("/v1/optional", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		who := "anonymous"
		if pr := authz.PrincipalFromContext(r.Context()); pr != nil {
			who = pr.ID.String()
		}
		_, _ = w.Write([]byte(`{"who":"` + who + `"}`))
	})
	g.Get("/v1/unannotated", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	g.Get("/v1/preview", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	g.Get("/v1/broken", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"other":1}`))
	})
	ta.h = r
	return ta
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// lockedWriter makes a bytes.Buffer safe for the concurrent logger.
type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// doAuth is do as the principal behind "Bearer ok".
func (ta *testAPI) doAuth(method, path, body string, headers ...string) *httptest.ResponseRecorder {
	return ta.do(method, path, body, append([]string{"Authorization", "Bearer ok"}, headers...)...)
}

func (ta *testAPI) do(method, path, body string, headers ...string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	ta.h.ServeHTTP(rec, req)
	return rec
}

type problemBody struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Code     string `json:"code"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
	Errors   []struct {
		Field  string         `json:"field"`
		Code   string         `json:"code"`
		Params map[string]any `json:"params"`
	} `json:"errors"`
}

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) problemBody {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, status, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type = %q", ct)
	}
	var p problemBody
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v; body: %s", err, rec.Body)
	}
	if p.Code != code || p.Status != status || p.Instance == "" {
		t.Fatalf("problem = %+v, want code %s", p, code)
	}
	return p
}

func TestValidationBadQueryParam(t *testing.T) {
	ta := newTestAPI(t, nil)
	for _, q := range []string{"limit=abc", "limit=0", "limit=201"} {
		p := decodeProblem(t, ta.do("GET", "/v1/things?"+q, ""), 422, "validation_failed")
		if len(p.Errors) != 1 || p.Errors[0].Field != "limit" {
			t.Errorf("%s: errors = %+v", q, p.Errors)
		}
	}
	if rec := ta.do("GET", "/v1/things?limit=10", ""); rec.Code != 200 {
		t.Fatalf("valid limit: %d %s", rec.Code, rec.Body)
	}
}

func TestValidationBody(t *testing.T) {
	ta := newTestAPI(t, nil)
	key := []string{"Idempotency-Key", "key-12345678"}

	// Malformed JSON is bad_request, not a field error.
	decodeProblem(t, ta.doAuth("POST", "/v1/things", `{"name":`, key...), 400, "bad_request")

	// Field errors, several at once.
	p := decodeProblem(t, ta.doAuth("POST", "/v1/things", `{"name":"toolongname","qty":-1}`, key...), 422, "validation_failed")
	got := map[string]string{}
	for _, e := range p.Errors {
		got[e.Field] = e.Code
	}
	if got["name"] != "too_long" || got["qty"] != "too_small" {
		t.Errorf("errors = %+v", p.Errors)
	}

	p = decodeProblem(t, ta.doAuth("POST", "/v1/things", `{}`, key...), 422, "validation_failed")
	if len(p.Errors) != 1 || p.Errors[0].Field != "name" || p.Errors[0].Code != "required" {
		t.Errorf("missing field: %+v", p.Errors)
	}

	p = decodeProblem(t, ta.doAuth("POST", "/v1/things", `{"name":123}`, key...), 422, "validation_failed")
	if len(p.Errors) != 1 || p.Errors[0].Field != "name" || p.Errors[0].Code != "invalid_type" {
		t.Errorf("wrong type: %+v", p.Errors)
	}
}

func TestUnknownPathIsProblem(t *testing.T) {
	ta := newTestAPI(t, nil)
	decodeProblem(t, ta.do("GET", "/v1/nope", ""), 404, "not_found")
}

func TestStabilityHeader(t *testing.T) {
	ta := newTestAPI(t, nil)
	if got := ta.do("GET", "/v1/preview", "").Header().Get("X-OpenBekci-Stability"); got != "preview" {
		t.Errorf("preview header = %q", got)
	}
	if got := ta.do("GET", "/v1/things", "").Header().Get("X-OpenBekci-Stability"); got != "" {
		t.Errorf("stable op has header %q", got)
	}
}

func TestAuthenticationStep(t *testing.T) {
	ta := newTestAPI(t, nil)
	org := uuid.NewString()
	decodeProblem(t, ta.do("GET", "/v1/orgs/"+org+"/secret", ""), 401, "unauthenticated")
	rec := ta.do("GET", "/v1/orgs/"+org+"/secret", "", "Authorization", "Bearer ok")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "anonymous") {
		t.Fatalf("authenticated: %d %s", rec.Code, rec.Body)
	}
	decodeProblem(t, ta.do("GET", "/v1/orgs/not-a-uuid/secret", "", "Authorization", "Bearer ok"), 422, "validation_failed")
}

// Authentication runs before validation: an anonymous caller gets 401 even
// when the request is also invalid, and learns nothing about the parameters.
func TestAuthenticationBeforeValidation(t *testing.T) {
	ta := newTestAPI(t, nil)
	rec := ta.do("GET", "/v1/orgs/not-a-uuid/secret", "")
	decodeProblem(t, rec, 401, "unauthenticated")
	if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("WWW-Authenticate = %q", got)
	}
	if strings.Contains(rec.Body.String(), "org_id") {
		t.Errorf("401 leaks validation detail: %s", rec.Body)
	}
}

func TestOptionalAuthAttachesPrincipalWhenValid(t *testing.T) {
	ta := newTestAPI(t, nil)
	anon := ta.do("GET", "/v1/optional", "")
	if anon.Code != 200 || !strings.Contains(anon.Body.String(), "anonymous") {
		t.Fatalf("anonymous: %d %s", anon.Code, anon.Body)
	}
	bad := ta.do("GET", "/v1/optional", "", "Authorization", "Bearer nope")
	if bad.Code != 200 || !strings.Contains(bad.Body.String(), "anonymous") {
		t.Fatalf("bad credential: %d %s", bad.Code, bad.Body)
	}
	ok := ta.do("GET", "/v1/optional", "", "Authorization", "Bearer ok")
	if ok.Code != 200 || strings.Contains(ok.Body.String(), "anonymous") {
		t.Fatalf("valid credential: %d %s", ok.Code, ok.Body)
	}
}

// An operation without a security key must fail closed.
func TestMissingSecurityKeyFailsClosed(t *testing.T) {
	ta := newTestAPI(t, nil)
	decodeProblem(t, ta.do("GET", "/v1/unannotated", ""), 401, "unauthenticated")
	if rec := ta.do("GET", "/v1/unannotated", "", "Authorization", "Bearer ok"); rec.Code != 200 {
		t.Fatalf("authenticated: %d %s", rec.Code, rec.Body)
	}
}

func TestResponseValidationCatchesContractViolation(t *testing.T) {
	ta := newTestAPI(t, nil)
	p := decodeProblem(t, ta.do("GET", "/v1/broken", ""), 500, "internal_error")
	if !strings.Contains(p.Detail, "contract") {
		t.Errorf("detail = %q", p.Detail)
	}
}

func TestIdempotencyRequiredHeaderMissing(t *testing.T) {
	ta := newTestAPI(t, nil)
	p := decodeProblem(t, ta.doAuth("POST", "/v1/things", `{"name":"a"}`), 422, "validation_failed")
	if len(p.Errors) != 1 || p.Errors[0].Field != "Idempotency-Key" || p.Errors[0].Code != "required" {
		t.Errorf("errors = %+v", p.Errors)
	}
	// Too short per the spec's minLength.
	decodeProblem(t, ta.doAuth("POST", "/v1/things", `{"name":"a"}`, "Idempotency-Key", "x"), 422, "validation_failed")
}
