package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// Check is one readiness probe.
type Check struct {
	Name string
	Fn   func(ctx context.Context) error
	// Warn makes a failure show as "warn" without making the server unready (pack Ready hooks).
	Warn bool
}

type healthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// healthz reports that the process is up. It touches nothing else.
func healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

// readyz runs every check. Failure details go to the log, not to the client:
// the endpoint is unauthenticated.
func readyz(log *slog.Logger, checks []Check) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		resp := healthResponse{Status: "ok", Checks: make(map[string]string, len(checks))}
		for _, c := range checks {
			if err := c.Fn(ctx); err != nil {
				log.WarnContext(ctx, "readiness check failed", "check", c.Name, "error", err)
				if c.Warn {
					resp.Checks[c.Name] = "warn"
					continue
				}
				resp.Status = "unavailable"
				resp.Checks[c.Name] = "failed"
				continue
			}
			resp.Checks[c.Name] = "ok"
		}
		code := http.StatusOK
		if resp.Status != "ok" {
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, resp)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v) // the client went away; nothing useful to do
}
