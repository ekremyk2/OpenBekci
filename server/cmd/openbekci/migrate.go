package main

import (
	"github.com/spf13/cobra"

	"github.com/openbekci/openbekci/server/internal/app"
)

func newMigrateCmd(load configLoader) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Manage database migrations (runs as the migrator role)",
	}
	for _, sub := range []struct{ name, short string }{
		{"up", "Apply all pending migrations (goose, then River)"},
		{"down", "Roll back the most recent goose migration"},
		{"status", "Show migration status"},
	} {
		cmd.AddCommand(&cobra.Command{
			Use:   sub.name,
			Short: sub.short,
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				cfg, err := load()
				if err != nil {
					return err
				}
				log := app.NewLogger(cmd.ErrOrStderr(), cfg.Debug)
				return app.Migrate(cmd.Context(), cfg, log, cmd.Name(), cmd.OutOrStdout())
			},
		})
	}
	return cmd
}
