package cmd

import (
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/spf13/cobra"

	"hookspot/internal/api"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage the active hookspot project",
}

var projectListCmd = &cobra.Command{
	Use:         "list",
	Short:       "List projects accessible to your account",
	Annotations: commandAnnotations(true),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := resolveCommandConfig(cmd, false)
		if err != nil {
			return wrapCommandError("resolve project configuration", configRecoveryHint(), err)
		}
		if cfg.CLIKey == "" {
			return loginRequiredError()
		}

		client := api.New(activeEndpoint, cfg.CLIKey)
		projects, err := client.ListProjects(cmd.Context())
		if err != nil {
			return fmt.Errorf("list projects: %w", err)
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "UID\tOrganization\tProject")
		for _, p := range projects {
			fmt.Fprintf(w, "%s\t%s\t%s\n", safeDisplayText(p.UID), safeDisplayText(p.Organization.Name), safeDisplayText(p.Name))
		}
		return w.Flush()
	},
}

var projectUseCmd = &cobra.Command{
	Use:         "use [project-uid]",
	Short:       "Set the active hookspot project",
	Args:        cobra.MaximumNArgs(1),
	Annotations: commandAnnotations(true),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := resolveCommandConfig(cmd, len(args) == 0)
		if err != nil {
			return wrapCommandError("resolve project configuration", configRecoveryHint(), err)
		}
		if cfg.CLIKey == "" {
			return loginRequiredError()
		}

		client := api.New(activeEndpoint, cfg.CLIKey)
		var project *api.Project
		if len(args) == 1 {
			project, err = client.GetProject(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("validate project %q: %w", args[0], err)
			}
		} else {
			projects, err := client.ListProjects(cmd.Context())
			if err != nil {
				return fmt.Errorf("list projects: %w", err)
			}

			project, err = selectProject(projects, cfg.Project, surveyProjectPrompt)
			if errors.Is(err, terminal.InterruptErr) {
				return nil
			}
			if err != nil {
				return err
			}
		}

		if err := store.SaveProject(project.UID); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Active project set to %s\n", projectDisplayName(*project))
		return nil
	},
}

func surveyProjectPrompt(options []string, defaultIndex int) (int, error) {
	prompt := &survey.Select{Message: "Select Project", Options: options}
	if defaultIndex >= 0 {
		prompt.Default = defaultIndex
	}

	var selectedIndex int
	if err := survey.AskOne(prompt, &selectedIndex); err != nil {
		return 0, err
	}
	return selectedIndex, nil
}

func selectProject(projects []api.Project, currentUID string, prompt func([]string, int) (int, error)) (*api.Project, error) {
	if len(projects) == 0 {
		return nil, fmt.Errorf("no projects found")
	}
	if len(projects) == 1 {
		return &projects[0], nil
	}

	options := make([]string, len(projects))
	defaultIndex := -1
	for i, project := range projects {
		options[i] = projectDisplayName(project)
		if project.UID == currentUID {
			defaultIndex = i
		}
	}

	selectedIndex, err := prompt(options, defaultIndex)
	if err != nil {
		return nil, err
	}
	if selectedIndex < 0 || selectedIndex >= len(projects) {
		return nil, fmt.Errorf("invalid project selection")
	}
	return &projects[selectedIndex], nil
}

func projectDisplayName(project api.Project) string {
	return safeDisplayText(project.Organization.Name) + " | " + safeDisplayText(project.Name)
}

func init() {
	projectCmd.AddCommand(projectListCmd, projectUseCmd)
	rootCmd.AddCommand(projectCmd)
}
