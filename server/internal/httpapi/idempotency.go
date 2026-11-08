package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/idempotency"
	"github.com/openbekci/openbekci/server/internal/problem"
)

const storeTimeout = 5 * time.Second

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
//
// Keys are scoped by the authenticated principal. An anonymous caller has no
// stored keys: a required operation answers 401, an optional one ignores the
// header and runs the handler.
func (p *pipeline) idempotency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := p.op(r)
		if info.idempotent == "" {
			next.ServeHTTP(w, r)
			return
		}
		principal := authz.PrincipalFromContext(r.Context())
		if principal == nil {
			if info.idempotent == idemRequired {
				problem.Write(w, r, problem.Unauthenticated("authentication is required"))
				return
			}
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
		if p.idem == nil { // not wired (unit tests without a database)
			next.ServeHTTP(w, r)
			return
		}

		var body []byte
		var digest *uploadDigest
		if info.stream {
			// A streamed body (file upload) is not buffered to be hashed, and
			// its multipart boundary and length differ between retries, so the
			// request hash covers method, path and query only. What was
			// uploaded is compared by content: the handler records the SHA-256
			// of the form while it streams it, the store keeps it with the key,
			// and a retry is read through the same digest before it replays.
			body = []byte("stream")
			digest = &uploadDigest{}
			r = r.WithContext(context.WithValue(r.Context(), uploadDigestKey{}, digest))
		} else {
			var err error
			body, err = io.ReadAll(r.Body) // already capped by maxBody
			if err != nil {
				problem.Write(w, r, bodyReadError(err))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		hash := requestHash(r, body)
		scope := requestScope(r, principal)

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
			if info.stream && res.Response.Digest != "" {
				// Same key, same route: it is a replay only if the same content
				// comes in again. Nothing is stored on this path.
				got, err := formDigest(r)
				if err != nil {
					problem.Write(w, r, err)
					return
				}
				if got != res.Response.Digest {
					problem.Write(w, r, problem.New(http.StatusUnprocessableEntity, problem.CodeIdempotencyMismatch,
						"this Idempotency-Key was already used with a different file"))
					return
				}
			}
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
				stored := rec.stored()
				if digest != nil {
					stored.Digest = digest.get()
				}
				serr = p.idem.Complete(sctx, scope, key, res.Owner, stored)
			} else {
				serr = p.idem.Abort(sctx, scope, key, res.Owner)
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
	for k, v := range resp.Headers {
		w.Header()[http.CanonicalHeaderKey(k)] = v
	}
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

// requestScope is (org from the path, the authenticated principal).
func requestScope(r *http.Request, pr *authz.Principal) idempotency.Scope {
	s := idempotency.Scope{PrincipalID: pr.ID}
	if pr.APIKey != nil {
		// a key's principal carries its creator's user id; the key must not
		// share a namespace with the creator's own requests (a replay would
		// hand it a response it was never entitled to) or with the creator's
		// other keys
		s.PrincipalID = pr.APIKey.KeyID
	}
	if v := chi.URLParam(r, "org_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			s.OrgID = &id
		}
	}
	return s
}

// replayHeaders are the response headers a replay restores. Everything else is
// per-request (dates, request ids, cookies) and must not be replayed.
var replayHeaders = []string{"Content-Type", "Location", headerStability}

// recorder passes the response through and keeps a copy.
type recorder struct {
	http.ResponseWriter
	status   int
	body     bytes.Buffer
	captured http.Header // allow-listed headers, snapshotted when the response starts
}

// start snapshots the headers as they are when the response begins: later
// changes are not sent and must not be stored.
func (rc *recorder) start(code int) {
	if rc.status != 0 {
		return
	}
	rc.status = code
	rc.captured = http.Header{}
	for _, name := range replayHeaders {
		if v := rc.Header().Values(name); len(v) > 0 {
			rc.captured[http.CanonicalHeaderKey(name)] = append([]string(nil), v...)
		}
	}
}

func (rc *recorder) WriteHeader(code int) {
	rc.start(code)
	rc.ResponseWriter.WriteHeader(code)
}

func (rc *recorder) Write(b []byte) (int, error) {
	rc.start(http.StatusOK)
	rc.body.Write(b)
	return rc.ResponseWriter.Write(b)
}

// stored is the response to keep for replay.
func (rc *recorder) stored() idempotency.Response {
	rc.start(http.StatusOK) // handler wrote nothing: an implicit 200
	resp := idempotency.Response{Status: rc.status, ContentType: rc.captured.Get("Content-Type"), Body: rc.body.Bytes()}
	for k, v := range rc.captured {
		if k == "Content-Type" {
			continue
		}
		if resp.Headers == nil {
			resp.Headers = map[string][]string{}
		}
		resp.Headers[k] = v
	}
	return resp
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
		// A streamed binary response (a file download) is not decoded: the spec
		// declares it as an opaque binary, and the headers are still checked.
		Options: &openapi3filter.Options{IncludeResponseStatus: true, ExcludeResponseBody: p.op(r).stream && !strings.Contains(buf.header.Get("Content-Type"), "json")},
	}
	in.SetBodyBytes(buf.body.Bytes())
	return openapi3filter.ValidateResponse(r.Context(), in)
}
