package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"hookspot/internal/api"
	"hookspot/internal/printer"
	"hookspot/internal/proxy"
	"hookspot/internal/ws"
)

const reconnectDelay = 2 * time.Second

const maxInitialConnectAttempts = 10

const maxLocalResponseBodyBytes = 16 * 1024 * 1024

var errLocalResponseBodyTooLarge = errors.New("local response body exceeds 16 MiB limit")

var (
	forwardTo            string
	showSensitiveHeaders bool
	maxBodyLines         int
	maxHeaders           int
	maxValueChars        int
)

var listenCmd = &cobra.Command{
	Use:         "listen [source...]",
	Short:       "Print hookspot webhook events, optionally forwarding them to a local server",
	Args:        cobra.ArbitraryArgs,
	Annotations: commandAnnotations(true),
	RunE: func(cmd *cobra.Command, args []string) error {
		sourceNames := args
		if maxBodyLines < 0 || maxHeaders < 0 || maxValueChars < 0 {
			return newCommandError("rendering limits must be zero or greater", "Use zero for an unlimited value, or provide a positive limit.")
		}
		var forwarder *proxy.Forwarder
		if forwardTo != "" {
			var err error
			forwarder, err = proxy.New(forwardTo)
			if err != nil {
				return wrapCommandError("invalid --forward-to value", "Use an HTTP(S) host with an optional path prefix.", err)
			}
		}

		cfg, err := resolveCommandConfig(cmd, true)
		if err != nil {
			return wrapCommandError("resolve listen configuration", configRecoveryHint(), err)
		}
		if cfg.CLIKey == "" {
			return loginRequiredError()
		}
		if cfg.Project == "" && cfg.OrganizationSlug == "" {
			return newCommandError(
				"no active project",
				"Run 'hookspot project use' or set HOOKSPOT_ORGANIZATION_SLUG and HOOKSPOT_PROJECT_SLUG.",
			)
		}

		client := api.New(activeEndpoint, cfg.CLIKey)
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

		p := printer.New(cmd.OutOrStdout(), printer.Options{
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
		listenContext, stopListening := context.WithCancel(cmd.Context())
		defer stopListening()
		if forwarder != nil {
			target = forwarder.String()
			session = newForwardSession(listenContext, forwarder, target, p)
			handler = session.Handle
			replayEnabled = isTerminalReader(cmd.InOrStdin())
		}

		wsURL := activeEndpoint.WebSocket()
		if wsURL == nil {
			return newCommandError("Hookspot websocket endpoint is not configured", "Install the correct release.")
		}
		if err := printListenInfoWithReplay(cmd.OutOrStdout(), sources, forwarder, replayEnabled); err != nil {
			return err
		}
		var replay *replayInputSession
		if replayEnabled {
			replay = startReplayInput(listenContext, cmd.InOrStdin(), session.Replay, stopListening)
		}
		topic := "project:" + project.UID

		wsClient := ws.New(wsURL.String(), cfg.CLIKey, topic, sourceUIDs)

		listenErr := superviseListen(listenContext, cmd.ErrOrStderr(), wsClient, handler, reconnectPolicy{
			Delay:              reconnectDelay,
			MaxInitialAttempts: maxInitialConnectAttempts,
		})
		stopListening()
		if replay != nil {
			if replayErr := replay.Stop(); replayErr != nil {
				return fmt.Errorf("replay input: %w", replayErr)
			}
		}
		return listenErr
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
					fmt.Sprintf("could not connect to Hookspot after %d attempts", initialAttempts),
					"Check your network connection and the Hookspot server URL, then try again.",
					err,
				)
			}
		}

		notice := fmt.Sprintf("connection lost: %s; reconnecting in %s...\n", safeDisplayText(err.Error()), policy.Delay)
		if err := writeCommandText(errOut, notice); err != nil {
			return fmt.Errorf("write reconnect notice: %w", err)
		}
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
	return safeDisplayText(project.Organization.Slug) + "/" + safeDisplayText(project.Slug)
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

func printListenInfoWithReplay(out io.Writer, sources []api.Source, forwarder *proxy.Forwarder, replay bool) error {
	var output strings.Builder
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
	fmt.Fprintf(&output, "Listening on %d source%s • %d connection%s\n", len(sources), sourceSuffix, connectionCount, connectionSuffix)

	for _, source := range sources {
		fmt.Fprintln(&output)
		fmt.Fprintln(&output, safeDisplayText(source.Name))
		if forwarder == nil {
			fmt.Fprintf(&output, "├ Requests to → %s\n", safeDisplayText(source.URL))
			fmt.Fprintln(&output, "└ Output      → terminal")
		} else {
			fmt.Fprintf(&output, "│  Requests to → %s\n", safeDisplayText(source.URL))
			for i, connection := range source.Connections {
				branch := "├─"
				if i == len(source.Connections)-1 {
					branch = "└─"
				}
				label := safeDisplayText(connectionLabel(connection))
				if label != "" {
					label = " (" + label + ")"
				}
				destination, err := forwarder.DestinationURL(connection.Destination.Path, "")
				if err != nil {
					return fmt.Errorf("resolve forwarding destination: %w", err)
				}
				fmt.Fprintf(&output, "%s Forwards to → %s%s\n", branch, destination.String(), label)
			}
		}
	}

	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "Requests ──────────────────────────────────────")
	fmt.Fprintln(&output)
	if replay {
		fmt.Fprintln(&output, "↵ replay last request")
	}
	fmt.Fprintln(&output, "Waiting for requests...")
	return writeCommandText(out, output.String())
}

func writeCommandText(out io.Writer, value string) error {
	written, err := io.WriteString(out, value)
	if err != nil {
		return err
	}
	if written != len(value) {
		return io.ErrShortWrite
	}
	return nil
}

func connectionLabel(connection api.Connection) string {
	if connection.Name != nil && *connection.Name != "" {
		return *connection.Name
	}
	return ""
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
	outcome, outputErr := s.forward(delivery, false)
	if outcome.Failure != nil {
		return ws.Response{
			Status:    http.StatusBadGateway,
			LatencyMS: latencyMilliseconds(outcome.Latency),
		}, outputErr
	}
	return outcome.Response, outputErr
}

func (s *forwardSession) Replay() error {
	delivery, ok := s.cache.Load()
	if !ok {
		return nil
	}
	_, err := s.forward(delivery, true)
	return err
}

func (s *forwardSession) forward(delivery ws.Delivery, replay bool) (printer.ForwardOutcome, error) {
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
		return outcome, s.printer.PrintForward(delivery, outcome)
	}
	body, err := readLocalResponseBody(response.Body)
	_ = response.Body.Close()
	latency := s.now().Sub(started)
	if errors.Is(err, errLocalResponseBodyTooLarge) {
		return printer.ForwardOutcome{}, err
	}
	if err != nil {
		outcome := printer.ForwardOutcome{
			Latency:   latency,
			Failure:   proxy.Failure(err),
			TargetURL: s.target,
			Replay:    replay,
		}
		return outcome, s.printer.PrintForward(delivery, outcome)
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
	return outcome, s.printer.PrintForward(delivery, outcome)
}

func readLocalResponseBody(body io.Reader) ([]byte, error) {
	contents, err := io.ReadAll(io.LimitReader(body, maxLocalResponseBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(contents) > maxLocalResponseBodyBytes {
		return nil, errLocalResponseBodyTooLarge
	}
	return contents, nil
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

type replayInputSession struct {
	cancel       context.CancelFunc
	closeInput   io.Closer
	producerDone <-chan struct{}
	consumerDone <-chan error
	once         sync.Once
	err          error
}

func startReplayInput(parent context.Context, input io.Reader, replay func() error, onError func()) *replayInputSession {
	ctx, cancel := context.WithCancel(parent)
	events := make(chan struct{}, 1)
	producerResult := make(chan error, 1)
	producerDone := make(chan struct{})
	closer, owned := ownedReplayInput(input)

	go func() {
		defer close(producerDone)
		defer close(events)
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			if ctx.Err() != nil {
				producerResult <- nil
				return
			}
			if scanner.Text() != "" {
				continue
			}
			select {
			case events <- struct{}{}:
			case <-ctx.Done():
				producerResult <- nil
				return
			}
		}
		err := scanner.Err()
		if ctx.Err() != nil {
			err = nil
		}
		producerResult <- err
	}()

	consumerDone := make(chan error, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				consumerDone <- nil
				return
			case _, ok := <-events:
				if !ok {
					err := <-producerResult
					if err != nil {
						onError()
					}
					consumerDone <- err
					return
				}
				if ctx.Err() != nil {
					consumerDone <- nil
					return
				}
				if err := replay(); err != nil {
					onError()
					consumerDone <- err
					return
				}
			}
		}
	}()

	session := &replayInputSession{
		cancel:       cancel,
		producerDone: producerDone,
		consumerDone: consumerDone,
	}
	if owned {
		session.closeInput = closer
	}
	return session
}

func ownedReplayInput(input io.Reader) (io.Closer, bool) {
	closer, ok := input.(io.Closer)
	if !ok {
		return nil, false
	}
	if file, ok := input.(*os.File); ok && file == os.Stdin {
		// The console reader may remain blocked until process exit. It owns no
		// callback work, is started once for the command, and global stdin is
		// never closed.
		return nil, false
	}
	return closer, true
}

func (s *replayInputSession) Stop() error {
	s.once.Do(func() {
		s.cancel()
		var closeErr error
		if s.closeInput != nil {
			closeErr = s.closeInput.Close()
		}
		s.err = <-s.consumerDone
		if s.closeInput != nil {
			<-s.producerDone
		}
		if s.err == nil {
			s.err = closeErr
		}
	})
	return s.err
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
