package cmd

import (
	"context"
	"fmt"
	"strings"

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
		if cmd == configMigrateCmd {
			intent = config.MigrationCreate
		}
		var err error
		store, err = config.New(config.Options{
			Environment:     CurrentBuildInfo().Environment,
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
	rootCmd.Use = executableName()
	return rootCmd.ExecuteContext(ctx)
}

// Execute runs the root command.
func Execute() error {
	return ExecuteContext(context.Background())
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "environment-specific config file")
	rootCmd.PersistentFlags().String("cli-key", "", "hookspot CLI key (prefer a scoped environment variable)")
	rootCmd.PersistentFlags().String("project", "", "active hookspot project ID")
	rootCmd.PersistentFlags().String("log-level", "", "deprecated; retained for compatibility")
	if err := rootCmd.PersistentFlags().MarkDeprecated("log-level", "logging is no longer configurable"); err != nil {
		panic(err)
	}
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
	name := executableName()
	return fmt.Sprintf("Check the selected config path and file. For a fresh login, use an unused path with '%s --config PATH login'. For a legacy file, see '%s config migrate --help'.", name, name)
}

func executableName() string {
	if CurrentBuildInfo().Environment == "stage" {
		return "hookspot-stage"
	}
	return "hookspot"
}

func scopedVariable(suffix string) string {
	return "HOOKSPOT_" + strings.ToUpper(CurrentBuildInfo().Environment) + "_" + suffix
}
