package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"hookspot/internal/api"
	"hookspot/internal/config"
	"hookspot/internal/endpoint"
)

var projectUseLocal bool

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
	Use:   "use [ORGANIZATION [PROJECT]]",
	Short: "Set the active hookspot project",
	Long: `Set the active hookspot project.

With no arguments, choose from every accessible project. One argument is
resolved as a project UID first; if no such UID exists, it is matched as an
exact organization name. Two arguments match exact organization and project
names. Name matching ignores case.`,
	Example: `  hookspot project use
  hookspot project use PROJECT_UID
  hookspot project use "Acme Inc."
  hookspot project use "Acme Inc." Payments
  hookspot project use --local "Acme Inc." Payments`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) > 2 {
			return newCommandError("project selection accepts at most an organization and project", projectUseHint())
		}
		for _, arg := range args {
			if arg == "" {
				return newCommandError("project selection arguments must not be empty", projectUseHint())
			}
		}
		return nil
	},
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
		project, err := resolveProjectSelection(cmd.Context(), client, args, store.SavedProject(), cmd.InOrStdin(), cmd.OutOrStdout(), promptProject)
		if err != nil {
			return err
		}
		return persistProjectSelection(cmd.Context(), store, *project, cmd.OutOrStdout())
	},
}

type projectAPI interface {
	GetProject(context.Context, string) (*api.Project, error)
	ListProjects(context.Context) ([]api.Project, error)
}

type projectPrompt func(context.Context, io.Reader, io.Writer, []string, int) (int, error)

func resolveProjectSelection(ctx context.Context, client projectAPI, args []string, currentUID string, in io.Reader, out io.Writer, prompt projectPrompt) (*api.Project, error) {
	if len(args) == 1 {
		if _, err := endpoint.Segment(args[0]); err == nil {
			project, err := client.GetProject(ctx, args[0])
			if err == nil {
				return project, nil
			}
			var apiErr *api.Error
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
				return nil, fmt.Errorf("validate project %q: %w", args[0], err)
			}
		}
	}

	projects, err := client.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	candidates, err := projectCandidates(projects, args)
	if err != nil {
		return nil, err
	}
	return selectProject(ctx, in, out, candidates, currentUID, prompt)
}

func projectCandidates(projects []api.Project, args []string) ([]api.Project, error) {
	if len(args) == 0 {
		if len(projects) == 0 {
			return nil, errors.New("no projects are accessible to this account")
		}
		return projects, nil
	}

	candidates := make([]api.Project, 0)
	for _, project := range projects {
		if !strings.EqualFold(project.Organization.Name, args[0]) {
			continue
		}
		if len(args) == 2 && !strings.EqualFold(project.Name, args[1]) {
			continue
		}
		candidates = append(candidates, project)
	}
	if len(candidates) == 0 {
		if len(args) == 1 {
			return nil, fmt.Errorf("no projects match organization %q", safeDisplayText(args[0]))
		}
		return nil, fmt.Errorf("no projects match %q in organization %q", safeDisplayText(args[1]), safeDisplayText(args[0]))
	}
	if len(args) == 2 && len(candidates) > 1 {
		return nil, fmt.Errorf("multiple projects match %q in organization %q", safeDisplayText(args[1]), safeDisplayText(args[0]))
	}
	return candidates, nil
}

func selectProject(ctx context.Context, in io.Reader, out io.Writer, projects []api.Project, currentUID string, prompt projectPrompt) (*api.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return nil, errors.New("no projects are available for selection")
	}
	if len(projects) == 1 {
		return &projects[0], nil
	}

	options := make([]string, len(projects))
	defaultIndex := 0
	for index, project := range projects {
		options[index] = projectDisplayName(project)
		if project.UID == currentUID {
			defaultIndex = index
			options[index] += " (current)"
		}
	}
	selectedIndex, err := prompt(ctx, in, out, options, defaultIndex)
	if err != nil {
		return nil, err
	}
	if selectedIndex < 0 || selectedIndex >= len(projects) {
		return nil, errors.New("invalid project selection")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &projects[selectedIndex], nil
}

func persistProjectSelection(ctx context.Context, store *config.Store, project api.Project, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.SaveProject(project.UID); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	if _, err := fmt.Fprintf(out, "Active project set to %s\n", projectDisplayName(project)); err != nil {
		return fmt.Errorf("write project selection: %w", err)
	}
	return nil
}

func projectUseHint() string {
	return "Run 'hookspot project use --help' for selection forms."
}

func projectDisplayName(project api.Project) string {
	return safeDisplayText(project.Organization.Name) + " | " + safeDisplayText(project.Name)
}

func init() {
	projectUseCmd.Flags().BoolVar(&projectUseLocal, "local", false, "save selection in .hookspot for the current directory")
	projectCmd.AddCommand(projectListCmd, projectUseCmd)
	rootCmd.AddCommand(projectCmd)
}
