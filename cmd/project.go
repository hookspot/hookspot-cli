package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"hookspot/internal/api"
	"hookspot/internal/config"
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
		if cfg.CLIKey == "" {
			return fmt.Errorf("not logged in: run 'hookspot login' or set HOOKSPOT_CLI_KEY")
		}

		url, err := requireServerURL()
		if err != nil {
			return err
		}

		client := api.New(url, cfg.CLIKey)
		projects, err := client.ListProjects(cmd.Context())
		if err != nil {
			return fmt.Errorf("list projects: %w", err)
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "UID\tOrganization\tProject")
		for _, p := range projects {
			fmt.Fprintf(w, "%s\t%s\t%s\n", p.UID, p.Organization.Name, p.Name)
		}
		return w.Flush()
	},
}

var projectUseCmd = &cobra.Command{
	Use:   "use <project>",
	Short: "Set the active hookspot project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		uid := args[0]

		cfg := config.Load(v)
		if cfg.CLIKey == "" {
			return fmt.Errorf("not logged in: run 'hookspot login' or set HOOKSPOT_CLI_KEY")
		}

		url, err := requireServerURL()
		if err != nil {
			return err
		}

		project, err := api.New(url, cfg.CLIKey).GetProject(cmd.Context(), uid)
		if err != nil {
			return fmt.Errorf("validate project %q: %w", uid, err)
		}

		v.Set("project", uid)
		if err := config.Save(v, cfgFile); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Active project set to %s/%s (%s)\n", project.Organization.Name, project.Name, uid)
		return nil
	},
}

func init() {
	projectCmd.AddCommand(projectListCmd, projectUseCmd)
	rootCmd.AddCommand(projectCmd)
}
