package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/openbekci/openbekci/server/internal/problem"
)

// realIP sets r.RemoteAddr to the client address. X-Forwarded-For is honoured
// only when the direct peer is in trusted; the rightmost entry that is not
// itself a trusted proxy is the client.
func realIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a.Unmap()) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(trusted) > 0 {
				if peer, err := netip.ParseAddrPort(r.RemoteAddr); err == nil && isTrusted(peer.Addr()) {
					if ip := clientFromXFF(r.Header.Values("X-Forwarded-For"), isTrusted); ip != "" {
						r.RemoteAddr = net.JoinHostPort(ip, "0")
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientFromXFF(values []string, isTrusted func(netip.Addr) bool) string {
	var hops []string
	for _, v := range values {
		for _, h := range strings.Split(v, ",") {
			if h = strings.TrimSpace(h); h != "" {
				hops = append(hops, h)
			}
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(hops[i])
		if err != nil {
			return "" // malformed header: do not guess
		}
		if !isTrusted(a) {
			return a.String()
		}
	}
	return ""
}

// requestLogger logs one structured line per request. It never logs query
// strings, headers or bodies, which may carry personal data.
func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)
			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			log.LogAttrs(r.Context(), levelFor(status), "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int("bytes", ww.BytesWritten()),
				slog.Duration("duration", time.Since(start)),
				slog.String("remote_ip", host),
			)
		})
	}
}

func levelFor(status int) slog.Level {
	if status >= 500 {
		return slog.LevelError
	}
	return slog.LevelInfo
}

// requestTimeout bounds each request. A handler that gives up on the expired
// context and writes nothing gets a problem+json 504 here; one that returns
// the context error is mapped by problem.From.
func requestTimeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			timeout := d
			if isUploadRequest(r) {
				timeout = max(d, uploadTimeout) // a phone on a slow link needs longer than a JSON call
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r.WithContext(ctx))
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && ww.Status() == 0 && ww.BytesWritten() == 0 {
				problem.Write(w, r, problem.Wrap(http.StatusGatewayTimeout, problem.CodeTimeout, "the request took too long", ctx.Err()))
			}
		})
	}
}

// DefaultMaxBodyBytes is the request body cap for JSON API requests (1 MiB).
// File uploads get their own, larger limit (DefaultMaxUploadBytes).
const DefaultMaxBodyBytes = 1 << 20

// DefaultMaxUploadBytes is the cap for the body of a file upload request:
// the file limit (25 MiB) plus room for the multipart framing and fields. The
// file module enforces the exact per-file limit while streaming.
const DefaultMaxUploadBytes = 25<<20 + uploadSlack

// UploadBodyLimit is the request body cap that goes with a per-file limit.
func UploadBodyLimit(fileLimit int64) int64 { return fileLimit + uploadSlack }

const (
	uploadSlack   = 1 << 20
	uploadTimeout = 10 * time.Minute
)

var uploadPath = regexp.MustCompile(`^/v1/orgs/[^/]+/files/?$`)

// isUploadRequest recognizes the one multipart route (POST /v1/orgs/{org_id}/files).
// The body cap and timeout run before routing, so they match on the path.
func isUploadRequest(r *http.Request) bool {
	return r.Method == http.MethodPost && uploadPath.MatchString(r.URL.Path)
}

// maxBody caps request bodies before anything reads them. A declared
// Content-Length over the limit is refused at once; a body without a length
// (chunked) is buffered up to the limit so the refusal is deterministic and
// still happens ahead of validation, authentication and idempotency. Readers
// further down are also behind http.MaxBytesReader.
//
// The file upload route has its own, larger cap (uploadLimit) and is never
// buffered: its body is streamed, so a chunked upload over the cap fails
// while it is being read (413 from the upload handler).
func maxBody(limit, uploadLimit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body == nil || r.Body == http.NoBody {
				next.ServeHTTP(w, r)
				return
			}
			limit, streamed := limit, isUploadRequest(r)
			if streamed {
				limit = uploadLimit
			}
			if r.ContentLength > limit {
				problem.Write(w, r, tooLarge(limit, nil))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			if r.ContentLength < 0 && !streamed {
				b, err := io.ReadAll(r.Body)
				if err != nil {
					var mbe *http.MaxBytesError
					if errors.As(err, &mbe) {
						problem.Write(w, r, tooLarge(limit, err))
						return
					}
					problem.Write(w, r, problem.Wrap(http.StatusBadRequest, problem.CodeBadRequest, "the request body could not be read", err))
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(b))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// bodyReadError maps a failed body read to a problem.
func bodyReadError(err error) *problem.Error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return tooLarge(mbe.Limit, err)
	}
	return problem.Wrap(http.StatusBadRequest, problem.CodeBadRequest, "the request body could not be read", err)
}

func tooLarge(limit int64, cause error) *problem.Error {
	return problem.Wrap(http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge,
		fmt.Sprintf("the request body exceeds the limit of %d bytes", limit), cause)
}

// recoverer turns a handler panic into a logged 500 problem+json.
func recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				log.Error("panic in handler", "method", r.Method, "path", r.URL.Path,
					"panic", fmt.Sprint(rec), "stack", string(debug.Stack()))
				problem.Write(w, r, problem.Internal(fmt.Errorf("panic: %v", rec)))
			}()
			next.ServeHTTP(w, r)
		})
	}
}
