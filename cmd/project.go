package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"hookspot-cli/internal/api"
	"hookspot-cli/internal/config"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage the active hookspot project",
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List projects accessible to your account",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load(v)
		if cfg.Token == "" {
			return fmt.Errorf("not logged in: run 'hookspot-cli login' or set HOOKSPOT_TOKEN")
		}

		client := api.New(cfg.ServerURL, cfg.Token)
		projects, err := client.ListProjects(cmd.Context())
		if err != nil {
			return fmt.Errorf("list projects: %w", err)
		}

		for _, p := range projects {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", p.ID, p.Name)
		}
		return nil
	},
}

var projectUseCmd = &cobra.Command{
	Use:   "use <project>",
	Short: "Set the active hookspot project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v.Set("project", args[0])
		if err := config.Save(v, cfgFile); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Active project set to %s\n", args[0])
		return nil
	},
}

func init() {
	projectCmd.AddCommand(projectListCmd, projectUseCmd)
	rootCmd.AddCommand(projectCmd)
}
