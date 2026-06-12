package cmd

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hookspot-cli/internal/config"
	"hookspot-cli/internal/proxy"
	"hookspot-cli/internal/ws"
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
		if cfg.Token == "" {
			return fmt.Errorf("not logged in: run 'hookspot-cli login' or set HOOKSPOT_TOKEN")
		}
		if cfg.Project == "" {
			return fmt.Errorf("no active project: run 'hookspot-cli project use <project>' or set HOOKSPOT_PROJECT")
		}

		if len(sources) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Listening for all sources in project %s, forwarding to http://%s:%s%s\n", cfg.Project, forwardHost, port, listenPath)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Listening for sources %s in project %s, forwarding to http://%s:%s%s\n", strings.Join(sources, ", "), cfg.Project, forwardHost, port, listenPath)
		}

		query := url.Values{}
		query.Set("project", cfg.Project)
		for _, source := range sources {
			query.Add("source", source)
		}

		wsURL := strings.Replace(cfg.ServerURL, "http", "ws", 1) + "/cli/listen?" + query.Encode()

		wsClient := ws.New(wsURL, cfg.Token)
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
