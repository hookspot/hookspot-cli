package cmd

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"hookspot/internal/cards"
)

var logoutCmd = &cobra.Command{
	Use:         "logout",
	Short:       "Remove the stored hookspot CLI key",
	Annotations: commandAnnotations(false),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := store.ClearCLIKey(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		out := cmd.OutOrStdout()
		lipgloss.Fprintln(out, cards.Logout(store.EnvironmentCLIKeyActive(), cards.Width(out)))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
