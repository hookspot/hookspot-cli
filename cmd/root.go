package cmd

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hookspot-cli/internal/config"
)

var (
	cfgFile string
	v       *viper.Viper
)

var rootCmd = &cobra.Command{
	Use:   "hookspot-cli",
	Short: "Forward hookspot webhook events to your local machine",
	Long: `hookspot-cli connects to your hookspot project over a websocket
and proxies incoming webhook events to a local host and port.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		v, err = config.New(cfgFile)
		if err != nil {
			return err
		}

		for _, name := range []string{"token", "project", "server-url", "log-level"} {
			key := strings.ReplaceAll(name, "-", "_")
			if err := v.BindPFlag(key, cmd.Flags().Lookup(name)); err != nil {
				return err
			}
		}

		return nil
	},
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default $HOME/.config/hookspot-cli/config.toml)")
	rootCmd.PersistentFlags().String("token", "", "hookspot API token (env HOOKSPOT_TOKEN)")
	rootCmd.PersistentFlags().String("project", "", "active hookspot project ID (env HOOKSPOT_PROJECT)")
	rootCmd.PersistentFlags().String("server-url", "", "hookspot server URL (env HOOKSPOT_SERVER_URL)")
	rootCmd.PersistentFlags().String("log-level", "", "log level: debug, info, warn, error (env HOOKSPOT_LOG_LEVEL)")
}
