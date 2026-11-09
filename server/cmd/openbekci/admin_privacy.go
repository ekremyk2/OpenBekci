package main

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func newPrivacyCmd(load configLoader) *cobra.Command {
	cmd := &cobra.Command{Use: "privacy", Short: "Privacy requests"}
	cmd.AddCommand(newPrivacyEraseCmd(load))
	return cmd
}

func newPrivacyEraseCmd(load configLoader) *cobra.Command {
	var org, person, reason string
	var force bool
	cmd := &cobra.Command{
		Use:   "erase",
		Short: "Erase a person for a privacy request (irreversible)",
		Long: "Pseudonymizes the person: name, contact, notes and national id go, the sign-in account is unlinked and its sessions and\n" +
			"devices deleted; ledger lines, ballots, attendance and the audit trail keep their rows pointing at \"Former person xxxxxxxx\".\n" +
			"Refused while the person has open charges unless --force is given (audited as forced). The event is audited as a system event.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if org == "" || person == "" {
				return errors.New("--org and --person are required")
			}
			personID, err := uuid.Parse(person)
			if err != nil {
				return fmt.Errorf("--person must be a person id: %w", err)
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
			res, err := a.ErasePerson(cmd.Context(), orgID, personID, reason, force)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "erased person %s (open charges at the time: %d)\n", personID, res.OpenItems)
			return nil
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization id or slug")
	cmd.Flags().StringVar(&person, "person", "", "person id")
	cmd.Flags().StringVar(&reason, "reason", "operator request", "why (audited)")
	cmd.Flags().BoolVar(&force, "force", false, "erase although the person has open charges")
	return cmd
}
