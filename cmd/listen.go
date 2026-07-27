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
		sources := args

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

		p := printer.New(cmd.OutOrStdout(), printBody)
		var handler ws.Handler = p.Handle
		mode := "printing deliveries (pass --forward-to to forward)"
		if forwardTo != "" {
			target := forwardBaseURL(forwardTo)
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
			mode = "forwarding to " + target
		}

		if len(sources) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Listening for all sources in project %s, %s\n", projectLabel, mode)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Listening for sources %s in project %s, %s\n", strings.Join(sources, ", "), projectLabel, mode)
		}

		wsURL := strings.Replace(srvURL, "http", "ws", 1) + "/cli/websocket?vsn=2.0.0"
		topic := "project:" + cfg.Project

		wsClient := ws.New(wsURL, cfg.CLIKey, topic, sources)

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
