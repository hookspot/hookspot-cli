package cmd

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"hookspot-cli/internal/api"
	"hookspot-cli/internal/config"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate hookspot-cli with a personal access token",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load(v)

		token := cfg.Token
		if token == "" {
			fmt.Fprint(cmd.OutOrStdout(), "Enter your hookspot API token: ")
			reader := bufio.NewReader(cmd.InOrStdin())
			line, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("read token: %w", err)
			}
			token = strings.TrimSpace(line)
		}

		if token == "" {
			return fmt.Errorf("no token provided")
		}

		client := api.New(cfg.ServerURL, token)
		user, err := client.Me(cmd.Context())
		if err != nil {
			return fmt.Errorf("validate token: %w", err)
		}

		v.Set("token", token)
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
