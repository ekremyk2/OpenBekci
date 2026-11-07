package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newLoginLinkCmd issues a fresh one-time sign-in link for an existing user
// (the link of `create-org` or `create-platform-admin` is single use and
// expires after 15 minutes).
func newLoginLinkCmd(load configLoader) *cobra.Command {
	var email, phone string
	cmd := &cobra.Command{
		Use:   "login-link",
		Short: "Print a fresh one-time sign-in link for a user",
		Long: "Issues a new single-use sign-in link (valid 15 minutes) for the user with this email or phone\n" +
			"and prints it. Earlier open links of the user are voided.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ident, err := pickContact("", email, phone)
			if err != nil {
				return err
			}
			a, err := openAdmin(cmd, load)
			if err != nil {
				return err
			}
			defer a.Close()
			u, found, err := a.Identity.FindUser(cmd.Context(), ident)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("no user %q", ident)
			}
			if u.Status != "active" {
				return fmt.Errorf("user %s is %s and cannot sign in", u.ID, u.Status)
			}
			return printLoginLink(cmd, a, u)
		},
	}
	contactFlags(cmd, "", &email, &phone)
	return cmd
}
