package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
