package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the hookspot-cli version",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "hookspot-cli %s\n", version)
		return err
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
