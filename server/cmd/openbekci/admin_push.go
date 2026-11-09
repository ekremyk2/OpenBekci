package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/openbekci/openbekci/server/internal/app"
)

func newPushCmd(load configLoader) *cobra.Command {
	cmd := &cobra.Command{Use: "push", Short: "Mobile push administration"}
	cmd.AddCommand(newPushEnrolCmd(load))
	return cmd
}

func newPushEnrolCmd(load configLoader) *cobra.Command {
	var o app.PushEnrolOptions
	cmd := &cobra.Command{
		Use:   "enrol",
		Short: "Enrol this server with a push relay",
		Long: "Publishes a fresh challenge in this server's discovery document, asks the relay to enrol the server (the relay\n" +
			"fetches https://<BASE_URL>/.well-known/openbekci.json and compares the challenge), stores the returned secret\n" +
			"in a mode-600 file and prints the OPENBEKCI_PUSH_RELAYS entry to add. The server must be running and reachable\n" +
			"from the relay while this command runs; --manual (an invite issued as manual) skips the fetch for LAN instances.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.RelayURL == "" {
				return fmt.Errorf("--relay is required")
			}
			a, err := openAdmin(cmd, load)
			if err != nil {
				return err
			}
			defer a.Close()
			entry, file, verified, err := a.PushEnrol(cmd.Context(), o)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "enrolled (verified: %v); secret written to %s\n", verified, file)
			fmt.Fprintf(w, "add this entry to OPENBEKCI_PUSH_RELAYS and restart:\n  %s\n", entry)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.RelayURL, "relay", "", "relay base URL (https)")
	f.StringVar(&o.AppID, "app-id", "", "the store app the relay serves for you (for example org.openbekci.app)")
	f.StringVar(&o.Invite, "invite", "", "invite code issued by the relay operator")
	f.BoolVar(&o.Manual, "manual", false, "manual enrolment: the relay does not fetch the discovery document")
	f.StringVar(&o.RelayID, "relay-id", "official", "local relay id devices register with")
	f.StringVar(&o.SecretFile, "secret-file", "", "where to write the secret (default DATA_DIR/push-relay-<relay-id>.secret)")
	_ = cmd.MarkFlagRequired("app-id")
	return cmd
}
