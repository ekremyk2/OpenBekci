package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/openbekci/openbekci/server/internal/platform/obs"
)

// serveWorkerEndpoints gives a worker-only process /healthz and /metrics on
// cfg.MetricsAddr (a cluster-internal port; /metrics still needs the token
// when one is set). It returns the function that stops the listener.
func serveWorkerEndpoints(cfg Config, log *slog.Logger) func() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	mux.Handle("GET /metrics", obs.Handler(cfg.MetricsToken))
	srv := &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("worker metrics listener", "error", err)
		}
	}()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}
