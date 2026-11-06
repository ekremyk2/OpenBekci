// Command openbekci is the OpenBekci server: API, web app and job worker in one binary.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/openbekci/openbekci/server/internal/app"
)

// Set by -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configPath string
	root := &cobra.Command{
		Use:           "openbekci",
		Short:         "OpenBekci residential site management server",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&configPath, "config", "", "path to an optional YAML config file (OPENBEKCI_* variables override it)")

	load := func() (app.Config, error) { return app.LoadConfig(configPath, os.Getenv) }
	root.AddCommand(
		newServeCmd(load),
		newWorkerCmd(load),
		newMigrateCmd(load),
		newAdminCmd(),
		newVersionCmd(),
		newHealthcheckCmd(),
	)
	return root
}
