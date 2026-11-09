package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeadersOnWebAndAPI(t *testing.T) {
	h := NewRouter(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, Options{})
	for _, path := range []string{"/", "/v1/instance"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		for _, name := range []string{"Content-Security-Policy", "Strict-Transport-Security", "X-Frame-Options", "Referrer-Policy", "X-Content-Type-Options"} {
			if rec.Header().Get(name) == "" {
				t.Errorf("%s: no %s header", path, name)
			}
		}
	}
}
