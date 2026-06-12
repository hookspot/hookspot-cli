package cmd

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "hookspot-cli",
	Short: "Forward hookspot webhook events to your local machine",
	Long: `hookspot-cli connects to your hookspot project over a websocket
and proxies incoming webhook events to a local host and port.`,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
