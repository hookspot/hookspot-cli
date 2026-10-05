package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"hookspot/internal/api"
	"hookspot/internal/cards"
	"hookspot/internal/endpoint"
	"hookspot/internal/proxy"
	"hookspot/internal/session"
	"hookspot/internal/tui"
	"hookspot/internal/ws"
)

const (
	firstReconnectCeiling     = time.Second
	maxReconnectCeiling       = 30 * time.Second
	maxInitialConnectAttempts = 10
)

var (
	forwardTo            string
	streamOutput         bool
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
		sources, sourceUIDs, warnings, err := resolveSources(activeEndpoint, project, availableSources, sourceNames)
		requestsURL := dashboardRequestsURL(activeEndpoint, project)
		listenCards := cards.Listen{
			Sources:              sourceNamesByUID(sources),
			ShowSensitiveHeaders: showSensitiveHeaders,
			Container:            runningInContainer(),
			Limits: cards.Limits{
				MaxBodyLines:  maxBodyLines,
				MaxHeaders:    maxHeaders,
				MaxValueChars: maxValueChars,
			},
		}
		writer := cards.NewWriter(cmd.OutOrStdout(), cmd.ErrOrStderr(), listenCards, requestsURL)
		// Warnings print even when no source is left; nothing else writes
		// yet, so they skip the session.
		for _, warning := range warnings {
			if err := writer.Emit(warning); err != nil {
				return fmt.Errorf("write source warnings: %w", err)
			}
		}
		if err != nil {
			return err
		}

		wsURL := activeEndpoint.WebSocket()
		if wsURL == nil {
			return newCommandError("Hookspot websocket endpoint is not configured", "Install the correct release.")
		}
		routes, err := bannerRoutes(sources, forwarder)
		if err != nil {
			return err
		}
		listenContext, stopListening := context.WithCancel(cmd.Context())
		defer stopListening()
		var local session.Forwarder
		var target string
		if forwarder != nil {
			local = forwarder
			target = forwarder.String()
		}
		wsClient := ws.New(wsURL.String(), cfg.CLIKey, "project:"+project.UID, sourceUIDs, target)
		// Piped output may feed a script: it gets plain mode and no update
		// alert.
		terminal := cards.Terminal(cmd.OutOrStdout())
		setup := listenSetup{
			ctx:         listenContext,
			stop:        stopListening,
			sources:     sources,
			local:       local,
			listenCards: listenCards,
			writer:      writer,
			project:     projectDisplayName(*project),
			routes:      routes,
			requestsURL: requestsURL,
			listen: func(sess *session.Session) error {
				if terminal {
					defer announceUpdate(listenContext, sess)()
				}
				if err := sess.Emit(session.Connecting{}); err != nil {
					return err
				}
				notices := newConnectionNotices(sess.Emit)
				wsClient.OnJoined = notices.joined
				return superviseListen(listenContext, notices, wsClient, sess.Handle, reconnectPolicy{
					Delay:              reconnectDelay,
					MaxInitialAttempts: maxInitialConnectAttempts,
				})
			},
		}

		// A dumb terminal, such as Emacs' M-x shell, has no cursor control
		// for the full-screen or stream view.
		piped := !terminal
		if piped || os.Getenv("TERM") == "dumb" {
			return runPlain(setup, cmd.InOrStdin(), piped)
		}
		var input io.Reader
		if cards.Terminal(cmd.InOrStdin()) {
			input = cmd.InOrStdin()
		}
		program := tui.NewProgram(input, cmd.OutOrStdout(), stopListening)
		if input != nil && !streamOutput {
			return runFullscreen(setup, program, warnings)
		}
		return runStream(setup, program, input != nil)
	},
}

// listenSetup is what listen's full-screen, stream and plain views share.
type listenSetup struct {
	ctx     context.Context
	stop    context.CancelFunc
	sources []api.Source
	// local is nil in inspect mode.
	local       session.Forwarder
	listenCards cards.Listen
	writer      *cards.Writer
	project     string
	routes      []cards.BannerRoute
	requestsURL string
	// listen runs the connection, reporting to sess, until listening stops.
	listen func(sess *session.Session) error
}

func runFullscreen(setup listenSetup, program *tui.Program, warnings []session.Event) error {
	sess := session.New(setup.ctx, setup.sources, setup.local, program.FullscreenSink())
	screen := tui.Fullscreen{
		Requests:             sess,
		Listen:               setup.listenCards,
		Project:              setup.project,
		Routes:               setup.routes,
		RequestsURL:          setup.requestsURL,
		ShowSensitiveHeaders: setup.listenCards.ShowSensitiveHeaders,
	}
	if setup.local != nil {
		screen.Target = setup.local.String()
	}
	return runInTerminal(program, screen, func() error {
		// The alt screen hides the warnings printed before it.
		for _, warning := range warnings {
			if err := sess.Emit(warning); err != nil {
				return err
			}
		}
		return setup.listen(sess)
	})
}

// runStream prompts for commands when prompt is set.
func runStream(setup listenSetup, program *tui.Program, prompt bool) error {
	// The status line carries the hints.
	if err := setup.writer.Banner(setup.project, setup.routes, nil); err != nil {
		return err
	}
	sess := session.New(setup.ctx, setup.sources, setup.local, program.StreamSink(setup.listenCards, setup.requestsURL))
	stream := tui.Stream{
		Requests:             sess,
		Println:              program.Println,
		Project:              setup.project,
		Forwarding:           setup.local != nil,
		Prompt:               prompt,
		ShowSensitiveHeaders: setup.listenCards.ShowSensitiveHeaders,
	}
	return runInTerminal(program, stream, func() error { return setup.listen(sess) })
}

// runPlain takes line commands from input when it is a terminal and listen
// runs in its foreground. With stdout piped, only forwarding takes them: a
// pager reading the pipe shares the terminal, and replays are what commands
// in `listen | tee log` are for.
func runPlain(setup listenSetup, input io.Reader, piped bool) error {
	sess := session.New(setup.ctx, setup.sources, setup.local, setup.writer)
	commandsEnabled := cards.Terminal(input) && foreground(input) && (setup.local != nil || !piped)
	setup.writer.Commands = commandsEnabled
	hints := []string{tui.QuitHint}
	if commandsEnabled {
		hints = append(tui.CommandHints(setup.local != nil), hints...)
	}
	if err := setup.writer.Banner(setup.project, setup.routes, hints); err != nil {
		return err
	}
	var commands *lineCommandReader
	if commandsEnabled {
		options := lineCommandOptions{
			forwarding:           setup.local != nil,
			showSensitiveHeaders: setup.listenCards.ShowSensitiveHeaders,
		}
		commands = startLineCommands(setup.ctx, input, func(line string) error {
			return runLineCommand(sess, setup.writer, options, line)
		}, setup.stop)
	}
	listenErr := setup.listen(sess)
	setup.stop()
	if commands != nil {
		if err := commands.Stop(); err != nil {
			return fmt.Errorf("line commands: %w", err)
		}
	}
	return listenErr
}

// runInTerminal runs listen under program: listen ending quits the program,
// and the program ending first stops listen. When both fail, the program's
// error wins.
func runInTerminal(program *tui.Program, model tea.Model, listen func() error) error {
	listened := make(chan error, 1)
	go func() {
		err := listen()
		program.Quit()
		listened <- err
	}()
	_, runErr := program.Run(model)
	listenErr := <-listened
	if runErr != nil {
		return runErr
	}
	return listenErr
}

// announceUpdate tells sess about a newer release on GitHub while listen
// connects. The returned stop cancels the check and waits for it, so no
// alert follows listening. An alert that can't be written never stops it.
func announceUpdate(ctx context.Context, sess *session.Session) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if latest := newerRelease(ctx, version); latest != "" {
			_ = sess.Emit(session.UpdateAvailable{Latest: latest, Upgrade: upgradeCommand()})
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

type websocketListener interface {
	Listen(context.Context, ws.Handler) error
}

type reconnectPolicy struct {
	// Delay picks the wait before reconnecting from the count of sessions
	// failed since the last join.
	Delay              func(failures int) time.Duration
	MaxInitialAttempts int
}

// superviseListen keeps transient WebSocket failures inside the long-running
// command. Authentication, not-found, protocol, and handler failures are
// fatal; an initial connection is bounded, while a session that connected once
// retries until cancellation.
func superviseListen(ctx context.Context, notices *connectionNotices, listener websocketListener, handler ws.Handler, policy reconnectPolicy) error {
	failures := 0
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
				failures = 0
			}
			if !sessionErr.Retryable() {
				return fmt.Errorf("listen: %w", err)
			}
		}

		failures++
		if !connectedOnce && policy.MaxInitialAttempts > 0 && failures >= policy.MaxInitialAttempts {
			return wrapCommandError(
				fmt.Sprintf("could not connect to Hookspot after %d attempts", failures),
				"Check your network connection and the Hookspot server URL, then try again.",
				err,
			)
		}

		delay := policy.Delay(failures)
		if err := notices.lost(err, delay); err != nil {
			return fmt.Errorf("write reconnect notice: %w", err)
		}
		if !waitForReconnect(ctx, delay) {
			return nil
		}
	}
}

// connectionNotices turns joins and failed sessions into connection states.
// Deliveries start only after the channel join, so Ready waits for it.
type connectionNotices struct {
	emit         func(session.Event) error
	now          func() time.Time
	ready        bool
	offlineSince time.Time
}

func newConnectionNotices(emit func(session.Event) error) *connectionNotices {
	return &connectionNotices{emit: emit, now: time.Now}
}

func (n *connectionNotices) joined() error {
	if !n.ready {
		n.ready = true
		return n.emit(session.Ready{})
	}
	offline := n.now().Sub(n.offlineSince).Round(time.Second)
	n.offlineSince = time.Time{}
	return n.emit(session.Reconnected{Offline: offline})
}

// lost reports a failed session. An outage is timed from its first failed
// session, not from the latest reconnect attempt.
func (n *connectionNotices) lost(err error, retryIn time.Duration) error {
	if n.ready && n.offlineSince.IsZero() {
		n.offlineSince = n.now()
	}
	return n.emit(session.ConnectionLost{Err: err, RetryIn: retryIn})
}

func dashboardRequestsURL(base endpoint.Base, project *api.Project) string {
	if u := dashboardURL(base, project, "requests"); u != "" {
		return u
	}
	return "the dashboard"
}

func addRouteHint(base endpoint.Base, project *api.Project) string {
	if u := dashboardURL(base, project, "routes/new"); u != "" {
		return "Add a route in the dashboard: " + u
	}
	return "Add a route in the dashboard."
}

// dashboardURL returns "" when a slug isn't a safe path segment.
func dashboardURL(base endpoint.Base, project *api.Project, page string) string {
	organization, organizationErr := endpoint.Segment(project.Organization.Slug)
	slug, slugErr := endpoint.Segment(project.Slug)
	if organizationErr != nil || slugErr != nil {
		return ""
	}
	if u := base.API(organization + "/" + slug + "/" + page); u != nil {
		return u.String()
	}
	return ""
}

// reconnectDelay draws a full-jitter wait, so the CLIs a deploy disconnects
// together don't all reconnect at once.
func reconnectDelay(failures int) time.Duration {
	return rand.N(reconnectCeiling(failures))
}

func reconnectCeiling(failures int) time.Duration {
	ceiling := firstReconnectCeiling
	for i := 1; i < failures && ceiling < maxReconnectCeiling; i++ {
		ceiling *= 2
	}
	return min(ceiling, maxReconnectCeiling)
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
	return cards.Line(project.Organization.Slug) + "/" + cards.Line(project.Slug)
}

// resolveSources returns the sources to listen to, the UIDs to join with
// (none means every source), and warnings about named sources skipped for
// having no route and about disabled sources, whose requests are rejected.
// The warnings come with the error when no source is left.
func resolveSources(base endpoint.Base, project *api.Project, availableSources []api.Source, sourceNames []string) ([]api.Source, []string, []session.Event, error) {
	var selectedSources []api.Source
	var sourceUIDs []string
	var warnings []session.Event
	if len(sourceNames) == 0 {
		selectedSources = sourcesWithRoutes(availableSources)
	} else {
		availableByName := make(map[string]api.Source, len(availableSources))
		for _, source := range availableSources {
			availableByName[source.Name] = source
		}
		for _, sourceName := range sourceNames {
			source, ok := availableByName[sourceName]
			if !ok {
				return nil, nil, nil, unknownSourceError(project, availableSources, sourceName)
			}
			if len(source.Routes) == 0 {
				warnings = append(warnings, session.SkippedSource{Name: source.Name})
				continue
			}
			selectedSources = append(selectedSources, source)
			sourceUIDs = append(sourceUIDs, source.UID)
		}
	}
	for _, source := range selectedSources {
		if !source.Active {
			warnings = append(warnings, session.DisabledSource{Name: source.Name})
		}
	}

	if len(selectedSources) == 0 {
		message := "none of the named sources has a route"
		if len(sourceNames) == 0 {
			message = "no sources with routes in " + projectDisplayName(*project)
		}
		return nil, nil, warnings, newCommandError(message, addRouteHint(base, project))
	}
	return selectedSources, sourceUIDs, warnings, nil
}

// unknownSourceError suggests a name only when it is unambiguous: exactly one
// source matches ignoring case.
func unknownSourceError(project *api.Project, availableSources []api.Source, sourceName string) error {
	var matches []string
	for _, source := range availableSources {
		if strings.EqualFold(source.Name, sourceName) {
			matches = append(matches, source.Name)
		}
	}
	message := fmt.Sprintf("source %q is not present in project %s", sourceName, formatProjectLabel(project))
	if len(matches) == 1 {
		message += fmt.Sprintf("; did you mean %q?", matches[0])
	}
	return errors.New(message)
}

func sourcesWithRoutes(sources []api.Source) []api.Source {
	selected := make([]api.Source, 0, len(sources))
	for _, source := range sources {
		if len(source.Routes) > 0 {
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

// bannerRoutes lists every route listen delivers through; inspect mode has no
// destinations.
func bannerRoutes(sources []api.Source, forwarder *proxy.Forwarder) ([]cards.BannerRoute, error) {
	var routes []cards.BannerRoute
	for _, source := range sources {
		for _, route := range source.Routes {
			banner := cards.BannerRoute{
				SourceUID: source.UID,
				Source:    source.Name,
				PublicURL: source.URL,
				RouteUID:  route.UID,
				Path:      route.Destination.Path,
				Label:     session.RouteLabel(route),
			}
			if forwarder != nil {
				destination, err := forwarder.DestinationURL(route.Destination.Path, "")
				if err != nil {
					return nil, fmt.Errorf("resolve forwarding destination: %w", err)
				}
				banner.Destination = destination.String()
			}
			routes = append(routes, banner)
		}
	}
	return routes, nil
}

func runningInContainer() bool {
	_, docker := os.Stat("/.dockerenv")
	_, podman := os.Stat("/run/.containerenv")
	return docker == nil || podman == nil
}

// runLineCommand runs one line typed into the stream: ↵ replays the last
// request, r N replays #N, c N prints #N as a cURL command, e N exports it as
// a fixture, t [source] sends a test event, ? prints help, and anything else
// gets the command list.
func runLineCommand(sess *session.Session, writer *cards.Writer, options lineCommandOptions, line string) error {
	command, ok := tui.ParseCommand(line)
	if !ok {
		return writer.Reply(tui.CommandUsage(options.forwarding))
	}
	switch command.Key {
	case tui.ReplayLastKey:
		return replyToReplay(writer, sess.ReplayLast())
	case tui.ReplayKey:
		return replyToReplay(writer, sess.Replay(command.N))
	case tui.CurlKey:
		curl, err := sess.Curl(command.N, !options.showSensitiveHeaders)
		if err != nil {
			return writer.Reply(err.Error())
		}
		return writer.Print(cards.CurlNotes(command.N, curl, false) + "\n" + curl.Shown)
	case tui.ExportKey:
		fixture, err := sess.ExportFixture(command.N, !options.showSensitiveHeaders)
		if err != nil {
			return writer.Reply(err.Error())
		}
		return writer.Reply(cards.Exported(command.N, fixture))
	case tui.TestKey:
		// A test event that fails to send leaves listening as it was.
		source, err := sess.SendTest(command.Source)
		if err != nil {
			return writer.Reply(err.Error())
		}
		return writer.Reply("test event sent to " + source)
	}
	return writer.Print(tui.CommandHelp(options.forwarding))
}

// lineCommandOptions are the listen flags line commands follow.
type lineCommandOptions struct {
	forwarding           bool
	showSensitiveHeaders bool
}

// replyToReplay answers a replay this run can't make: a number it never
// reached or already dropped is a typo, and inspect mode has nothing to replay
// to. Neither is a reason to stop listening.
func replyToReplay(writer *cards.Writer, err error) error {
	if errors.Is(err, session.ErrUnknown) || errors.Is(err, session.ErrEvicted) || errors.Is(err, session.ErrNoTarget) {
		return writer.Reply(err.Error())
	}
	return err
}

// lineCommandReader feeds the lines typed into the stream to its commands.
type lineCommandReader struct {
	cancel       context.CancelFunc
	closeInput   io.Closer
	producerDone <-chan struct{}
	consumerDone <-chan error
	once         sync.Once
	err          error
}

// startLineCommands runs each line of input until parent ends. A failed
// command calls onError and ends the reader with its error.
func startLineCommands(parent context.Context, input io.Reader, run func(line string) error, onError func()) *lineCommandReader {
	ctx, cancel := context.WithCancel(parent)
	lines := make(chan string, 1)
	producerResult := make(chan error, 1)
	producerDone := make(chan struct{})
	closer, owned := ownedInput(input)

	go func() {
		defer close(producerDone)
		defer close(lines)
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			if ctx.Err() != nil {
				producerResult <- nil
				return
			}
			select {
			case lines <- scanner.Text():
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
			case line, ok := <-lines:
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
				if err := run(line); err != nil {
					onError()
					consumerDone <- err
					return
				}
			}
		}
	}()

	reader := &lineCommandReader{
		cancel:       cancel,
		producerDone: producerDone,
		consumerDone: consumerDone,
	}
	if owned {
		reader.closeInput = closer
	}
	return reader
}

func ownedInput(input io.Reader) (io.Closer, bool) {
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

func (r *lineCommandReader) Stop() error {
	r.once.Do(func() {
		r.cancel()
		var closeErr error
		if r.closeInput != nil {
			closeErr = r.closeInput.Close()
		}
		r.err = <-r.consumerDone
		if r.closeInput != nil {
			<-r.producerDone
		}
		if r.err == nil {
			r.err = closeErr
		}
	})
	return r.err
}

func init() {
	listenCmd.Flags().StringVar(&forwardTo, "forward-to", "", "base URL to forward events to, e.g. localhost:3000 (deliveries keep their own path; omit to only print)")
	listenCmd.Flags().BoolVar(&streamOutput, "stream", false, "print requests as a scrolling stream instead of the full-screen view")
	listenCmd.Flags().BoolVar(&showSensitiveHeaders, "show-sensitive-headers", false, "show sensitive header values on screen, in cURL commands and in fixtures")
	listenCmd.Flags().IntVar(&maxBodyLines, "max-body-lines", 12, "maximum body lines to print (0 for unlimited)")
	listenCmd.Flags().IntVar(&maxHeaders, "max-headers", 20, "maximum headers to print (0 for unlimited)")
	listenCmd.Flags().IntVar(&maxValueChars, "max-value-chars", 160, "maximum characters per value or body line (0 for unlimited)")
	rootCmd.AddCommand(listenCmd)
}
