package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newResealCmd(load configLoader) *cobra.Command {
	return &cobra.Command{
		Use:   "reseal-national-ids",
		Short: "Rewrite national ids sealed in the old format under the current key id",
		Long: "National ids are stored as \"<key id>:<ciphertext>\". Values written before key ids existed have no prefix;\n" +
			"this opens them with OPENBEKCI_ENCRYPTION_KEY and seals them again. Safe to repeat.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := openAdmin(cmd, load)
			if err != nil {
				return err
			}
			defer a.Close()
			done, failed, err := a.ResealNationalIDs(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "resealed %d national ids; %d could not be opened with the configured key\n", done, failed)
			return nil
		},
	}
}
