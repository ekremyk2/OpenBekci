package httpapi

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
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
