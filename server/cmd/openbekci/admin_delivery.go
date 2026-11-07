package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newDeliveryLogCmd lists the SMS and mail delivery log. The sign-in code and
// link requests never tell a requester that a send failed (that would reveal
// which accounts exist), so this is where an operator sees a failing gateway.
func newDeliveryLogCmd(load configLoader) *cobra.Command {
	var limit int
	var problems bool
	cmd := &cobra.Command{
		Use:   "delivery-log",
		Short: "Show recent SMS and email deliveries (targets are masked)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := openAdmin(cmd, load)
			if err != nil {
				return err
			}
			defer a.Close()
			list, err := a.Identity.RecentDeliveries(cmd.Context(), limit, problems)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "AT\tCHANNEL\tPURPOSE\tTARGET\tSTATUS\tDETAIL")
			for _, d := range list {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", d.At.Format("2006-01-02 15:04:05"), d.Channel, d.Purpose, d.Target, d.Status, d.Detail)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "number of entries")
	cmd.Flags().BoolVar(&problems, "failed", false, "only failed and dropped deliveries")
	return cmd
}
