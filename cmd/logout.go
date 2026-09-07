package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:         "logout",
	Short:       "Remove the stored hookspot CLI key",
	Annotations: commandAnnotations(false),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := store.ClearCLIKey(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Logged out.")
		if store.EnvironmentCLIKeyActive() {
			fmt.Fprintln(cmd.OutOrStdout(), "An environment-provided CLI key is still active; unset it to finish logging out.")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
