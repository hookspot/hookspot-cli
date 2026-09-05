package cmd

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"hookspot/internal/api"
	"hookspot/internal/config"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate hookspot with a CLI key",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load(v)

		cliKey := cfg.CLIKey
		if cliKey == "" {
			fmt.Fprint(cmd.OutOrStdout(), "Enter your hookspot CLI key: ")
			reader := bufio.NewReader(cmd.InOrStdin())
			line, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("read CLI key: %w", err)
			}
			cliKey = strings.TrimSpace(line)
		}

		if cliKey == "" {
			return newCommandError(commandErrorAuthentication, "no CLI key provided", "Pass a CLI key when prompted or set HOOKSPOT_CLI_KEY.")
		}

		url, err := requireServerURL()
		if err != nil {
			return err
		}

		client := api.New(url, cliKey)
		user, err := client.Me(cmd.Context())
		if err != nil {
			return fmt.Errorf("validate CLI key: %w", err)
		}

		v.Set("cli_key", cliKey)
		if err := config.Save(v, cfgFile); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Logged in as %s\n", user.Email)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}
