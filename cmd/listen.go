package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"hookspot/internal/api"
	"hookspot/internal/config"
	"hookspot/internal/printer"
	"hookspot/internal/proxy"
	"hookspot/internal/ws"
)

const reconnectDelay = 2 * time.Second

const maxInitialConnectAttempts = 10

var (
	forwardTo            string
	showSensitiveHeaders bool
	maxBodyLines         int
	maxHeaders           int
	maxValueChars        int
)

var listenCmd = &cobra.Command{
	Use:   "listen [source...]",
	Short: "Print hookspot webhook events, optionally forwarding them to a local server",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		sourceNames := args
		if maxBodyLines < 0 || maxHeaders < 0 || maxValueChars < 0 {
			return newCommandError(commandErrorUsage, "rendering limits must be zero or greater", "Use zero for an unlimited value, or provide a positive limit.")
		}

		cfg := config.Load(v)
		if cfg.CLIKey == "" {
			return loginRequiredError()
		}
		if (cfg.OrganizationSlug == "") != (cfg.ProjectSlug == "") {
			return newCommandError(
				commandErrorConfiguration,
				"project selection requires both HOOKSPOT_ORGANIZATION_SLUG and HOOKSPOT_PROJECT_SLUG",
				"Set both variables, or unset them and run 'hookspot project use'.",
			)
		}
		if cfg.Project == "" && cfg.OrganizationSlug == "" {
			return newCommandError(
				commandErrorConfiguration,
				"no active project",
				"Run 'hookspot project use <project>' or set HOOKSPOT_ORGANIZATION_SLUG and HOOKSPOT_PROJECT_SLUG.",
			)
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

		mode := printer.ModeInspect
		if forwardTo != "" {
			mode = printer.ModeForward
		}
		p := printer.New(cmd.OutOrStdout(), printer.Options{
			Mode:                 mode,
			Sources:              sourceNamesByUID(sources),
			ShowSensitiveHeaders: showSensitiveHeaders,
			Color:                printer.SupportsColor(cmd.OutOrStdout()),
			Limits: printer.Limits{
				MaxBodyLines:  maxBodyLines,
				MaxHeaders:    maxHeaders,
				MaxValueChars: maxValueChars,
			},
		})
		var handler ws.Handler = p.Handle
		target := ""
		var session *forwardSession
		replayEnabled := false
		if forwardTo != "" {
			target = forwardBaseURL(forwardTo)
			session = newForwardSession(cmd.Context(), proxy.New(target), target, p)
			handler = session.Handle
			replayEnabled = isTerminalReader(cmd.InOrStdin())
		}

		printListenInfoWithReplay(cmd.OutOrStdout(), sources, target, replayEnabled)
		if replayEnabled {
			go replayInput(cmd.Context(), cmd.InOrStdin(), session.Replay)
		}

		wsURL := strings.Replace(srvURL, "http", "ws", 1) + "/cli/websocket?vsn=2.0.0"
		topic := "project:" + project.UID

		wsClient := ws.New(wsURL, cfg.CLIKey, topic, sourceUIDs)

		return superviseListen(cmd.Context(), cmd.ErrOrStderr(), wsClient, handler, reconnectPolicy{
			Delay:              reconnectDelay,
			MaxInitialAttempts: maxInitialConnectAttempts,
		})
	},
}

type websocketListener interface {
	Listen(context.Context, ws.Handler) error
}

type reconnectPolicy struct {
	Delay              time.Duration
	MaxInitialAttempts int
}

// superviseListen keeps transient WebSocket failures inside the long-running
// command. Authentication, protocol, and handler failures are fatal; an
// initial connection is bounded, while a session that connected once retries
// until cancellation.
func superviseListen(ctx context.Context, errOut io.Writer, listener websocketListener, handler ws.Handler, policy reconnectPolicy) error {
	initialAttempts := 0
	connectedOnce := false

	for {
		err := listener.Listen(ctx, handler)
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return nil
		}
		if err == nil {
			return nil
		}

		var sessionErr *ws.SessionError
		if errors.As(err, &sessionErr) {
			if sessionErr.Connected {
				connectedOnce = true
				initialAttempts = 0
			}
			if !sessionErr.Retryable() {
				return fmt.Errorf("listen: %w", err)
			}
		}

		if !connectedOnce {
			initialAttempts++
			if policy.MaxInitialAttempts > 0 && initialAttempts >= policy.MaxInitialAttempts {
				return wrapCommandError(
					commandErrorRuntime,
					fmt.Sprintf("could not connect to Hookspot after %d attempts", initialAttempts),
					"Check your network connection and the Hookspot server URL, then try again.",
					err,
				)
			}
		}

		fmt.Fprintf(errOut, "connection error: %v, reconnecting in %s...\n", err, policy.Delay)
		if !waitForReconnect(ctx, policy.Delay) {
			return nil
		}
	}
}

func waitForReconnect(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
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

func sourceNamesByUID(sources []api.Source) map[string]string {
	names := make(map[string]string, len(sources))
	for _, source := range sources {
		names[source.UID] = source.Name
	}
	return names
}

func printListenInfo(out io.Writer, sources []api.Source, target string) {
	printListenInfoWithReplay(out, sources, target, false)
}

func printListenInfoWithReplay(out io.Writer, sources []api.Source, target string, replay bool) {
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
	fmt.Fprintln(out, "Requests ──────────────────────────────────────")
	fmt.Fprintln(out)
	if replay {
		fmt.Fprintln(out, "↵ replay last request")
	}
	fmt.Fprintln(out, "Waiting for requests...")
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

type forwardSession struct {
	ctx       context.Context
	forwarder deliveryForwarder
	target    string
	printer   *printer.Printer
	cache     replayCache
	now       func() time.Time
}

type deliveryForwarder interface {
	Forward(context.Context, string, string, string, []byte, http.Header) (*http.Response, error)
}

func newForwardSession(ctx context.Context, forwarder deliveryForwarder, target string, output *printer.Printer) *forwardSession {
	return &forwardSession{
		ctx:       ctx,
		forwarder: forwarder,
		target:    target,
		printer:   output,
		now:       time.Now,
	}
}

func (s *forwardSession) Handle(delivery ws.Delivery) (ws.Response, error) {
	s.cache.Store(delivery)
	outcome := s.forward(delivery, false)
	if outcome.Failure != nil {
		return ws.Response{
			Status:    http.StatusBadGateway,
			LatencyMS: latencyMilliseconds(outcome.Latency),
		}, nil
	}
	return outcome.Response, nil
}

func (s *forwardSession) Replay() {
	delivery, ok := s.cache.Load()
	if !ok {
		return
	}
	s.forward(delivery, true)
}

func (s *forwardSession) forward(delivery ws.Delivery, replay bool) printer.ForwardOutcome {
	started := s.now()
	response, err := s.forwarder.Forward(
		s.ctx,
		delivery.Method,
		delivery.Path,
		delivery.Query,
		delivery.Body,
		delivery.Headers,
	)
	if err != nil {
		outcome := printer.ForwardOutcome{
			Latency:   s.now().Sub(started),
			Failure:   proxy.Failure(err),
			TargetURL: s.target,
			Replay:    replay,
		}
		s.printer.PrintForward(delivery, outcome)
		return outcome
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	latency := s.now().Sub(started)
	if err != nil {
		outcome := printer.ForwardOutcome{
			Latency:   latency,
			Failure:   proxy.Failure(err),
			TargetURL: s.target,
			Replay:    replay,
		}
		s.printer.PrintForward(delivery, outcome)
		return outcome
	}

	outcome := printer.ForwardOutcome{
		Response: ws.Response{
			Status:    response.StatusCode,
			Headers:   response.Header.Clone(),
			Body:      body,
			LatencyMS: latencyMilliseconds(latency),
		},
		Latency:   latency,
		TargetURL: s.target,
		Replay:    replay,
	}
	s.printer.PrintForward(delivery, outcome)
	return outcome
}

func latencyMilliseconds(latency time.Duration) int64 {
	if latency <= 0 {
		return 0
	}
	rounded := latency.Round(time.Millisecond)
	if rounded < time.Millisecond {
		return 1
	}
	return rounded.Milliseconds()
}

type replayCache struct {
	mu       sync.RWMutex
	delivery *ws.Delivery
}

func (c *replayCache) Store(delivery ws.Delivery) {
	copy := cloneDelivery(delivery)
	c.mu.Lock()
	c.delivery = &copy
	c.mu.Unlock()
}

func (c *replayCache) Load() (ws.Delivery, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.delivery == nil {
		return ws.Delivery{}, false
	}
	return cloneDelivery(*c.delivery), true
}

func cloneDelivery(delivery ws.Delivery) ws.Delivery {
	delivery.Body = append([]byte(nil), delivery.Body...)
	delivery.Headers = delivery.Headers.Clone()
	return delivery
}

func replayInput(ctx context.Context, input io.Reader, replay func()) {
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if scanner.Text() == "" {
			replay()
		}
	}
}

func isTerminalReader(input io.Reader) bool {
	fd, ok := input.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(fd.Fd()))
}

func init() {
	listenCmd.Flags().StringVar(&forwardTo, "forward-to", "", "base URL to forward events to, e.g. localhost:3000 (deliveries keep their own path; omit to only print)")
	listenCmd.Flags().BoolVar(&showSensitiveHeaders, "show-sensitive-headers", false, "show authorization and cookie header values")
	listenCmd.Flags().IntVar(&maxBodyLines, "max-body-lines", 12, "maximum body lines to print (0 for unlimited)")
	listenCmd.Flags().IntVar(&maxHeaders, "max-headers", 20, "maximum headers to print (0 for unlimited)")
	listenCmd.Flags().IntVar(&maxValueChars, "max-value-chars", 160, "maximum characters per value or body line (0 for unlimited)")
	rootCmd.AddCommand(listenCmd)
}
