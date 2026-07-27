package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hookspot/internal/config"
)

var (
	cfgFile string
	v       *viper.Viper
)

// serverURL is the hookspot server URL. It must be set at build time with:
//
//	go build -ldflags "-X hookspot/cmd.serverURL=https://..."
//
// A binary built without this flag has serverURL == "" and will refuse to
// run any command that talks to the hookspot server.
var serverURL string

// requireServerURL returns the build-time server URL, or an error if the
// binary was built without one.
func requireServerURL() (string, error) {
	if serverURL == "" {
		return "", fmt.Errorf("hookspot was built without a server URL; rebuild with -ldflags \"-X hookspot/cmd.serverURL=https://...\"")
	}
	return serverURL, nil
}

var rootCmd = &cobra.Command{
	Use:   "hookspot",
	Short: "Forward hookspot webhook events to your local machine",
	Long: `hookspot connects to your hookspot project over a websocket
and proxies incoming webhook events to a local host and port.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		v, err = config.New(cfgFile)
		if err != nil {
			return err
		}

		for _, name := range []string{"cli-key", "project", "log-level"} {
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
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default $HOME/.config/hookspot/config.toml)")
	rootCmd.PersistentFlags().String("cli-key", "", "hookspot CLI key (env HOOKSPOT_CLI_KEY)")
	rootCmd.PersistentFlags().String("project", "", "active hookspot project ID (env HOOKSPOT_PROJECT)")
	rootCmd.PersistentFlags().String("log-level", "", "log level: debug, info, warn, error (env HOOKSPOT_LOG_LEVEL)")
}
