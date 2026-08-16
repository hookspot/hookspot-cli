package cmd

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hookspot/internal/api"
	"hookspot/internal/config"
	"hookspot/internal/printer"
	"hookspot/internal/proxy"
	"hookspot/internal/ws"
)

const reconnectDelay = 2 * time.Second

var (
	forwardTo string
	printBody bool
)

var listenCmd = &cobra.Command{
	Use:   "listen [source...]",
	Short: "Print hookspot webhook events, optionally forwarding them to a local server",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		sourceNames := args

		cfg := config.Load(v)
		if cfg.CLIKey == "" {
			return fmt.Errorf("not logged in: run 'hookspot login' or set HOOKSPOT_CLI_KEY")
		}
		if (cfg.OrganizationSlug == "") != (cfg.ProjectSlug == "") {
			return fmt.Errorf("project selection requires both HOOKSPOT_ORGANIZATION_SLUG and HOOKSPOT_PROJECT_SLUG")
		}
		if cfg.Project == "" && cfg.OrganizationSlug == "" {
			return fmt.Errorf("no active project: run 'hookspot project use <project>' or set HOOKSPOT_ORGANIZATION_SLUG and HOOKSPOT_PROJECT_SLUG")
		}

		srvURL, err := requireServerURL()
		if err != nil {
			return err
		}

		client := api.New(srvURL, cfg.CLIKey)
		var project *api.Project
		if cfg.OrganizationSlug != "" {
			project, err = client.GetProjectBySlugs(cmd.Context(), cfg.OrganizationSlug, cfg.ProjectSlug)
		} else {
			project, err = client.GetProject(cmd.Context(), cfg.Project)
		}
		if err != nil {
			return fmt.Errorf("resolve project: %w", err)
		}
		availableSources, err := client.ListProjectSources(cmd.Context(), project.UID)
		if err != nil {
			return fmt.Errorf("list project sources: %w", err)
		}
		sources, sourceUIDs, err := resolveSources(project, availableSources, sourceNames)
		if err != nil {
			return err
		}

		p := printer.New(cmd.OutOrStdout(), printBody)
		var handler ws.Handler = p.Handle
		target := ""
		if forwardTo != "" {
			target = forwardBaseURL(forwardTo)
			forwarder := proxy.New(target)
			handler = p.Wrap(func(d ws.Delivery) (ws.Response, error) {
				resp, err := forwarder.Forward(cmd.Context(), d.Method, d.Path, d.Query, d.Body, d.Headers)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "forward error: %v\n", err)
					return ws.Response{Status: http.StatusBadGateway}, nil
				}
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "read response body: %v\n", err)
					return ws.Response{Status: http.StatusBadGateway}, nil
				}

				return ws.Response{
					Status:  resp.StatusCode,
					Headers: resp.Header,
					Body:    body,
				}, nil
			})
		}

		printListenInfo(cmd.OutOrStdout(), sources, target)

		wsURL := strings.Replace(srvURL, "http", "ws", 1) + "/cli/websocket?vsn=2.0.0"
		topic := "project:" + project.UID

		wsClient := ws.New(wsURL, cfg.CLIKey, topic, sourceUIDs)

		for {
			err := wsClient.Listen(cmd.Context(), handler)
			if cmd.Context().Err() != nil {
				return cmd.Context().Err()
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "connection error: %v, reconnecting in %s...\n", err, reconnectDelay)
			time.Sleep(reconnectDelay)
		}
	},
}

func formatProjectLabel(project *api.Project) string {
	return project.Organization.Slug + "/" + project.Slug
}

func resolveSources(project *api.Project, availableSources []api.Source, sourceNames []string) ([]api.Source, []string, error) {
	if len(sourceNames) == 0 {
		selectedSources := sourcesWithConnections(availableSources)
		if len(selectedSources) == 0 {
			return nil, nil, fmt.Errorf("no matching connections found")
		}
		return selectedSources, nil, nil
	}

	availableByName := make(map[string]api.Source, len(availableSources))
	for _, source := range availableSources {
		availableByName[source.Name] = source
	}

	selectedSources := make([]api.Source, 0, len(sourceNames))
	sourceUIDs := make([]string, 0, len(sourceNames))
	for _, sourceName := range sourceNames {
		source, ok := availableByName[sourceName]
		if !ok {
			return nil, nil, fmt.Errorf("source %q is not present in project %s", sourceName, formatProjectLabel(project))
		}
		if len(source.Connections) == 0 {
			continue
		}
		selectedSources = append(selectedSources, source)
		sourceUIDs = append(sourceUIDs, source.UID)
	}
	if len(selectedSources) == 0 {
		return nil, nil, fmt.Errorf("no matching connections found")
	}

	return selectedSources, sourceUIDs, nil
}

func sourcesWithConnections(sources []api.Source) []api.Source {
	selected := make([]api.Source, 0, len(sources))
	for _, source := range sources {
		if len(source.Connections) > 0 {
			selected = append(selected, source)
		}
	}
	return selected
}

func printListenInfo(out io.Writer, sources []api.Source, target string) {
	connectionCount := 0
	for _, source := range sources {
		connectionCount += len(source.Connections)
	}

	sourceSuffix := "s"
	if len(sources) == 1 {
		sourceSuffix = ""
	}
	connectionSuffix := "s"
	if connectionCount == 1 {
		connectionSuffix = ""
	}
	fmt.Fprintf(out, "Listening on %d source%s • %d connection%s\n", len(sources), sourceSuffix, connectionCount, connectionSuffix)

	for _, source := range sources {
		fmt.Fprintln(out)
		fmt.Fprintln(out, source.Name)
		if target == "" {
			fmt.Fprintf(out, "├ Requests to → %s\n", source.URL)
			fmt.Fprintln(out, "└ Output      → terminal")
		} else {
			fmt.Fprintf(out, "│  Requests to → %s\n", source.URL)
			for i, connection := range source.Connections {
				branch := "├─"
				if i == len(source.Connections)-1 {
					branch = "└─"
				}
				label := connectionLabel(connection)
				if label != "" {
					label = " (" + label + ")"
				}
				fmt.Fprintf(out, "%s Forwards to → %s%s\n", branch, proxy.ForwardURL(target, connection.Destination.Path), label)
			}
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Events ────────────────────────────────────────")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Waiting for events...")
}

func connectionLabel(connection api.Connection) string {
	if connection.Name != nil && *connection.Name != "" {
		return *connection.Name
	}
	return ""
}

// forwardBaseURL defaults a scheme-less --forward-to value to http.
func forwardBaseURL(s string) string {
	if !strings.Contains(s, "://") {
		return "http://" + s
	}
	return s
}

func init() {
	listenCmd.Flags().StringVar(&forwardTo, "forward-to", "", "base URL to forward events to, e.g. localhost:3000 (deliveries keep their own path; omit to only print)")
	listenCmd.Flags().BoolVar(&printBody, "print-body", false, "print request bodies, not just summary lines")
	rootCmd.AddCommand(listenCmd)
}
