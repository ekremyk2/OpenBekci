// Package problem maps Go errors to RFC 9457 application/problem+json
// responses with the stable codes of the API spec (backend plan, section 8).
//
// Business code returns *Error (directly or wrapped); the HTTP layer renders
// it. The package has no dependency on the generated API types: the contract
// test checks that what it writes conforms to the Problem schema.
package problem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// ContentType is the media type of every error response.
const ContentType = "application/problem+json"

// Code is the stable machine-readable error code. Keep in sync with the
// Problem.code enum in api/src/components/problem.yaml (the contract test
// enforces it).
type Code string

// Problem codes.
const (
	CodeValidationFailed       Code = "validation_failed"
	CodeBadRequest             Code = "bad_request"
	CodeUnauthenticated        Code = "unauthenticated"
	CodeNotFound               Code = "not_found"
	CodeConflict               Code = "conflict"
	CodeVersionConflict        Code = "version_conflict"
	CodeIdempotencyMismatch    Code = "idempotency_mismatch"
	CodeLockedPeriod           Code = "locked_period"
	CodeInsufficientPermission Code = "insufficient_permission"
	CodeReauthRequired         Code = "reauth_required"
	CodeMFARequired            Code = "mfa_required"
	CodeInvalidGrant           Code = "invalid_grant"
	CodeGuardSessionRequired   Code = "guard_session_required"
	CodeGuardSessionExpired    Code = "guard_session_expired"
	CodeRateLimited            Code = "rate_limited"
	CodePackError              Code = "pack_error"
	CodeSiteOffboarded         Code = "site_offboarded"
	CodeInternalError          Code = "internal_error"
	CodeCSRFFailed             Code = "csrf_failed"
	CodeSMSUnavailable         Code = "sms_unavailable"
	CodeMailUnavailable        Code = "mail_unavailable"
	CodePayloadTooLarge        Code = "payload_too_large"
	CodeMethodNotAllowed       Code = "method_not_allowed"
	CodeUnsupportedMediaType   Code = "unsupported_media_type"
	CodeTimeout                Code = "timeout"
	CodeScanUnavailable        Code = "scan_unavailable"
	// CodeAgentOffline: the on-site agent of an access point is not connected (409).
	CodeAgentOffline Code = "agent_offline"
	// CodeAgentSessionExpired: the agent's long-poll session ended; start a new one with hello (410).
	CodeAgentSessionExpired Code = "agent_session_expired"
	// CodeAPIKeyRevoked: the API key was revoked or replaced by a rotation (401).
	CodeAPIKeyRevoked Code = "api_key_revoked"
	// CodeAPIKeyExpired: the API key is past its expires_at (401).
	CodeAPIKeyExpired Code = "api_key_expired"
)

// Field is one entry of the errors[] array.
type Field struct {
	Field  string         `json:"field"`
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

// Error is a problem to report to the client.
type Error struct {
	Code   Code
	Status int
	Detail string
	Fields []Field
	// Expose sends Detail to the client even for a 5xx (dev aids only).
	Expose bool
	// RetryAfter, when positive, is sent as the Retry-After header (seconds,
	// rounded up); used with 429 rate_limited and 503.
	RetryAfter time.Duration
	// Err is the underlying cause. It is logged, never sent to the client.
	Err error
}

func (e *Error) Error() string {
	s := fmt.Sprintf("%d %s", e.Status, e.Code)
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

// Unwrap returns the underlying cause.
func (e *Error) Unwrap() error { return e.Err }

// New builds an Error.
func New(status int, code Code, detail string) *Error {
	return &Error{Code: code, Status: status, Detail: detail}
}

// Wrap builds an Error carrying a cause that is logged but not sent.
func Wrap(status int, code Code, detail string, cause error) *Error {
	return &Error{Code: code, Status: status, Detail: detail, Err: cause}
}

// Validation is a 422 validation_failed with per-field errors.
func Validation(fields ...Field) *Error {
	return &Error{Code: CodeValidationFailed, Status: http.StatusUnprocessableEntity, Detail: "the request is not valid", Fields: fields}
}

// BadRequest is a 400 bad_request (malformed request, not a field problem).
func BadRequest(detail string) *Error { return New(http.StatusBadRequest, CodeBadRequest, detail) }

// NotFound is a 404 not_found.
func NotFound(detail string) *Error { return New(http.StatusNotFound, CodeNotFound, detail) }

// Unauthenticated is a 401 unauthenticated.
func Unauthenticated(detail string) *Error {
	return New(http.StatusUnauthorized, CodeUnauthenticated, detail)
}

// Forbidden is a 403 insufficient_permission.
func Forbidden(detail string) *Error {
	return New(http.StatusForbidden, CodeInsufficientPermission, detail)
}

// Conflict is a 409 conflict.
func Conflict(detail string) *Error { return New(http.StatusConflict, CodeConflict, detail) }

// RateLimited is a 429 rate_limited telling the client when to retry.
func RateLimited(retryAfter time.Duration) *Error {
	return &Error{Code: CodeRateLimited, Status: http.StatusTooManyRequests, Detail: "too many requests; try again later", RetryAfter: retryAfter}
}

// Internal is a 500 internal_error that hides the cause from the client.
func Internal(cause error) *Error {
	return &Error{Code: CodeInternalError, Status: http.StatusInternalServerError, Err: cause}
}

// Body is the JSON shape of a problem response.
type Body struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Code     Code   `json:"code"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance"`
	// RequestID repeats Instance under the name web clients look for.
	RequestID string  `json:"request_id,omitempty"`
	Errors    []Field `json:"errors,omitempty"`
}

// From converts any error to a *Error: a *Error anywhere in the chain is used
// as is, anything else is an internal error.
func From(err error) *Error {
	var pe *Error
	if errors.As(err, &pe) {
		return pe
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Wrap(http.StatusGatewayTimeout, CodeTimeout, "the request took too long", err)
	}
	return Internal(err)
}

// Write renders err as a problem response. instance is the request id.
func Write(w http.ResponseWriter, r *http.Request, err error) {
	pe := From(err)
	status := pe.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	id := middleware.GetReqID(r.Context())
	body := Body{
		Type:      "about:blank",
		Title:     http.StatusText(status),
		Status:    status,
		Code:      pe.Code,
		Instance:  id,
		RequestID: id,
		Errors:    pe.Fields,
	}
	if status < http.StatusInternalServerError || pe.Expose {
		body.Detail = pe.Detail // never leak internals on 5xx
	}
	b, merr := json.Marshal(body)
	if merr != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ContentType)
	if status == http.StatusUnauthorized && h.Get("WWW-Authenticate") == "" {
		h.Set("WWW-Authenticate", "Bearer")
	}
	if id != "" {
		h.Set("X-Request-Id", id)
	}
	if pe.RetryAfter > 0 {
		h.Set("Retry-After", strconv.Itoa(int(math.Ceil(pe.RetryAfter.Seconds()))))
	}
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}
