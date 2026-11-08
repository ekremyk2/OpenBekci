package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/openbekci/openbekci/server/internal/problem"
)

func TestMaxBodyRefusesBeforeAnythingElse(t *testing.T) {
	h := NewRouter(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, Options{MaxBodyBytes: 1024})
	cases := map[string]func() *http.Request{
		"declared length": func() *http.Request {
			return httptest.NewRequest(http.MethodPost, "/v1/instance", strings.NewReader(strings.Repeat("x", 2048)))
		},
		"chunked": func() *http.Request {
			req := httptest.NewRequest(http.MethodPost, "/v1/instance", io.NopCloser(strings.NewReader(strings.Repeat("x", 2048))))
			req.ContentLength = -1
			return req
		},
	}
	for name, mk := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, mk())
		p := decodeProblem(t, rec, http.StatusRequestEntityTooLarge, "payload_too_large")
		if p.Instance == "" {
			t.Errorf("%s: no instance", name)
		}
	}
	// A body within the limit is untouched by the cap.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/instance", strings.NewReader("small")))
	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Errorf("small body refused: %s", rec.Body)
	}
}

func TestMaxBodyDefaultIsOneMiB(t *testing.T) {
	h := NewRouter(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, Options{})
	req := httptest.NewRequest(http.MethodPost, "/v1/instance", strings.NewReader(""))
	req.ContentLength = DefaultMaxBodyBytes + 1
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	decodeProblem(t, rec, http.StatusRequestEntityTooLarge, "payload_too_large")
}

func TestRequestTimeoutIsProblem(t *testing.T) {
	h := chi.NewRouter()
	h.Use(middleware.RequestID, requestTimeout(20*time.Millisecond))
	h.Get("/slow", func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }) // gives up silently
	h.Get("/slow-err", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		problem.Write(w, r, r.Context().Err()) // handler reports the context error
	})
	h.Get("/fast", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	for _, path := range []string{"/slow", "/slow-err"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		decodeProblem(t, rec, http.StatusGatewayTimeout, "timeout")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fast", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("fast: %d %s", rec.Code, rec.Body)
	}
}
