package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/charmbracelet/x/vt"
	"github.com/gorilla/websocket"

	"hookspot/internal/api"
	"hookspot/internal/cards"
	"hookspot/internal/session"
	"hookspot/internal/ws"
)

var sources = []api.Source{{UID: "src_stripe", Name: "stripe", Routes: []api.Route{{UID: "rte_stripe", Destination: api.Destination{Path: "/hooks"}}}}}

func delivery(attempt int, path string) ws.Delivery {
	return ws.Delivery{AttemptUID: "att_" + strconv.Itoa(attempt), SourceUID: "src_stripe", Method: "POST", Path: path}
}

// forwarder answers 200, or 500 for /fail, a millisecond later. When set,
// called hears of each forward and gate holds it until closed.
type forwarder struct {
	called chan<- struct{}
	gate   <-chan struct{}
}

func (f forwarder) Forward(_ context.Context, _, path, _ string, _ []byte, _ http.Header) (*http.Response, error) {
	if f.called != nil {
		f.called <- struct{}{}
	}
	if f.gate != nil {
		<-f.gate
	}
	time.Sleep(time.Millisecond)
	status := http.StatusOK
	if path == "/fail" {
		status = http.StatusInternalServerError
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":"boom"}`)),
	}, nil
}

func (forwarder) DestinationURL(path, _ string) (*url.URL, error) {
	return url.Parse("http://localhost:3000" + path)
}

func (forwarder) String() string { return "http://localhost:3000" }

// output is a program's output, safe to read while it draws.
type output struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

func (o *output) Read(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Read(p)
}

// keyboard is a program's input that the test types into.
func keyboard(t *testing.T) (io.Reader, func(keys string)) {
	t.Helper()
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	return reader, func(keys string) {
		t.Helper()
		if _, err := writer.Write([]byte(keys)); err != nil {
			t.Fatal(err)
		}
	}
}

// harness wires a session to a Program's stream sink, as listen does. ctx is
// the listen context the program stops.
type harness struct {
	ctx     context.Context
	program *Program
	session *session.Session
	out     *output
	exits   chan int
	done    chan result
	prompt  bool
	forward bool
}

type result struct {
	model tea.Model
	err   error
}

// newHarness forwards with fwd, or only prints when it's nil; input is nil
// when stdin isn't a terminal.
func newHarness(t *testing.T, fwd session.Forwarder, input io.Reader) *harness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := &harness{ctx: ctx, out: &output{}, exits: make(chan int, 1), done: make(chan result, 1), prompt: input != nil, forward: fwd != nil}
	h.program = NewProgram(input, h.out, cancel)
	h.program.exit = func(code int) { h.exits <- code }
	h.session = session.New(ctx, sources, fwd, h.program.StreamSink(cards.Listen{Sources: map[string]string{"src_stripe": "stripe"}}, "https://hookspot.test/acme/payments/requests"))
	return h
}

func (h *harness) stream() Stream {
	return Stream{Requests: h.session, Println: h.program.Println, Project: "Acme | Payments", Forwarding: h.forward, Prompt: h.prompt}
}

func (h *harness) run(model tea.Model) {
	go func() {
		final, err := h.program.Run(model)
		h.done <- result{final, err}
	}()
	// The program sends its size from a goroutine; this one lands before the
	// test's first event.
	h.program.Send(tea.WindowSizeMsg{Width: cards.DefaultWidth, Height: 24})
}

func (h *harness) wait(t *testing.T) result {
	t.Helper()
	select {
	case r := <-h.done:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("program did not exit")
		return result{}
	}
}

func (h *harness) handle(t *testing.T, d ws.Delivery) {
	t.Helper()
	if _, err := h.session.Handle(d); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) emit(t *testing.T, event session.Event) {
	t.Helper()
	if err := h.session.Emit(event); err != nil {
		t.Fatal(err)
	}
}

// receive waits for a value from ch.
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}

// waitFor waits until out meets condition.
func waitFor(t *testing.T, out io.Reader, condition func([]byte) bool) {
	t.Helper()
	teatest.WaitFor(t, out, condition, teatest.WithDuration(5*time.Second))
}

var p50Pattern = regexp.MustCompile(`p50 \S+`)

// view is a model's view without color and with its p50 masked.
func view(model tea.Model) string {
	return p50Pattern.ReplaceAllString(ansi.Strip(model.View().Content), "p50 Nms")
}

var cardStart = regexp.MustCompile(`^(?:╭─ )?#(\d+) `)

// printed lists the numbers of the cards printed whole in out, in order, and
// fails on a card broken by other output.
func printed(t *testing.T, out []byte) []int {
	var numbers []int
	inCard := false
	for _, line := range strings.Split(ansi.Strip(string(out)), "\n") {
		line = strings.Trim(line, "\r")
		if inCard && !strings.HasPrefix(line, "│") && !strings.HasPrefix(line, "├") && !strings.HasPrefix(line, "╰") {
			t.Fatalf("card broken by %q in:\n%s", line, ansi.Strip(string(out)))
		}
		inCard = inCard && !strings.HasPrefix(line, "╰")
		if match := cardStart.FindStringSubmatch(line); match != nil {
			number, _ := strconv.Atoi(match[1])
			numbers = append(numbers, number)
			inCard = strings.HasPrefix(line, "╭")
		}
	}
	return numbers
}

func TestStreamPrintsWholeCardsInOrder(t *testing.T) {
	keys, typeKeys := keyboard(t)
	h := newHarness(t, forwarder{}, keys)
	h.run(h.stream())
	h.handle(t, delivery(0, "/hooks"))

	var burst sync.WaitGroup
	for i := 1; i <= 20; i++ {
		path := "/hooks"
		if i%4 == 0 {
			path = "/fail"
		}
		burst.Go(func() {
			if _, err := h.session.Handle(delivery(i, path)); err != nil {
				t.Error(err)
			}
		})
	}
	// Replays race the burst.
	typeKeys("r 1\rr 1\r\r")
	burst.Wait()

	var all []byte
	waitFor(t, h.out, func(out []byte) bool {
		all = out
		return len(printed(t, out)) == 24
	})
	want := make([]int, 24)
	for i := range want {
		want[i] = i + 1
	}
	if got := printed(t, all); !slices.Equal(got, want) {
		t.Fatalf("printed cards = %v, want %v", got, want)
	}
	if replays := strings.Count(ansi.Strip(string(all)), "↻ #"); replays != 3 {
		t.Fatalf("replay marks = %d, want 3", replays)
	}

	h.program.Quit()
	if r := h.wait(t); r.err != nil {
		t.Fatal(r.err)
	}
}

func TestStreamStatusLine(t *testing.T) {
	lost := session.ConnectionLost{Err: errors.New("dial tcp: connection refused"), RetryIn: 2 * time.Second}
	tests := []struct {
		name    string
		inspect bool
		prompt  bool
		events  func(t *testing.T, h *harness, typeKeys func(string))
	}{
		{name: "connecting", events: func(*testing.T, *harness, func(string)) {}},
		{name: "counts", events: func(t *testing.T, h *harness, _ func(string)) {
			h.emit(t, session.Ready{})
			h.handle(t, delivery(1, "/hooks"))
			h.handle(t, delivery(2, "/fail"))
		}},
		{name: "inspect counts", inspect: true, events: func(t *testing.T, h *harness, _ func(string)) {
			h.emit(t, session.Ready{})
			h.handle(t, delivery(1, "/hooks"))
		}},
		{name: "offline", events: func(t *testing.T, h *harness, _ func(string)) {
			h.emit(t, session.Ready{})
			h.handle(t, delivery(1, "/hooks"))
			// The reason stays; the update alert doesn't fit beside it.
			h.emit(t, updateAvailable)
			h.emit(t, lost)
		}},
		{name: "reconnected", events: func(t *testing.T, h *harness, _ func(string)) {
			h.emit(t, session.Ready{})
			h.emit(t, lost)
			h.emit(t, session.Reconnected{Offline: 3 * time.Second})
			h.emit(t, session.RootNotFound{Root: "http://localhost:3000/", Status: http.StatusNotFound})
			waitFor(t, h.out, func(out []byte) bool {
				plain := ansi.Strip(string(out))
				return strings.Contains(plain, "Reconnected after 3s offline. Requests that arrived meanwhile were not delivered; retry them from https://hookspot.test/acme/payments/requests") &&
					strings.Contains(plain, "http://localhost:3000/ returned 404. If your webhook route is elsewhere")
			})
		}},
		{name: "notice", events: func(t *testing.T, h *harness, _ func(string)) {
			h.emit(t, session.Notice{Text: deprecation})
			h.emit(t, session.Ready{})
		}},
		{name: "stopping with a prompt", prompt: true, events: func(t *testing.T, h *harness, typeKeys func(string)) {
			h.emit(t, session.Ready{})
			// The prompt goes, and the status line says a second Ctrl-C kills,
			// over the update alert.
			h.emit(t, updateAvailable)
			typeKeys("\x03")
			receive(t, h.ctx.Done())
		}},
		{name: "inspect prompt", inspect: true, prompt: true, events: func(t *testing.T, h *harness, _ func(string)) {
			h.emit(t, session.Ready{})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var fwd session.Forwarder = forwarder{}
			if test.inspect {
				fwd = nil
			}
			// Without a terminal on stdin there's no prompt.
			var input io.Reader
			typeKeys := func(string) {}
			if test.prompt {
				input, typeKeys = keyboard(t)
			}
			h := newHarness(t, fwd, input)
			h.run(h.stream())
			test.events(t, h, typeKeys)
			// Quitting without Program.Quit keeps the live frame.
			<-h.program.started
			h.program.program.Quit()
			golden.RequireEqual(t, view(h.wait(t).model))
		})
	}
}

// ingest is a fake Hookspot ingest endpoint: it answers 202, or 404 under
// /missing, and hands each accepted test event's id to deliver.
func ingest(t *testing.T, deliver func(id string)) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		if deliver != nil {
			deliver(r.Header.Get("X-Hookspot-Test"))
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestStreamTestEvent(t *testing.T) {
	keys, typeKeys := keyboard(t)
	h := newHarness(t, forwarder{}, keys)
	delivered := make(chan error, 1)
	base := ingest(t, func(id string) {
		d := delivery(1, "/hooks")
		d.Headers = http.Header{"x-hookspot-test": {id}}
		// Hookspot delivers it over the websocket, after answering the POST.
		go func() {
			_, err := h.session.Handle(d)
			delivered <- err
		}()
	})
	public := []api.Source{{UID: "src_stripe", Name: "stripe", URL: base + "/in/src_stripe", Routes: sources[0].Routes}}
	h.session = session.New(h.ctx, public, forwarder{}, h.program.StreamSink(cards.Listen{Sources: map[string]string{"src_stripe": "stripe"}}, ""))
	h.run(h.stream())

	h.emit(t, session.Ready{})
	waitFor(t, h.out, func(out []byte) bool {
		plain := ansi.Strip(string(out))
		return strings.Contains(plain, "No requests yet.") && strings.Contains(plain, " t   send a test event to stripe") &&
			strings.Contains(plain, "curl -X POST '"+base+"/in/src_stripe'")
	})
	typeKeys("t\r")
	waitFor(t, h.out, func(out []byte) bool {
		plain := ansi.Strip(string(out))
		return strings.Contains(plain, " test ") && strings.Contains(plain, "✓ path works: hookspot → this terminal → http://localhost:3000/hooks")
	})
	if err := receive(t, delivered); err != nil {
		t.Fatal(err)
	}

	h.program.Quit()
	if r := h.wait(t); r.err != nil {
		t.Fatal(r.err)
	}
}

// discard is a sink for sessions run without a program.
type discard struct{}

func (discard) Emit(session.Event) error { return nil }

// typeLine types line and ↵ into model, running any command it starts the way
// the program would.
func typeLine(model tea.Model, line string) tea.Model {
	for _, key := range typed(line) {
		model, _ = model.Update(key)
	}
	return press(model, enter)
}

func TestStreamCommands(t *testing.T) {
	boom := errors.New("boom\x1b")
	help := "c N     copy request #N as cURL\ne N     export request #N as a fixture\nt NAME  send a test event to source NAME\n"
	tests := []struct {
		name    string
		inspect bool
		paste   string
		line    string
		err     error
		// asked is what the line asks of the session.
		asked string
		reply string
	}{
		{name: "replay last", line: "", asked: "replay last"},
		{name: "replay a number", line: " r  2 ", asked: "replay #2"},
		{name: "pasted", paste: "r\t2\n", asked: "replay #2"},
		{name: "test a source named in words", line: "t  Stripe  Prod ", asked: "test Stripe  Prod", reply: "test event sent to Stripe  Prod"},
		{name: "typo", line: "r x", reply: "commands: ↵ replay last · r N replay #N · c N cURL · e N fixture · t test event · ? help"},
		{name: "help", line: "?", reply: "↵       replay the last request\nr N     replay request #N\n" + help + "ctrl-c  stop listening"},
		// Errors come from the session, escaped.
		{name: "failed replay", line: "r 9", err: boom, asked: "replay #9", reply: `boom\x1b`},
		{name: "failed copy", line: "c 9", err: boom, asked: "curl #9 redact true", reply: `boom\x1b`},
		{name: "failed export", line: "e 9", err: boom, asked: "fixture #9 redact true", reply: `boom\x1b`},
		{name: "failed test event", line: "t x", err: boom, asked: "test x", reply: `boom\x1b`},
		{name: "inspect replay last", inspect: true, line: "", reply: "nothing to replay without --forward-to"},
		{name: "inspect replay", inspect: true, line: "r 1", reply: "nothing to replay without --forward-to"},
		{name: "inspect typo", inspect: true, line: "c", reply: "commands: c N cURL · e N fixture · t test event · ? help"},
		{name: "inspect help", inspect: true, line: "?", reply: help + "replays need --forward-to\nctrl-c  stop listening"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := newFake()
			requests.err = test.err
			model := tea.Model(Stream{Requests: requests, Project: "Acme | Payments", Forwarding: !test.inspect, Prompt: true})
			if test.paste != "" {
				model, _ = model.Update(tea.PasteMsg{Content: test.paste})
			}
			model = typeLine(model, test.line)
			asked := ""
			select {
			case asked = <-requests.asked:
			default:
			}
			if got := model.(Stream).reply; got != test.reply || asked != test.asked {
				t.Errorf("reply %q, asked for %q; want %q, %q", got, asked, test.reply, test.asked)
			}
			if got := model.(Stream).input; got != "" {
				t.Errorf("input after ↵ = %q", got)
			}
		})
	}

	t.Run("prompt", func(t *testing.T) {
		model := tea.Model(Stream{Requests: newFake(), Project: "Acme | Payments", Forwarding: true, Prompt: true})
		model, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		// The reply wraps rather than being cut at the terminal's width.
		model = typeLine(model, "x")
		for _, key := range append(typed("r 42"), backspace) {
			model, _ = model.Update(key)
		}
		golden.RequireEqual(t, view(model))
	})
}

func TestStreamCopiesAndExports(t *testing.T) {
	t.Chdir(t.TempDir())
	d := delivery(1, "/hooks")
	d.RequestUID = "req_1"
	d.Headers = http.Header{"Authorization": []string{"Bearer secret"}}
	d.Body = []byte("a\r\nb")
	copied := "copied #1 as cURL\n" + cards.ClipboardNote

	for _, test := range []struct {
		name  string
		show  bool
		reply string
		// header is how the printed command shows the Authorization header.
		header string
	}{
		{name: "redacted", reply: copied + "\nsensitive headers are hidden; --show-sensitive-headers shows the full command", header: "-H 'Authorization: [redacted]'"},
		{name: "--show-sensitive-headers", show: true, reply: copied, header: "-H 'Authorization: Bearer secret'"},
	} {
		t.Run("copy "+test.name, func(t *testing.T) {
			keys, typeKeys := keyboard(t)
			h := newHarness(t, forwarder{}, keys)
			stream := h.stream()
			stream.ShowSensitiveHeaders = test.show
			h.run(stream)
			h.handle(t, d)
			full, err := h.session.Curl(1, false)
			if err != nil {
				t.Fatal(err)
			}

			typeKeys("c 1\r")
			// The full command goes to the clipboard; the shown one prints.
			waitFor(t, h.out, func(out []byte) bool {
				plain := ansi.Strip(string(out))
				return bytes.Contains(out, []byte(ansi.SetSystemClipboard(full.Command))) && strings.Contains(plain, test.header)
			})
			// Quitting without Program.Quit keeps the reply.
			h.program.program.Quit()
			if got := h.wait(t).model.(Stream).reply; got != test.reply {
				t.Errorf("reply = %q, want %q", got, test.reply)
			}
		})
	}

	t.Run("export", func(t *testing.T) {
		fixtures, err := filepath.Abs("hookspot-fixtures")
		if err != nil {
			t.Fatal(err)
		}
		sess := session.New(context.Background(), sources, forwarder{}, discard{})
		if _, err := sess.Handle(d); err != nil {
			t.Fatal(err)
		}
		want := "exported #1 to " + filepath.Join(fixtures, "req_1_rte_stripe.json") + " and " + filepath.Join(fixtures, "req_1_rte_stripe.body") + " · sensitive headers redacted"
		if got := typeLine(Stream{Requests: sess, Forwarding: true, Prompt: true}, "e 1").(Stream).reply; got != want {
			t.Errorf("reply = %q, want %q", got, want)
		}
	})
}

// TestPrintlnKeepsFullLinesWhole covers lines that fill the terminal's rows,
// whose last column tea's erase after each printed line would clear.
func TestPrintlnKeepsFullLinesWhole(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.run(h.stream())
	// Lines print above the first frame, as cards do.
	var out []byte
	waitFor(t, h.out, func(drawn []byte) bool {
		out = drawn
		return bytes.Contains(drawn, []byte("connecting"))
	})
	width := cards.DefaultWidth
	lines := []string{strings.Repeat("a", width-1) + "1", strings.Repeat("b", 2*width-1) + "2", "after"}
	for _, line := range lines {
		if err := h.program.Println(line); err != nil {
			t.Fatal(err)
		}
	}
	h.program.Quit()
	if r := h.wait(t); r.err != nil {
		t.Fatal(r.err)
	}

	terminal := vt.NewEmulator(width, 24)
	t.Cleanup(func() { _ = terminal.InputPipe().(io.Closer).Close() })
	// The terminal's answers to queries go nowhere.
	go func() { _, _ = io.Copy(io.Discard, terminal) }()
	rest, err := io.ReadAll(h.out)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = terminal.Write(append(out, rest...))
	screen := terminal.String()
	for _, row := range []string{lines[0], lines[1][:width], lines[1][width:], "after"} {
		if !strings.Contains(screen, row+"\n") {
			t.Errorf("no row %q on the screen:\n%s", row, screen)
		}
	}
	// A line counted a row longer than it is leaves a stale frame behind.
	if strings.Contains(screen, "connecting") {
		t.Errorf("a stale frame on the screen:\n%s", screen)
	}
}

func TestCtrlC(t *testing.T) {
	t.Run("second kills and exits 130 without hanging the handler", func(t *testing.T) {
		keys, typeKeys := keyboard(t)
		called, gate := make(chan struct{}, 1), make(chan struct{})
		h := newHarness(t, forwarder{called: called, gate: gate}, keys)
		h.run(h.stream())
		handled := make(chan error, 1)
		go func() {
			_, err := h.session.Handle(delivery(1, "/hooks"))
			handled <- err
		}()
		receive(t, called)

		typeKeys("\x03\x03")
		select {
		case code := <-h.exits:
			if code != 130 {
				t.Fatalf("exit(%d), want 130", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("second Ctrl-C did not exit")
		}
		if r := h.wait(t); r.err != nil {
			t.Fatalf("killed program error = %v, want none", r.err)
		}

		close(gate)
		select {
		case err := <-handled:
			if !errors.Is(err, ErrClosed) {
				t.Fatalf("handler error = %v, want ErrClosed", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("handler hung after the program was killed")
		}
	})

	t.Run("after q, the first Ctrl-C kills", func(t *testing.T) {
		keys, typeKeys := keyboard(t)
		h := newHarness(t, nil, keys)
		h.run(screen())
		// q stops listening as the first Ctrl-C does, on the Sources page too.
		typeKeys("sq")
		receive(t, h.ctx.Done())
		typeKeys("\x03")
		if code := receive(t, h.exits); code != 130 {
			t.Fatalf("exit(%d), want 130", code)
		}
		if r := h.wait(t); r.err != nil {
			t.Fatalf("killed program error = %v, want none", r.err)
		}
	})
}

// listenTo runs a websocket client against a fake Hookspot that sends one
// delivery after the join, and returns the client's result.
func listenTo(t *testing.T, h *harness) <-chan error {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		_ = conn.WriteJSON([]any{nil, nil, join[2], "delivery", map[string]string{"attempt_uid": "att_1", "source_uid": "src_stripe", "method": "POST", "path": "/hooks"}})
		_, _, _ = conn.ReadMessage()
	}))
	t.Cleanup(server.Close)
	listened := make(chan error, 1)
	go func() {
		listened <- ws.New("ws"+strings.TrimPrefix(server.URL, "http"), "key", "", "project:proj_1", nil, "").Listen(h.ctx, h.session.Handle)
	}()
	return listened
}

// requireQuietStop fails unless listening ended with the cancelled context,
// not a handler error ("the delivery could not be processed").
func requireQuietStop(t *testing.T, listened <-chan error) {
	t.Helper()
	select {
	case err := <-listened:
		var sessionErr *ws.SessionError
		if !errors.Is(err, context.Canceled) || errors.As(err, &sessionErr) {
			t.Fatalf("listen error = %v, want the cancelled context", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("listening did not stop")
	}
}

func TestProgramExitingFirstStopsListening(t *testing.T) {
	t.Run("startup error", func(t *testing.T) {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		_ = writer.Close()
		_ = reader.Close()
		called := make(chan struct{}, 1)
		h := newHarness(t, forwarder{called: called}, reader)
		listened := listenTo(t, h)
		// The delivery's card waits for the program, which then fails to start.
		receive(t, called)
		h.run(h.stream())
		if r := h.wait(t); r.err == nil {
			t.Fatal("Run with a closed input = nil, want an error")
		}
		requireQuietStop(t, listened)
	})

	t.Run("panic", func(t *testing.T) {
		// bubbletea prints the recovered panic's stack to stderr before Run
		// returns.
		devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		stderr := os.Stderr
		os.Stderr = devNull
		t.Cleanup(func() {
			os.Stderr = stderr
			_ = devNull.Close()
		})

		keys, typeKeys := keyboard(t)
		called, gate := make(chan struct{}, 1), make(chan struct{})
		h := newHarness(t, forwarder{called: called, gate: gate}, keys)
		h.run(panicOnKey{})
		listened := listenTo(t, h)
		receive(t, called)

		typeKeys("x")
		if r := h.wait(t); !errors.Is(r.err, tea.ErrProgramPanic) {
			t.Fatalf("Run = %v, want the panic", r.err)
		}
		close(gate)
		requireQuietStop(t, listened)
	})
}

// panicOnKey panics on the first key.
type panicOnKey struct{}

func (panicOnKey) Init() tea.Cmd { return nil }

func (m panicOnKey) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); ok {
		panic("update failed")
	}
	return m, nil
}

func (panicOnKey) View() tea.View { return tea.NewView("") }
