package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/openbekci/openbekci/server/internal/app"
)

func newRebuildChargeStatusCmd(load configLoader) *cobra.Command {
	var org, site string
	cmd := &cobra.Command{
		Use:   "rebuild-charge-status",
		Short: "Re-derive the status of every charge of a site from its payment allocations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			siteID, err := uuid.Parse(site)
			if err != nil || org == "" {
				return errors.New("--org and --site (a site id) are required")
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
			n, err := a.RebuildChargeStatus(cmd.Context(), orgID, siteID)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "charge statuses rebuilt: %d changed\n", n)
			return nil
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization id or slug")
	cmd.Flags().StringVar(&site, "site", "", "site id")
	return cmd
}

func newPostEntryCmd(load configLoader) *cobra.Command {
	var org, site, date, memo string
	var lines []string
	cmd := &cobra.Command{
		Use:   "post-entry",
		Short: "Post a manual journal entry (accountant use)",
		Long: "Each --line is CODE:DEBIT:CREDIT[:FUND] with amounts in minor units, for example\n" +
			"  --line 1100:50000:0 --line 4900:0:50000\nThe entry must balance, also within every fund, and its date must be after the site's lock date.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			siteID, err := uuid.Parse(site)
			if err != nil || org == "" || date == "" || len(lines) < 2 {
				return errors.New("--org, --site, --date and at least two --line are required")
			}
			var ml []app.ManualLine
			for _, l := range lines {
				p := strings.Split(l, ":")
				if len(p) < 3 || len(p) > 4 {
					return fmt.Errorf("bad --line %q: want CODE:DEBIT:CREDIT[:FUND]", l)
				}
				d, e1 := strconv.ParseInt(p[1], 10, 64)
				c, e2 := strconv.ParseInt(p[2], 10, 64)
				if e1 != nil || e2 != nil {
					return fmt.Errorf("bad amounts in --line %q", l)
				}
				m := app.ManualLine{AccountCode: p[0], Debit: d, Credit: c}
				if len(p) == 4 {
					m.FundCode = p[3]
				}
				ml = append(ml, m)
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
			id, err := a.PostEntry(cmd.Context(), orgID, siteID, date, memo, ml)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "posted journal entry %s\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization id or slug")
	cmd.Flags().StringVar(&site, "site", "", "site id")
	cmd.Flags().StringVar(&date, "date", "", "entry date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&memo, "memo", "", "memo")
	cmd.Flags().StringArrayVar(&lines, "line", nil, "CODE:DEBIT:CREDIT[:FUND] (repeat)")
	return cmd
}
