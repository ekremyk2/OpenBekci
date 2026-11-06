package main

import "github.com/spf13/cobra"

// newAdminCmd is the parent of the administration subcommands (create-org,
// create-platform-admin, export-site, ...); later tasks attach them here.
func newAdminCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "admin",
		Short: "Administrative commands",
	}
}
