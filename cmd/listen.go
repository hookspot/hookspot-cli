package cmd

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hookspot/internal/api"
	"hookspot/internal/config"
	"hookspot/internal/proxy"
	"hookspot/internal/ws"
)

const reconnectDelay = 2 * time.Second

var (
	listenPath  string
	forwardHost string
)

var listenCmd = &cobra.Command{
	Use:   "listen <port> [source...]",
	Short: "Forward hookspot webhook events to a local port",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		port := args[0]
		sources := args[1:]

		cfg := config.Load(v)
		if cfg.CLIKey == "" {
			return fmt.Errorf("not logged in: run 'hookspot login' or set HOOKSPOT_CLI_KEY")
		}
		if cfg.Project == "" {
			return fmt.Errorf("no active project: run 'hookspot project use <project>' or set HOOKSPOT_PROJECT")
		}

		srvURL, err := requireServerURL()
		if err != nil {
			return err
		}

		project, err := api.New(srvURL, cfg.CLIKey).GetProject(cmd.Context(), cfg.Project)
		if err != nil {
			return fmt.Errorf("resolve project: %w", err)
		}
		projectLabel := project.Organization.Name + "/" + project.Name

		if len(sources) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Listening for all sources in project %s, forwarding to http://%s:%s%s\n", projectLabel, forwardHost, port, listenPath)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Listening for sources %s in project %s, forwarding to http://%s:%s%s\n", strings.Join(sources, ", "), projectLabel, forwardHost, port, listenPath)
		}

		query := url.Values{}
		query.Set("project", cfg.Project)
		for _, source := range sources {
			query.Add("source", source)
		}

		wsURL := strings.Replace(srvURL, "http", "ws", 1) + "/cli/websocket?" + query.Encode()

		wsClient := ws.New(wsURL, cfg.CLIKey)
		forwarder := proxy.New("http://" + forwardHost + ":" + port)

		handler := func(message []byte) error {
			resp, err := forwarder.Forward(cmd.Context(), listenPath, message, nil)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "forward error: %v\n", err)
				return nil
			}
			defer resp.Body.Close()

			fmt.Fprintf(cmd.OutOrStdout(), "forwarded event -> %d\n", resp.StatusCode)
			return nil
		}

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

func init() {
	listenCmd.Flags().StringVar(&listenPath, "path", "/", "path to forward events to on the local server")
	listenCmd.Flags().StringVar(&forwardHost, "forward-host", "localhost", "host to forward events to (use host.docker.internal when running in Docker)")
	rootCmd.AddCommand(listenCmd)
}
