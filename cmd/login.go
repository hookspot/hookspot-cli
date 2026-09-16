package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"hookspot/internal/api"
)

var loginCmd = &cobra.Command{
	Use:         "login",
	Short:       "Authenticate hookspot with a CLI key",
	Annotations: commandAnnotations(true),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := resolveCommandConfig(cmd, false)
		if err != nil {
			return wrapCommandError("resolve login configuration", configRecoveryHint(), err)
		}

		cliKey := cfg.CLIKey
		if cliKey == "" {
			line, err := readLoginKey(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
			_, newlineErr := fmt.Fprintln(cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("read CLI key: %w", err)
			}
			if newlineErr != nil {
				return fmt.Errorf("write CLI key prompt: %w", newlineErr)
			}
			cliKey = line
		}

		if cliKey == "" {
			return newCommandError("no CLI key provided", "Pass a CLI key when prompted or set HOOKSPOT_CLI_KEY.")
		}

		client := api.New(activeEndpoint, cliKey)
		user, err := client.Me(cmd.Context())
		if err != nil {
			return fmt.Errorf("validate CLI key: %w", err)
		}

		if err := store.SaveCLIKey(cliKey); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Logged in as %s\n", safeDisplayText(user.Email))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}
