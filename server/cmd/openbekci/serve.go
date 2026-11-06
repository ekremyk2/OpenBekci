package main

import (
	"github.com/spf13/cobra"

	"github.com/openbekci/openbekci/server/internal/app"
)

type configLoader func() (app.Config, error)

func newServeCmd(load configLoader) *cobra.Command {
	var noWorker bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP server (and the job worker unless disabled)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := load()
			if err != nil {
				return err
			}
			log := app.NewLogger(cmd.ErrOrStderr(), cfg.Debug)
			cfg.ServerVersion = version
			return app.Serve(cmd.Context(), cfg, log, cfg.Worker && !noWorker)
		},
	}
	cmd.Flags().BoolVar(&noWorker, "no-worker", false, "do not run the job worker in this process (same as OPENBEKCI_WORKER=off)")
	return cmd
}
