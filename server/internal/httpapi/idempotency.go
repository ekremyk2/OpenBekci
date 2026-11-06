package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/idempotency"
	"github.com/openbekci/openbekci/server/internal/problem"
)

const (
	maxIdempotentBody = 16 << 20
	maxKeyLength      = 255
	storeTimeout      = 5 * time.Second
)

// idempotency implements Idempotency-Key for operations tagged x-idempotent
// in the spec (backend plan, section 8):
//
//   - required: the header is mandatory (validation_failed without it);
//   - optional: honoured when present.
//
// The first request with a key inserts an in-progress row; a concurrent one
// gets 409 conflict; a later one with the same request replays the stored
// response; one with a different request gets 422 idempotency_mismatch.
// Responses with status 5xx are not stored: the key is released so the client
// can retry.
func (p *pipeline) idempotency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := p.op(r)
		if info.idempotent == "" {
			next.ServeHTTP(w, r)
			return
		}
		key := r.Header.Get(headerIdempotencyKey)
		if key == "" {
			if info.idempotent == idemRequired {
				problem.Write(w, r, problem.Validation(problem.Field{Field: headerIdempotencyKey, Code: "required"}))
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > maxKeyLength {
			problem.Write(w, r, problem.Validation(problem.Field{
				Field: headerIdempotencyKey, Code: "too_long", Params: map[string]any{"max": maxKeyLength},
			}))
			return
		}
		if p.idem == nil { // not wired (unit tests without a database)
			next.ServeHTTP(w, r)
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxIdempotentBody))
		if err != nil {
			problem.Write(w, r, problem.Wrap(http.StatusRequestEntityTooLarge, problem.CodeBadRequest, "request body too large", err))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		hash := requestHash(r, body)
		scope := requestScope(r)

		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), storeTimeout)
		res, err := p.idem.Begin(ctx, scope, key, hash)
		cancel()
		if err != nil {
			problem.Write(w, r, problem.Internal(err))
			return
		}
		switch res.State {
		case idempotency.InProgress:
			problem.Write(w, r, problem.Conflict("a request with this Idempotency-Key is still being processed"))
			return
		case idempotency.Mismatch:
			problem.Write(w, r, problem.New(http.StatusUnprocessableEntity, problem.CodeIdempotencyMismatch,
				"this Idempotency-Key was already used with a different request"))
			return
		case idempotency.Replay:
			replay(w, res.Response)
			return
		case idempotency.Acquired:
		}

		rec := &recorder{ResponseWriter: w}
		finished := false
		defer func() {
			// A panic or a server error releases the key instead of storing the response.
			sctx, scancel := context.WithTimeout(context.WithoutCancel(r.Context()), storeTimeout)
			defer scancel()
			var serr error
			if finished && rec.statusCode() < http.StatusInternalServerError {
				serr = p.idem.Complete(sctx, scope, key, idempotency.Response{
					Status: rec.statusCode(), ContentType: rec.Header().Get("Content-Type"), Body: rec.body.Bytes(),
				})
			} else {
				serr = p.idem.Abort(sctx, scope, key)
			}
			if serr != nil {
				p.log.Error("idempotency store", "error", serr, "key_scope_org", scope.OrgID != nil)
			}
		}()
		next.ServeHTTP(rec, r)
		finished = true
	})
}

func replay(w http.ResponseWriter, resp idempotency.Response) {
	if resp.ContentType != "" {
		w.Header().Set("Content-Type", resp.ContentType)
	}
	w.Header().Set("Idempotent-Replayed", "true")
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
}

func requestHash(r *http.Request, body []byte) string {
	h := sha256.New()
	h.Write([]byte(r.Method))
	h.Write([]byte{0})
	h.Write([]byte(r.URL.EscapedPath()))
	h.Write([]byte{0})
	h.Write([]byte(r.URL.RawQuery))
	h.Write([]byte{0})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// requestScope is (org from the path, principal from the authenticated caller).
// Both are nil when absent.
func requestScope(r *http.Request) idempotency.Scope {
	var s idempotency.Scope
	if v := chi.URLParam(r, "org_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			s.OrgID = &id
		}
	}
	if pr := authz.PrincipalFromContext(r.Context()); pr != nil {
		id := pr.ID
		s.PrincipalID = &id
	}
	return s
}

// recorder passes the response through and keeps a copy.
type recorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (rc *recorder) WriteHeader(code int) {
	if rc.status == 0 {
		rc.status = code
	}
	rc.ResponseWriter.WriteHeader(code)
}

func (rc *recorder) Write(b []byte) (int, error) {
	if rc.status == 0 {
		rc.status = http.StatusOK
	}
	rc.body.Write(b)
	return rc.ResponseWriter.Write(b)
}

func (rc *recorder) statusCode() int {
	if rc.status == 0 {
		return http.StatusOK
	}
	return rc.status
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (rc *recorder) Unwrap() http.ResponseWriter { return rc.ResponseWriter }

// bufferedResponse holds a response until it has been validated.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

func (b *bufferedResponse) statusCode() int {
	if b.status == 0 {
		return http.StatusOK
	}
	return b.status
}

// responseValidator checks every response against the spec (a dev aid
// enabled by OPENBEKCI_API_VALIDATE_RESPONSES and always on in tests). A
// response that does not conform becomes a 500 internal_error that carries the
// reason, and is logged.
func (p *pipeline) responseValidator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := &bufferedResponse{header: http.Header{}}
		next.ServeHTTP(buf, r)

		err := p.validateResponse(r, buf)
		if err != nil {
			p.log.Error("response does not match the API contract", "method", r.Method, "path", r.URL.Path, "status", buf.statusCode(), "error", err)
			pe := problem.Wrap(http.StatusInternalServerError, problem.CodeInternalError, "response does not match the API contract: "+err.Error(), err)
			pe.Expose = true
			problem.Write(w, r, pe)
			return
		}
		h := w.Header()
		for k, v := range buf.header {
			h[k] = v
		}
		w.WriteHeader(buf.statusCode())
		_, _ = w.Write(buf.body.Bytes())
	})
}

func (p *pipeline) validateResponse(r *http.Request, buf *bufferedResponse) error {
	route, params, err := p.router.FindRoute(r)
	if err != nil {
		return errors.New("route not in spec: " + err.Error())
	}
	in := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route},
		Status:                 buf.statusCode(),
		Header:                 buf.header,
		Options:                &openapi3filter.Options{IncludeResponseStatus: true},
	}
	in.SetBodyBytes(buf.body.Bytes())
	return openapi3filter.ValidateResponse(r.Context(), in)
}
