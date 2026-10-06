package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"hookspot/internal/api"
	"hookspot/internal/browser"
	"hookspot/internal/cards"
)

var loginInteractive bool

var loginCmd = &cobra.Command{
	Use:         "login",
	Short:       "Authenticate hookspot via the browser",
	Annotations: commandAnnotations(true),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := resolveCommandConfig(cmd, false)
		if err != nil {
			return wrapCommandError("resolve login configuration", configRecoveryHint(), err)
		}

		explicitKey := explicitLoginKey(cmd)
		if explicitKey || loginInteractive {
			cliKey := ""
			if explicitKey {
				cliKey = cfg.CLIKey
			}
			return runKeyLogin(cmd.Context(), cliKey, cmd.InOrStdin(), cmd.OutOrStdout())
		}

		return runBrowserLogin(cmd.Context(), browserLoginDeps{
			api:          api.New(activeEndpoint, "", userAgent()),
			endpoint:     activeEndpoint,
			openBrowser:  browser.Open,
			store:        store,
			pollInterval: browserLoginPollInterval,
			out:          cmd.OutOrStdout(),
		})
	},
}

// explicitLoginKey reports whether this invocation supplied a CLI key through
// the flag or environment. A key that is merely saved in config is not an
// explicit key and must still start the browser flow.
func explicitLoginKey(cmd *cobra.Command) bool {
	if cmd.Flags().Changed("cli-key") {
		return true
	}
	value, set := os.LookupEnv("HOOKSPOT_CLI_KEY")
	return set && value != ""
}

func runKeyLogin(ctx context.Context, cliKey string, in io.Reader, out io.Writer) error {
	if cliKey == "" {
		line, err := readLoginKey(ctx, in, out)
		_, newlineErr := fmt.Fprintln(out)
		if err != nil {
			return fmt.Errorf("read CLI key: %w", err)
		}
		if newlineErr != nil {
			return fmt.Errorf("write CLI key prompt: %w", newlineErr)
		}
		cliKey = line
	}

	if cliKey == "" {
		return newCommandError("no CLI key provided", "Pass a CLI key when prompted, set HOOKSPOT_CLI_KEY, or run 'hookspot login' to authenticate via the browser.")
	}

	client := api.New(activeEndpoint, cliKey, userAgent())
	user, err := client.Me(ctx)
	if err != nil {
		return fmt.Errorf("validate CLI key: %w", err)
	}

	if err := store.SaveCLIKey(cliKey); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if _, err := lipgloss.Fprintln(out, cards.Done("Logged in as", user.Email)); err != nil {
		return fmt.Errorf("write login: %w", err)
	}
	return nil
}

func init() {
	loginCmd.Flags().BoolVarP(&loginInteractive, "interactive", "i", false, "enter a CLI key interactively")
	rootCmd.AddCommand(loginCmd)
}
