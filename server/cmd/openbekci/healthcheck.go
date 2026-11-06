package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// newHealthcheckCmd probes the local /healthz. It exists for container
// HEALTHCHECKs: the distroless image has no curl or shell.
func newHealthcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "healthcheck",
		Short: "Probe the local server's /healthz and exit non-zero if it is not healthy",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			addr := os.Getenv("OPENBEKCI_LISTEN_ADDR")
			if addr == "" {
				addr = ":8080"
			}
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return fmt.Errorf("parse OPENBEKCI_LISTEN_ADDR %q: %w", addr, err)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort("127.0.0.1", port)+"/healthz", nil)
			if err != nil {
				return fmt.Errorf("build request: %w", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("probe /healthz: %w", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("/healthz returned %d", resp.StatusCode)
			}
			return nil
		},
	}
}
