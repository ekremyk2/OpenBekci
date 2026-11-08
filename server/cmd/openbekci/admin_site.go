package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/openbekci/openbekci/server/internal/core/tenancy"
)

func newExportSiteCmd(load configLoader) *cobra.Command {
	var org, site, out string
	cmd := &cobra.Command{
		Use:   "export-site",
		Short: "Export a whole site (people, occupancies, units, keys, vehicles, ...) to an archive file",
		Long: "Writes a JSON archive with identifiers preserved, for the site hand-over (decision D2).\n" +
			"National ids stay sealed with this instance's ENCRYPTION_KEY. The archive is personal data: keep it safe.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if org == "" || site == "" || out == "" {
				return errors.New("--org, --site and --out are required")
			}
			siteID, err := uuid.Parse(site)
			if err != nil {
				return fmt.Errorf("--site must be a site id: %w", err)
			}
			a, err := openAdmin(cmd, load)
			if err != nil {
				return err
			}
			defer a.Close()
			orgID, err := a.OrgID(cmd.Context(), org)
			if err != nil {
				return err
			}
			rec, data, err := a.Tenancy.SystemExportSite(cmd.Context(), orgID, siteID)
			if err != nil {
				return err
			}
			if err := os.WriteFile(out, data, 0o600); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "exported site %s (export %s) to %s (%d bytes)\n", siteID, rec.ID, out, len(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization id or slug")
	cmd.Flags().StringVar(&site, "site", "", "site id")
	cmd.Flags().StringVar(&out, "out", "", "archive file to write")
	return cmd
}

func newImportSiteCmd(load configLoader) *cobra.Command {
	var org, file string
	var untrusted bool
	cmd := &cobra.Command{
		Use:   "import-site",
		Short: "Import a site archive into an organization (identifiers preserved)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if org == "" || file == "" {
				return errors.New("--org and --file are required")
			}
			data, err := os.ReadFile(file) //nolint:gosec // operator-supplied path
			if err != nil {
				return err
			}
			a, err := openAdmin(cmd, load)
			if err != nil {
				return err
			}
			defer a.Close()
			orgID, err := a.OrgID(cmd.Context(), org)
			if err != nil {
				return err
			}
			rec, err := a.Tenancy.SystemImportSiteWithOptions(cmd.Context(), orgID, data, tenancy.ImportOptions{AllowUntrusted: untrusted})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "imported site %v into %s (record %s): %v\n", rec.SiteID, orgID, rec.ID, rec.Summary)
			return nil
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "target organization id or slug")
	cmd.Flags().StringVar(&file, "file", "", "archive file to read")
	cmd.Flags().BoolVar(&untrusted, "untrusted", false, "accept an archive not signed by this instance (another instance, or edited); only the site's own board and auditor memberships and no account links are imported either way")
	return cmd
}

func newOffboardSiteCmd(load configLoader) *cobra.Command {
	var org, site string
	cmd := &cobra.Command{
		Use:   "offboard-site",
		Short: "Close a site after its hand-over (ends its memberships, blocks every operation on it)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if org == "" || site == "" {
				return errors.New("--org and --site are required")
			}
			siteID, err := uuid.Parse(site)
			if err != nil {
				return fmt.Errorf("--site must be a site id: %w", err)
			}
			a, err := openAdmin(cmd, load)
			if err != nil {
				return err
			}
			defer a.Close()
			orgID, err := a.OrgID(cmd.Context(), org)
			if err != nil {
				return err
			}
			if _, err := a.Tenancy.SystemOffboardSite(cmd.Context(), orgID, siteID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "site %s offboarded\n", siteID)
			return nil
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization id or slug")
	cmd.Flags().StringVar(&site, "site", "", "site id")
	return cmd
}
