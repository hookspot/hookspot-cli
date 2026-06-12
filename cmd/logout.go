package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"hookspot-cli/internal/config"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the stored hookspot API token",
	RunE: func(cmd *cobra.Command, args []string) error {
		v.Set("token", "")
		if err := config.Save(v, cfgFile); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Logged out.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
