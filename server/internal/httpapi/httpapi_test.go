package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func testRouter(ready []Check, trusted ...string) http.Handler {
	var prefixes []netip.Prefix
	for _, p := range trusted {
		prefixes = append(prefixes, netip.MustParsePrefix(p))
	}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return NewRouter(Deps{Log: log}, Options{TrustedProxies: prefixes, Ready: ready})
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	rec := get(testRouter(nil), "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestReadyz(t *testing.T) {
	ok := Check{Name: "a", Fn: func(context.Context) error { return nil }}
	bad := Check{Name: "b", Fn: func(context.Context) error { return errors.New("secret detail") }}

	rec := get(testRouter([]Check{ok}), "/readyz")
	if rec.Code != http.StatusOK {
		t.Fatalf("all ok: status = %d", rec.Code)
	}

	rec = get(testRouter([]Check{ok, bad}), "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("one failing: status = %d", rec.Code)
	}
	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "unavailable" || resp.Checks["a"] != "ok" || resp.Checks["b"] != "failed" {
		t.Errorf("response = %+v", resp)
	}
	if body := rec.Body.String(); strings.Contains(body, "secret detail") {
		t.Errorf("failure detail leaked to client: %s", body)
	}
}

func TestRecovererReturns500(t *testing.T) {
	boom := Check{Name: "boom", Fn: func(context.Context) error { panic("kaboom") }}
	rec := get(testRouter([]Check{boom}), "/readyz")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestPlaceholderServedAtRootUnderNoWebAssetsOrEmptyDist(t *testing.T) {
	rec := get(testRouter(nil), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("content type = %q", rec.Header().Get("Content-Type"))
	}
	if get(testRouter(nil), "/nope").Code != http.StatusNotFound {
		t.Error("unknown path should be 404 while no web app is embedded")
	}
}

func TestRealIP(t *testing.T) {
	var seen string
	h := realIP([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) { seen = r.RemoteAddr }))

	cases := []struct {
		name, remote, xff, want string
	}{
		{"untrusted peer ignores XFF", "203.0.113.9:1234", "1.2.3.4", "203.0.113.9:1234"},
		{"trusted peer uses XFF", "10.0.0.2:1234", "198.51.100.7", "198.51.100.7:0"},
		{"skips trusted hops from the right", "10.0.0.2:1234", "198.51.100.7, 10.1.1.1", "198.51.100.7:0"},
		{"client cannot spoof leftmost", "10.0.0.2:1234", "6.6.6.6, 198.51.100.7", "198.51.100.7:0"},
		{"malformed XFF falls back", "10.0.0.2:1234", "garbage", "10.0.0.2:1234"},
		{"no XFF", "10.0.0.2:1234", "", "10.0.0.2:1234"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = tc.remote
		if tc.xff != "" {
			req.Header.Set("X-Forwarded-For", tc.xff)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		if seen != tc.want {
			t.Errorf("%s: RemoteAddr = %q, want %q", tc.name, seen, tc.want)
		}
	}
}
