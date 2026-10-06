package cmd

import (
	"context"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"hookspot/internal/config"
	"hookspot/internal/endpoint"
)

var (
	cfgFile        string
	store          *config.Store
	activeEndpoint endpoint.Base
)

const (
	annotationConfiguration = "hookspot/configuration"
	annotationEndpoint      = "hookspot/endpoint"
)

func commandAnnotations(needsEndpoint bool) map[string]string {
	annotations := map[string]string{annotationConfiguration: "true"}
	if needsEndpoint {
		annotations[annotationEndpoint] = "true"
	}
	return annotations
}

var rootCmd = &cobra.Command{
	Use:           "hookspot",
	Short:         "Forward hookspot webhook events to your local machine",
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       version,
	Long: `hookspot connects to your hookspot project over a websocket
and proxies incoming webhook events to a local host and port.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		activeEndpoint = endpoint.Base{}
		if cmd.Annotations[annotationEndpoint] == "true" {
			base, err := CurrentBuildInfo().networkEndpoint()
			if err != nil {
				return err
			}
			activeEndpoint = base
		}
		if cmd.Annotations[annotationConfiguration] != "true" {
			return nil
		}

		intent := config.Read
		if cmd == loginCmd {
			intent = config.LoginCreate
		}
		var err error
		store, err = config.New(config.Options{
			Prefix:          configPrefix,
			ExplicitPath:    cfgFile,
			ExplicitPathSet: cmd.Flags().Changed("config"),
			Local:           cmd == projectUseCmd && projectUseLocal,
			Intent:          intent,
		})
		if err != nil {
			return wrapCommandError("load configuration", configRecoveryHint(), err)
		}
		return nil
	},
}

// ExecuteContext runs the root command with ctx.
func ExecuteContext(ctx context.Context) error {
	// A Windows console shows styled output's escape sequences as text unless
	// VT processing is on. It stays on: bubbletea restores the mode it found,
	// so the error printed after a full-screen run is styled too.
	lipgloss.EnableLegacyWindowsANSI(os.Stdout)
	lipgloss.EnableLegacyWindowsANSI(os.Stderr)
	return rootCmd.ExecuteContext(ctx)
}

// Execute runs the root command.
func Execute() error {
	return ExecuteContext(context.Background())
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file")
	rootCmd.PersistentFlags().String("cli-key", "", "hookspot CLI key (prefer HOOKSPOT_CLI_KEY)")
	rootCmd.PersistentFlags().String("project", "", "active hookspot project ID")
}

func resolveCommandConfig(cmd *cobra.Command, needProject bool) (config.Config, error) {
	overrides := config.Overrides{NeedProject: needProject}
	if cmd.Flags().Changed("cli-key") {
		value, err := cmd.Flags().GetString("cli-key")
		if err != nil {
			return config.Config{}, err
		}
		overrides.CLIKey = &value
	}
	if cmd.Flags().Changed("project") {
		value, err := cmd.Flags().GetString("project")
		if err != nil {
			return config.Config{}, err
		}
		overrides.Project = &value
	}
	return store.Resolve(overrides)
}

func configRecoveryHint() string {
	return "Check the selected config path and file. For a fresh login, use an unused path with 'hookspot --config PATH login'."
}
