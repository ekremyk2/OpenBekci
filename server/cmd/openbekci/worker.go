package main

import (
	"github.com/spf13/cobra"

	"github.com/openbekci/openbekci/server/internal/app"
)

func newWorkerCmd(load configLoader) *cobra.Command {
	return &cobra.Command{
		Use:   "worker",
		Short: "Run only the job worker",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := load()
			if err != nil {
				return err
			}
			log := app.NewLogger(cmd.ErrOrStderr(), cfg.Debug)
			return app.RunWorker(cmd.Context(), cfg, log)
		},
	}
}
