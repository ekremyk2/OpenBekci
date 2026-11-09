package main

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newPacksCmd(load configLoader) *cobra.Command {
	cmd := &cobra.Command{Use: "packs", Short: "Manage packs"}
	cmd.AddCommand(newPurgePackCmd(load), newListPacksCmd(load))
	return cmd
}

func newListPacksCmd(load configLoader) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the packs compiled into this binary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(Official) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no packs are compiled into this binary")
				return nil
			}
			for _, p := range Official {
				m := p.Manifest()
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s (sdk >= %s) countries=%s\n", m.ID, m.Version, m.MinSDK, strings.Join(m.Countries, ","))
			}
			return nil
		},
	}
}

func newPurgePackCmd(load configLoader) *cobra.Command {
	var org, pack string
	var yes bool
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Delete a pack's rows for an organization (never automatic; asks for confirmation)",
		Long: "Deletes every row of the pack's pack_<id>_* tables for the organization, and the pack's enablement and\n" +
			"settings. The pack must be disabled for the organization first. Settings that presets wrote into the\n" +
			"organization's sites stay. This cannot be undone.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if org == "" || pack == "" {
				return errors.New("--org and --pack are required")
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
			tables, err := a.PackTables(cmd.Context(), pack)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "This permanently deletes the data of pack %q for organization %s from: %s\n", pack, orgID, strings.Join(append([]string{"(its settings)"}, tables...), ", "))
			if !yes {
				fmt.Fprintf(out, "Type the pack id (%s) to confirm: ", pack)
				line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if strings.TrimSpace(line) != pack {
					return errors.New("not confirmed; nothing was deleted")
				}
			}
			res, err := a.PurgePack(cmd.Context(), orgID, pack)
			if err != nil {
				return err
			}
			for t, n := range res.Tables {
				fmt.Fprintf(out, "%s: %d row(s) deleted\n", t, n)
			}
			fmt.Fprintf(out, "pack %q purged for organization %s (settings removed: %t)\n", pack, orgID, res.Settings)
			return nil
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization id or slug")
	cmd.Flags().StringVar(&pack, "pack", "", "pack id")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (for scripts)")
	return cmd
}
