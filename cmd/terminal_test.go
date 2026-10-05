package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
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
	hookspot := startFakeHookspot(t, fakeHookspotSources)
	run := startTerminal(t, terminalOptions{width: 100, height: 30}, developmentMetadata(hookspot.url), hookspot.listen("--forward-to", local.URL)...)
	hookspot.deliver(t, terminalDelivery)

	row := regexp.MustCompile(`(?m)^│ › 1  \d\d:\d\d:\d\d  stripe  POST +/webhooks/stripe +200 `)
	run.waitFor("the request's row", row.MatchString)
	run.requireAltScreen()
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

// TestTerminalFullscreenError runs full screen until listen fails. The alt
// screen shows the source warning its stderr copy hid and the --forward-to
// URL as typed; the error prints once the alt screen is gone.
func TestTerminalFullscreenError(t *testing.T) {
	disabled := strings.Replace(fakeHookspotSources, `src_github","active":true`, `src_github","active":false`, 1)
	hookspot := startFakeHookspot(t, disabled)
	run := startTerminal(t, terminalOptions{width: 100, height: 30}, developmentMetadata(hookspot.url), hookspot.listen("--forward-to", "3000/hooks/")...)
	header := regexp.MustCompile(`(?m)^● live · .*→ http://localhost:3000/hooks/ · \d\d:\d\d:\d\d$`)
	run.waitFor("the target and the warning", func(screen string) bool {
		return header.MatchString(screen) && strings.Contains(screen, "\n⚠ github is disabled: requests to it are rejected.")
	})
	hookspot.end(t)
	if code := run.wait(); code != 1 {
		t.Fatalf("exit code = %d, want 1 for the invalid delivery; screen:\n%s", code, run.text())
	}
	if run.altScreen() {
		t.Error("still on the alt screen after the error")
	}
	errorBox := regexp.MustCompile(`(?m)^╭─ ✗ Error ─+╮\n│ .*delivery is missing correlation fields`)
	if screen := run.text(); !errorBox.MatchString(screen) {
		t.Errorf("no error box after the alt screen:\n%s", screen)
	}
	run.requireRestored()
}

// TestTerminalModes covers how listen picks its view in the other terminal
// setups.
func TestTerminalModes(t *testing.T) {
	status := regexp.MustCompile(`(?m)^● live · Acme \| Payments · \d+ requests?`)
	prompt := regexp.MustCompile(`(?m)^› .*ctrl-c quit$`)
	banner := "╭─ Listening in Acme | Payments "
	card := "╭─ #1 stripe · POST /webhooks/stripe "
	row := regexp.MustCompile(`(?m)^#1 +POST +stripe +/webhooks/stripe `)

	t.Run("stream", func(t *testing.T) {
		hookspot := startFakeHookspot(t, fakeHookspotSources)
		var earlier strings.Builder
		for i := 1; i <= 25; i++ {
			fmt.Fprintf(&earlier, "earlier output %d\r\n", i)
		}
		// The local join is instant, so the test hint is ready before the
		// stream's first frame.
		run := startTerminal(t, terminalOptions{width: 100, height: 30, before: earlier.String()}, developmentMetadata(hookspot.url), hookspot.listen("--stream")...)
		screen := run.waitFor("the test hint, status line and prompt", func(screen string) bool {
			return strings.Contains(screen, "No requests yet.") && status.MatchString(screen) && prompt.MatchString(screen)
		})
		if !strings.Contains(screen, "earlier output 25\n"+banner) {
			t.Errorf("the banner or the output before it is gone:\n%s", screen)
		}
		if run.altScreen() {
			t.Error("the stream is on the alt screen")
		}
	})

	t.Run("stdin not a terminal", func(t *testing.T) {
		hookspot := startFakeHookspot(t, fakeHookspotSources)
		// A shell with history puts the banner's end, and so the stream's
		// first frame, on the last row; a slow join leaves that frame up.
		hookspot.holdJoins.Store(true)
		run := startTerminal(t, terminalOptions{width: 100, height: 30, before: strings.Repeat("$\r\n", 30), stdin: strings.NewReader("")}, developmentMetadata(hookspot.url), hookspot.listen()...)
		run.waitForText("○ connecting… · Acme | Payments · 0 requests")
		close(hookspot.release)
		hookspot.deliver(t, terminalDelivery)
		wholeBanner := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(banner) + `.*╮\n(│.*│\n)+╰─+╯$`)
		end := regexp.MustCompile(`\n╰─+╯\n● live · Acme \| Payments · 1 request +ctrl-c quit$`)
		screen := run.waitFor("the banner, card #1 and the status line below it", func(screen string) bool {
			return wholeBanner.MatchString(screen) && wholeCard(screen, 100) && end.MatchString(screen) && !strings.Contains(screen, "connecting")
		})
		// The prompt would come in the same frame as the status line.
		if prompt.MatchString(screen) || run.altScreen() {
			t.Fatalf("screen without a terminal on stdin:\n%s", screen)
		}
	})

	t.Run("stdout piped", func(t *testing.T) {
		local := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer local.Close()
		hookspot := startFakeHookspot(t, fakeHookspotSources)
		run := startTerminal(t, terminalOptions{width: 100, height: 30, pipeStdout: true}, developmentMetadata(hookspot.url), hookspot.listen("--forward-to", local.URL)...)
		hookspot.deliver(t, terminalDelivery)
		run.waitFor("#1 on stdout", func(string) bool { return row.MatchString(run.piped()) })

		// stdin is a terminal, so it takes line commands; replies go to stderr.
		run.send("\r")
		run.waitFor("the replay on stdout", func(string) bool { return strings.Contains(run.piped(), "\n#2 ↻ #1 ") })
		run.send("c 1\r")
		run.waitForText("curl -g -X POST '" + local.URL + "/webhooks/stripe'")
		run.send("?\r")
		run.waitForText("↵       replay the last request")
		hookspot.end(t)
		if code := run.wait(); code != 1 {
			t.Fatalf("exit code = %d, want 1 for the invalid delivery; screen:\n%s", code, run.text())
		}
		stdout := run.piped()
		for _, want := range []string{"↵ replay last · r N replay #N · c N cURL · e N fixture · t test event · ctrl-c quit ─╯\n", "\nReady. Waiting for requests (Ctrl-C to quit)\n"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("piped stdout has no %q:\n%s", want, stdout)
			}
		}
		if strings.Contains(stdout, "curl") || strings.ContainsRune(stdout, '\x1b') {
			t.Errorf("piped stdout has a command reply or escape sequences: %q", stdout)
		}
	})

	t.Run("stdout piped in inspect mode", func(t *testing.T) {
		hookspot := startFakeHookspot(t, fakeHookspotSources)
		run := startTerminal(t, terminalOptions{width: 100, height: 30, pipeStdout: true}, developmentMetadata(hookspot.url), hookspot.listen()...)
		// A pager reading the pipe, such as less, keeps the keyboard.
		hookspot.end(t)
		if code := run.wait(); code != 1 {
			t.Fatalf("exit code = %d, want 1 for the invalid delivery; screen:\n%s", code, run.text())
		}
		if stdout := run.piped(); !strings.Contains(stdout, "─ ctrl-c quit ─╯\n") {
			t.Errorf("piped inspect mode offers line commands:\n%s", stdout)
		}
	})

	t.Run("background job", func(t *testing.T) {
		local := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer local.Close()
		hookspot := startFakeHookspot(t, fakeHookspotSources)
		run := startTerminal(t, terminalOptions{width: 100, height: 30, pipeStdout: true, background: true}, developmentMetadata(hookspot.url), hookspot.listen("--forward-to", local.URL)...)
		// Reading the terminal from the background would stop the job.
		hookspot.deliver(t, terminalDelivery)
		run.waitFor("#1 on stdout", func(string) bool { return row.MatchString(run.piped()) })
		hookspot.end(t)
		if code := run.wait(); code != 1 {
			t.Fatalf("exit code = %d, want 1 for the invalid delivery; screen:\n%s", code, run.text())
		}
		if stdout := run.piped(); strings.Contains(stdout, "c N cURL") {
			t.Errorf("a background job offers line commands:\n%s", stdout)
		}
	})

	t.Run("TERM=dumb", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("ConPTY sends conhost's own rendering of the console, which always has escape sequences")
		}
		hookspot := startFakeHookspot(t, fakeHookspotSources)
		run := startTerminal(t, terminalOptions{width: 100, height: 30, environment: map[string]string{"TERM": "dumb"}}, developmentMetadata(hookspot.url), hookspot.listen()...)
		hookspot.deliver(t, terminalDelivery)
		run.waitForText(card)
		if written := run.written(); strings.ContainsRune(written, '\x1b') {
			t.Errorf("a dumb terminal got escape sequences: %q", written)
		}
	})

	t.Run("NO_COLOR", func(t *testing.T) {
		hookspot := startFakeHookspot(t, fakeHookspotSources)
		run := startTerminal(t, terminalOptions{width: 100, height: 30, environment: map[string]string{"NO_COLOR": "1"}}, developmentMetadata(hookspot.url), hookspot.listen("--stream")...)
		hookspot.deliver(t, terminalDelivery)
		run.waitFor("the banner, card #1 and the status line", func(screen string) bool {
			return strings.Contains(screen, banner) && strings.Contains(screen, card) &&
				strings.Contains(screen, "● live · Acme | Payments · 1 request")
		})
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
	stdout bytes.Buffer

	changed chan struct{}
	drained chan struct{}
	exited  chan struct{}
	exitErr error
}

type terminalOptions struct {
	width, height int
	// before is on the screen when the command starts, as earlier shell
	// output.
	before      string
	environment map[string]string
	// stdin takes the terminal's place when set.
	stdin io.Reader
	// pipeStdout pipes stdout, read with piped, instead of the terminal.
	pipeStdout bool
	// background runs the command as a job control shell's background job,
	// in a process group that doesn't own the terminal.
	background bool
}

// startTerminal runs the command on a pseudo-terminal of the given size, with
// TERM=xterm-256color, TZ=UTC and a temporary HOME. stderr is always the
// terminal.
func startTerminal(t *testing.T, options terminalOptions, metadata map[string]string, args ...string) *terminalRun {
	t.Helper()
	if runtime.GOOS == "windows" && (options.stdin != nil || options.pipeStdout) {
		t.Skip("ConPTY makes its console the command's stdin, stdout and stderr, so neither can be replaced")
	}
	if runtime.GOOS == "windows" && options.before != "" {
		t.Skip("ConPTY clears the screen on its first paint, so no earlier output stays on it")
	}
	pty, err := xpty.NewPty(options.width, options.height)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pty.Close() })
	environment := map[string]string{"TERM": "xterm-256color", "TZ": "UTC"}
	maps.Copy(environment, options.environment)
	command := commandProcess(t, metadata, environment, args...)
	if options.background {
		shell, err := exec.LookPath("sh")
		if err != nil {
			t.Fatal(err)
		}
		command.Path = shell
		command.Args = append([]string{"sh", "-c", `set -m; "$@" & wait $!`, "sh"}, command.Args...)
	}
	command.Stdin = options.stdin
	// The first standard file on the terminal makes it the controlling one.
	controlling := 0
	if options.stdin != nil {
		controlling = 1
		if options.pipeStdout {
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
	if options.pipeStdout {
		command.Stdout = pipedStdout{r}
	}
	_, _ = r.screen.Write([]byte(options.before))
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

// pipedStdout keeps the command's piped stdout and wakes waitFor.
type pipedStdout struct{ r *terminalRun }

func (p pipedStdout) Write(b []byte) (int, error) {
	p.r.mu.Lock()
	p.r.stdout.Write(b)
	p.r.mu.Unlock()
	select {
	case p.r.changed <- struct{}{}:
	default:
	}
	return len(b), nil
}

// piped is everything the command wrote to its piped stdout.
func (r *terminalRun) piped() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stdout.String()
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

// requireAltScreen fails unless the command is on the alt screen. Windows
// isn't checked: conhost may keep the alt screen to itself and repaint it on
// ConPTY's main screen.
func (r *terminalRun) requireAltScreen() {
	r.t.Helper()
	if runtime.GOOS != "windows" && !r.altScreen() {
		r.t.Errorf("not on the alt screen:\n%s", r.text())
	}
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

// boxEdges pairs each box-drawing character that starts a line with the one
// that must end it.
var boxEdges = map[rune]rune{'╭': '╮', '│': '│', '├': '┤', '╰': '╯'}

// wholeBoxes reports whether every line of screen that starts a box ends it
// in column width: nothing in a box wrapped, spilled over or was cut.
func wholeBoxes(screen string, width int) bool {
	for _, line := range strings.Split(screen, "\n") {
		first, _ := utf8.DecodeRuneInString(line)
		last, _ := utf8.DecodeLastRuneInString(line)
		if end, box := boxEdges[first]; box && (last != end || ansi.StringWidth(line) != width) {
			return false
		}
	}
	return true
}

// firstCard is the stream's card #1, from its top border to its bottom one.
var firstCard = regexp.MustCompile(`(?ms)^╭─ #1 .*?^╰[^\n]*`)

// wholeCard reports whether the stream shows card #1 whole in a terminal
// width columns wide. Cards leave the last column free.
func wholeCard(screen string, width int) bool {
	card := firstCard.FindString(screen)
	return card != "" && wholeBoxes(card, width-1)
}

// fakeHookspot serves the Acme | Payments project and its sources, and a
// websocket that accepts the join, then sends each payload given to deliver.
// It serves one run: with two connected, either may take a delivery.
type fakeHookspot struct {
	url        string
	config     string
	deliveries chan any
	// joins holds the first two channel joins: topic, event and payload.
	joins chan []json.RawMessage
	// rejectJoins answers joins as a project that isn't found.
	rejectJoins atomic.Bool
	done        chan struct{}
	// holdJoins holds each join's reply until release closes.
	holdJoins atomic.Bool
	release   chan struct{}
}

// hangUp, delivered, drops the websocket connection.
type hangUp struct{}

// endListen, delivered, has no correlation fields, so listen ends with an
// error.
var endListen = map[string]string{}

// fakeHookspotConfig signs in and selects the fake project.
const fakeHookspotConfig = "schema_version = 1\ncli_key = 'key'\nproject = 'proj_payments'\n"

// fakeHookspotSources are two sources: one route named, one not.
const fakeHookspotSources = `[
	{"uid":"src_stripe","name":"stripe","url":"https://in.hookspot.test/src_stripe","active":true,"routes":[{"uid":"rte_stripe","name":"payments","destination":{"path":"/webhooks/stripe"}}]},
	{"uid":"src_github","name":"github","url":"https://in.hookspot.test/src_github","active":true,"routes":[{"uid":"rte_github","destination":{"path":"/webhooks/github"}}]}
]`

func startFakeHookspot(t *testing.T, sources string) *fakeHookspot {
	t.Helper()
	hookspot := &fakeHookspot{
		config:     filepath.Join(t.TempDir(), "config.toml"),
		deliveries: make(chan any),
		joins:      make(chan []json.RawMessage, 2),
		release:    make(chan struct{}),
		done:       make(chan struct{}),
	}
	if err := writeCommandFixture(hookspot.config, []byte(fakeHookspotConfig)); err != nil {
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
			select {
			case hookspot.joins <- join[2:]:
			default:
			}
			if hookspot.holdJoins.Load() {
				select {
				case <-hookspot.release:
				case <-hookspot.done:
					return
				}
			}
			if hookspot.rejectJoins.Load() {
				_ = conn.WriteJSON([]any{join[0], join[1], join[2], "phx_reply", map[string]any{"status": "error", "response": map[string]string{"reason": "not_found"}}})
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
					if _, ok := payload.(hangUp); ok {
						return
					}
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
	t.Cleanup(func() { close(hookspot.done) })
	hookspot.url = server.URL
	return hookspot
}

// play delivers payloads, then ends listen, alongside a run that blocks until
// listen exits.
func (h *fakeHookspot) play(payloads ...any) {
	go func() {
		for _, payload := range append(payloads, endListen) {
			select {
			case h.deliveries <- payload:
			case <-h.done:
				return
			}
		}
	}()
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

// end ends listen once it has joined.
func (h *fakeHookspot) end(t *testing.T) {
	t.Helper()
	h.deliver(t, endListen)
}

// terminalDelivery is a POST of payment_intent.succeeded to stripe.
var terminalDelivery = ws.Delivery{
	AttemptUID: "att_1", RequestUID: "req_1", SourceUID: "src_stripe", Method: "POST", Path: "/webhooks/stripe",
	Headers: http.Header{"Content-Type": []string{"application/json"}},
	Body:    []byte(`{"type":"payment_intent.succeeded"}`),
}

// terminalColor is a styling sequence that sets a color; 39 and 49 only
// reset one.
var terminalColor = regexp.MustCompile(`\x1b\[([0-9]+;)*(3[0-8]|4[0-8]|9[0-7]|10[0-7])(;[0-9]+)*m`)
