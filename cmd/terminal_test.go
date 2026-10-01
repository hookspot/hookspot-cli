package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
	"github.com/gorilla/websocket"

	"hookspot/internal/ws"
)

// TestTerminalFullscreenJourney runs listen as it starts in a terminal: full
// screen, a delivery's row in the request list, and q leaving the terminal as
// it found it.
func TestTerminalFullscreenJourney(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer local.Close()
	hookspot := startFakeHookspot(t, listenStreamSources)
	run := startTerminal(t, terminalOptions{width: 100, height: 30}, developmentMetadata(hookspot.url), hookspot.listen("--forward-to", local.URL)...)
	hookspot.deliver(t, terminalDelivery)

	row := regexp.MustCompile(`(?m)^│ › 1  \d\d:\d\d:\d\d  stripe  POST +/webhooks/stripe +200 `)
	run.waitFor("the request's row", row.MatchString)
	if !run.altScreen() {
		t.Error("the request list is not on the alt screen")
	}
	if !terminalColor.MatchString(run.written()) {
		t.Error("no color in a terminal")
	}
	run.send("q")
	if code := run.wait(); code != 0 {
		t.Fatalf("exit code after q = %d, want 0; screen:\n%s", code, run.text())
	}
	if run.altScreen() {
		t.Error("still on the alt screen after q")
	}
	run.requireRestored()
}

// TestTerminalModes covers how listen picks its view in the other terminal
// setups.
func TestTerminalModes(t *testing.T) {
	status := regexp.MustCompile(`(?m)^● live · Acme \| Payments · \d+ requests?`)
	prompt := regexp.MustCompile(`(?m)^› .*ctrl-c quit$`)

	t.Run("stream", func(t *testing.T) {
		hookspot := startFakeHookspot(t, listenStreamSources)
		run := startTerminal(t, terminalOptions{width: 100, height: 30}, developmentMetadata(hookspot.url), hookspot.listen("--stream")...)
		run.waitFor("the status line and prompt", func(screen string) bool {
			return status.MatchString(screen) && prompt.MatchString(screen)
		})
		if run.altScreen() {
			t.Error("the stream is on the alt screen")
		}
	})

	t.Run("stdin not a terminal", func(t *testing.T) {
		hookspot := startFakeHookspot(t, listenStreamSources)
		run := startTerminal(t, terminalOptions{width: 100, height: 30, stdin: strings.NewReader("")}, developmentMetadata(hookspot.url), hookspot.listen()...)
		quit := regexp.MustCompile(status.String() + ` +ctrl-c quit$`)
		// The prompt would come in the same frame as the status line.
		if screen := run.waitFor("the status line", quit.MatchString); prompt.MatchString(screen) || run.altScreen() {
			t.Fatalf("screen without a terminal on stdin:\n%s", screen)
		}
	})

	t.Run("stdout piped", func(t *testing.T) {
		hookspot := startFakeHookspot(t, listenStreamSources)
		var stdout bytes.Buffer
		run := startTerminal(t, terminalOptions{width: 100, height: 30, stdout: &stdout}, developmentMetadata(hookspot.url), hookspot.listen()...)
		hookspot.deliver(t, terminalDelivery)
		hookspot.end(t)
		if code := run.wait(); code != 1 {
			t.Fatalf("exit code = %d, want 1 for the invalid delivery; screen:\n%s", code, run.text())
		}
		// stdin is a terminal, so the banner names the line commands.
		for _, want := range []string{"t test event · ctrl-c quit ─╯\n", "\nReady. Waiting for requests (Ctrl-C to quit)\n", "\n╭─ #1 stripe · POST /webhooks/stripe "} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("piped stdout has no %q:\n%s", want, stdout.String())
			}
		}
		if strings.ContainsRune(stdout.String(), '\x1b') {
			t.Errorf("piped stdout has escape sequences: %q", stdout.String())
		}
	})

	t.Run("NO_COLOR", func(t *testing.T) {
		hookspot := startFakeHookspot(t, listenStreamSources)
		run := startTerminal(t, terminalOptions{width: 100, height: 30, environment: map[string]string{"NO_COLOR": "1"}}, developmentMetadata(hookspot.url), hookspot.listen("--stream")...)
		hookspot.deliver(t, terminalDelivery)
		run.waitForText("● live · Acme | Payments · 1 request")
		if color := terminalColor.FindString(run.written()); color != "" {
			t.Errorf("NO_COLOR output has color %q", color)
		}
	})
}

// terminalTimeout bounds every wait on a terminal run. It's generous, since a
// loaded machine runs the command slowly.
const terminalTimeout = 30 * time.Second

// terminalRun is the command running on a pseudo-terminal, its screen rebuilt
// by a virtual terminal.
type terminalRun struct {
	t       *testing.T
	pty     xpty.Pty
	command *exec.Cmd
	initial terminalMode

	mu     sync.Mutex
	screen *vt.Emulator
	output bytes.Buffer

	changed chan struct{}
	drained chan struct{}
	exited  chan struct{}
	exitErr error
}

type terminalOptions struct {
	width, height int
	environment   map[string]string
	// stdin and stdout take the terminal's place when set.
	stdin  io.Reader
	stdout io.Writer
}

// startTerminal runs the command on a pseudo-terminal of the given size, with
// TERM=xterm-256color, TZ=UTC and a temporary HOME. stderr is always the
// terminal.
func startTerminal(t *testing.T, options terminalOptions, metadata map[string]string, args ...string) *terminalRun {
	t.Helper()
	if runtime.GOOS == "windows" && (options.stdin != nil || options.stdout != nil) {
		t.Skip("ConPTY makes its console the command's stdin, stdout and stderr, so neither can be replaced")
	}
	pty, err := xpty.NewPty(options.width, options.height)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pty.Close() })
	environment := map[string]string{"TERM": "xterm-256color", "TZ": "UTC"}
	maps.Copy(environment, options.environment)
	command := commandProcess(t, metadata, environment, args...)
	command.Stdin, command.Stdout = options.stdin, options.stdout
	// The first standard file on the terminal makes it the controlling one.
	controlling := 0
	if options.stdin != nil {
		controlling = 1
		if options.stdout != nil {
			controlling = 2
		}
	}
	controlTerminal(command, controlling)

	r := &terminalRun{
		t:       t,
		pty:     pty,
		command: command,
		initial: readTerminalMode(t, pty),
		screen:  vt.NewEmulator(options.width, options.height),
		changed: make(chan struct{}, 1),
		drained: make(chan struct{}),
		exited:  make(chan struct{}),
	}
	if err := pty.Start(command); err != nil {
		t.Fatal(err)
	}
	// Once the command has the terminal, only its exit ends the output.
	if unixPty, ok := pty.(*xpty.UnixPty); ok {
		_ = unixPty.Slave().Close()
	}
	go func() {
		r.exitErr = xpty.WaitProcess(context.Background(), command)
		// ConPTY keeps its console, and so the output, open after the
		// command exits.
		if runtime.GOOS == "windows" {
			_ = pty.Close()
		}
		close(r.exited)
	}()
	go r.read()
	// The virtual terminal answers the command's queries, as a real one does.
	go func() { _, _ = io.Copy(pty, r.screen) }()
	t.Cleanup(func() {
		select {
		case <-r.exited:
		default:
			_ = command.Process.Kill()
			<-r.exited
		}
		select {
		case <-r.drained:
		case <-time.After(terminalTimeout):
			t.Error("terminal output did not end after the command exited")
		}
		// Ends the copy of the virtual terminal's answers.
		_ = r.screen.InputPipe().(io.Closer).Close()
	})
	return r
}

func (r *terminalRun) read() {
	defer close(r.drained)
	buffer := make([]byte, 32*1024)
	for {
		n, err := r.pty.Read(buffer)
		if n > 0 {
			r.mu.Lock()
			r.output.Write(buffer[:n])
			_, _ = r.screen.Write(buffer[:n])
			r.mu.Unlock()
			select {
			case r.changed <- struct{}{}:
			default:
			}
		}
		if err != nil {
			return
		}
	}
}

// send types keys, as raw bytes: "\r" is enter, "\x1b[B" down.
func (r *terminalRun) send(keys string) {
	r.t.Helper()
	if _, err := io.WriteString(r.screen.InputPipe(), keys); err != nil {
		r.t.Fatal(err)
	}
}

// resize resizes the terminal, which sends the command SIGWINCH.
func (r *terminalRun) resize(width, height int) {
	r.t.Helper()
	r.mu.Lock()
	r.screen.Resize(width, height)
	r.mu.Unlock()
	if err := r.pty.Resize(width, height); err != nil {
		r.t.Fatal(err)
	}
}

// text is the screen, one line per row without trailing spaces.
func (r *terminalRun) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.screen.String()
}

// written is every byte the command wrote to the terminal.
func (r *terminalRun) written() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.output.String()
}

func (r *terminalRun) altScreen() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.screen.IsAltScreen()
}

// waitFor waits until condition holds for the screen's text, and returns
// that text.
func (r *terminalRun) waitFor(what string, condition func(screen string) bool) string {
	r.t.Helper()
	deadline := time.After(terminalTimeout)
	for {
		ended := closed(r.drained)
		screen := r.text()
		if condition(screen) {
			return screen
		}
		if ended {
			r.t.Fatalf("output ended before %s; screen:\n%s", what, screen)
		}
		select {
		case <-r.changed:
		case <-r.drained:
		case <-deadline:
			r.t.Fatalf("no %s after %s; screen:\n%s", what, terminalTimeout, screen)
		}
	}
}

func (r *terminalRun) waitForText(text string) string {
	r.t.Helper()
	return r.waitFor(text, func(screen string) bool { return strings.Contains(screen, text) })
}

// wait waits for the command to exit and its output to end, and returns its
// exit code.
func (r *terminalRun) wait() int {
	r.t.Helper()
	select {
	case <-r.exited:
	case <-time.After(terminalTimeout):
		r.t.Fatalf("command did not exit; screen:\n%s", r.text())
	}
	select {
	case <-r.drained:
	case <-time.After(terminalTimeout):
		r.t.Fatal("terminal output did not end after the command exited")
	}
	var exitErr *exec.ExitError
	if r.exitErr != nil && !errors.As(r.exitErr, &exitErr) {
		r.t.Fatal(r.exitErr)
	}
	return r.command.ProcessState.ExitCode()
}

// requireRestored fails unless the terminal mode is back to what it was
// before the command started.
func (r *terminalRun) requireRestored() {
	r.t.Helper()
	if mode := readTerminalMode(r.t, r.pty); mode != r.initial {
		r.t.Fatalf("terminal mode = %+v, want it restored to %+v", mode, r.initial)
	}
}

func closed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// fakeHookspot serves the Acme | Payments project and its sources, and a
// websocket that accepts the join, then sends each payload given to deliver.
// It serves one run: with two connected, either may take a delivery.
type fakeHookspot struct {
	url        string
	config     string
	deliveries chan any
}

func startFakeHookspot(t *testing.T, sources string) *fakeHookspot {
	t.Helper()
	hookspot := &fakeHookspot{config: filepath.Join(t.TempDir(), "config.toml"), deliveries: make(chan any)}
	if err := writeCommandFixture(hookspot.config, []byte("schema_version = 1\ncli_key = 'key'\nproject = 'proj_payments'\n")); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cli/projects/proj_payments":
			_, _ = w.Write([]byte(`{"uid":"proj_payments","name":"Payments","slug":"payments","organization":{"name":"Acme","slug":"acme"}}`))
		case "/cli/projects/proj_payments/sources":
			_, _ = w.Write([]byte(sources))
		case "/cli/websocket":
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			var join []json.RawMessage
			if err := conn.ReadJSON(&join); err != nil || len(join) != 5 {
				return
			}
			_ = conn.WriteJSON([]any{join[0], join[1], join[2], "phx_reply", map[string]any{"status": "ok", "response": map[string]any{}}})
			// Responses and heartbeats are dropped until the command hangs up.
			gone := make(chan struct{})
			go func() {
				defer close(gone)
				for {
					if _, _, err := conn.ReadMessage(); err != nil {
						return
					}
				}
			}()
			for {
				select {
				case payload := <-hookspot.deliveries:
					_ = conn.WriteJSON([]any{nil, nil, join[2], "delivery", payload})
				case <-gone:
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	hookspot.url = server.URL
	return hookspot
}

// listen is the command line of listen against this server.
func (h *fakeHookspot) listen(args ...string) []string {
	return append([]string{"--config", h.config, "listen"}, args...)
}

// deliver sends payload once listen has joined. listen handles deliveries one
// at a time, so each one's output comes before the next one's.
func (h *fakeHookspot) deliver(t *testing.T, payload any) {
	t.Helper()
	select {
	case h.deliveries <- payload:
	case <-time.After(terminalTimeout):
		t.Fatal("listen did not join")
	}
}

// end sends a delivery without correlation fields, which ends listen with an
// error.
func (h *fakeHookspot) end(t *testing.T) {
	t.Helper()
	h.deliver(t, map[string]string{})
}

// terminalDelivery is a POST of payment_intent.succeeded to stripe.
var terminalDelivery = ws.Delivery{
	AttemptUID: "att_1", RequestUID: "req_1", SourceUID: "src_stripe", Method: "POST", Path: "/webhooks/stripe",
	Headers: http.Header{"Content-Type": []string{"application/json"}},
	Body:    []byte(`{"type":"payment_intent.succeeded"}`),
}

// terminalColor is a styling sequence that sets a color.
var terminalColor = regexp.MustCompile(`\x1b\[([0-9]+;)*(3[0-9]|4[0-9]|9[0-7]|10[0-7])(;[0-9]+)*m`)
