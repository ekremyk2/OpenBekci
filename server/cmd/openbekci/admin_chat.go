package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func newChatCmd(load configLoader) *cobra.Command {
	cmd := &cobra.Command{Use: "chat", Short: "Chat channel (Telegram, WhatsApp)"}
	cmd.AddCommand(newChatSetWebhookCmd(load))
	return cmd
}

func newChatSetWebhookCmd(load configLoader) *cobra.Command {
	var org, provider, url string
	cmd := &cobra.Command{
		Use:   "set-webhook",
		Short: "Register the webhook of an organization's chat bot with the provider",
		Long: "Calls the provider's API (Telegram setWebhook with the secret token header) so that it delivers messages to\n" +
			"{BASE_URL}/v1/orgs/<org>/chat/webhooks/<provider>. The bot credentials come from the organization's chat settings.\n" +
			"WhatsApp has no such call: enter the URL and verify token in the Meta app's webhook settings.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if org == "" || provider == "" {
				return errors.New("--org and --provider are required")
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
			u, err := a.ChatSetWebhook(cmd.Context(), orgID, provider, url)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "webhook registered: %s\n", u)
			return nil
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization id or slug")
	cmd.Flags().StringVar(&provider, "provider", "telegram", "chat provider")
	cmd.Flags().StringVar(&url, "url", "", "webhook URL instead of the one built from the base URL (https)")
	return cmd
}
