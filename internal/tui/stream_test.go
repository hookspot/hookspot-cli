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
	"reflect"
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
	return Stream{Replayer: h.session, Project: "Acme | Payments", Forwarding: h.forward, Prompt: h.prompt}
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

func received(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
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
	teatest.WaitFor(t, h.out, func(out []byte) bool {
		all = out
		return len(printed(t, out)) == 24
	}, teatest.WithDuration(5*time.Second))
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
		events  func(t *testing.T, h *harness)
	}{
		{name: "connecting", events: func(*testing.T, *harness) {}},
		{name: "counts", events: func(t *testing.T, h *harness) {
			h.emit(t, session.Ready{})
			h.handle(t, delivery(1, "/hooks"))
			h.handle(t, delivery(2, "/fail"))
		}},
		{name: "inspect counts", inspect: true, events: func(t *testing.T, h *harness) {
			h.emit(t, session.Ready{})
			h.handle(t, delivery(1, "/hooks"))
		}},
		{name: "offline", events: func(t *testing.T, h *harness) {
			h.emit(t, session.Ready{})
			h.handle(t, delivery(1, "/hooks"))
			h.emit(t, lost)
		}},
		{name: "reconnected", events: func(t *testing.T, h *harness) {
			h.emit(t, session.Ready{})
			h.emit(t, lost)
			h.emit(t, session.Reconnected{Offline: 3 * time.Second})
			teatest.WaitFor(t, h.out, func(out []byte) bool {
				return strings.Contains(ansi.Strip(string(out)), "Reconnected after 3s offline. Requests that arrived meanwhile were not delivered; retry them from https://hookspot.test/acme/payments/requests")
			})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var fwd session.Forwarder = forwarder{}
			if test.inspect {
				fwd = nil
			}
			// Without a terminal on stdin there's no prompt.
			h := newHarness(t, fwd, nil)
			h.run(h.stream())
			test.events(t, h)
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
	t.Run("hint, then a test event through Hookspot", func(t *testing.T) {
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
		stream := h.stream()
		stream.Tester = h.session
		h.run(stream)

		h.emit(t, session.Ready{})
		teatest.WaitFor(t, h.out, func(out []byte) bool {
			plain := ansi.Strip(string(out))
			return strings.Contains(plain, "No requests yet.") && strings.Contains(plain, " t   send a test event to stripe") &&
				strings.Contains(plain, "curl -X POST '"+base+"/in/src_stripe'")
		})
		typeKeys("t\r")
		teatest.WaitFor(t, h.out, func(out []byte) bool {
			plain := ansi.Strip(string(out))
			return strings.Contains(plain, " test ") && strings.Contains(plain, "✓ path works: hookspot → this terminal → http://localhost:3000/hooks")
		})
		if err := <-delivered; err != nil {
			t.Fatal(err)
		}

		h.program.Quit()
		if r := h.wait(t); r.err != nil {
			t.Fatal(r.err)
		}
	})

	t.Run("replies", func(t *testing.T) {
		base := ingest(t, nil)
		stripe := api.Source{Name: "stripe", URL: base + "/in/src_stripe"}
		github := api.Source{Name: "github", URL: base + "/missing"}
		stripeProd := api.Source{Name: "Stripe  Prod", URL: base + "/in/src_stripe_prod"}
		for _, test := range []struct {
			name    string
			sources []api.Source
			line    string
			want    string
		}{
			{name: "the only source", sources: []api.Source{stripe}, line: "t", want: "test event sent to stripe"},
			{name: "a named source", sources: []api.Source{stripe, github}, line: " t  stripe ", want: "test event sent to stripe"},
			{name: "a name with spaces", sources: []api.Source{stripeProd, github}, line: "t Stripe  Prod ", want: "test event sent to Stripe  Prod"},
			{name: "several sources", sources: []api.Source{stripe, github}, line: "t", want: "test which source? t stripe · t github"},
			{name: "not listened to", sources: []api.Source{stripe}, line: "t shopify\x1b", want: `shopify\x1b: not a source this run listens to`},
			{name: "Hookspot refuses", sources: []api.Source{github}, line: "t", want: "test event to github: Hookspot answered 404 Not Found"},
		} {
			t.Run(test.name, func(t *testing.T) {
				sess := session.New(context.Background(), test.sources, nil, discard{})
				model := tea.Model(Stream{Replayer: sess, Tester: sess, Project: "Acme | Payments", Prompt: true})
				model = typeLine(model, test.line)
				if got := model.(Stream).reply; got != test.want {
					t.Errorf("reply = %q, want %q", got, test.want)
				}
			})
		}
	})
}

// discard is a sink for sessions run without a program.
type discard struct{}

func (discard) Emit(session.Event) error { return nil }

// typeLine types line and ↵ into model, running any command it starts the way
// the program would.
func typeLine(model tea.Model, line string) tea.Model {
	for _, r := range line {
		model, _ = model.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	model, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			model, _ = model.Update(msg)
		}
	}
	return model
}

func TestStreamCommands(t *testing.T) {
	forwarding := session.New(context.Background(), sources, forwarder{}, discard{})
	big := delivery(1, "/hooks")
	// History keeps 64 MiB of bodies, so #2 evicts #1.
	big.Body = make([]byte, 64<<20)
	for _, d := range []ws.Delivery{big, {AttemptUID: "att_2", SourceUID: "src_stripe", Method: "POST", Path: "/hooks", Body: []byte("{}")}} {
		if _, err := forwarding.Handle(d); err != nil {
			t.Fatal(err)
		}
	}
	inspecting := session.New(context.Background(), sources, nil, discard{})
	if _, err := inspecting.Handle(delivery(1, "/hooks")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		replay  Replayer
		line    string
		want    string
		replays bool
	}{
		{name: "replay last", replay: forwarding, line: "", replays: true},
		{name: "replay a number", replay: forwarding, line: " r  2 ", replays: true},
		{name: "evicted", replay: forwarding, line: "r 1", want: "#1: request dropped from history"},
		{name: "missing", replay: forwarding, line: "r 9", want: "#9: no such request"},
		{name: "typo", replay: forwarding, line: "r x", want: "commands: ↵ replay last · r N replay #N · c N copy as cURL · e N export fixture · t test event · ? help"},
		{name: "help", replay: forwarding, line: "?", want: "↵       replay the last request\nr N     replay request #N\nc N     copy request #N as cURL\ne N     export request #N as a fixture\nt NAME  send a test event to source NAME\nctrl-c  stop listening"},
		{name: "inspect replay last", replay: inspecting, line: "", want: "nothing to replay without --forward-to"},
		{name: "inspect replay", replay: inspecting, line: "r 1", want: "nothing to replay without --forward-to"},
		{name: "inspect typo", replay: inspecting, line: "c", want: "commands: c N copy as cURL · e N export fixture · t test event · ? help"},
		{name: "inspect help", replay: inspecting, line: "?", want: "c N     copy request #N as cURL\ne N     export request #N as a fixture\nt NAME  send a test event to source NAME\nreplays need --forward-to\nctrl-c  stop listening"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			replayer := &countingReplayer{Replayer: test.replay}
			model := tea.Model(Stream{Replayer: replayer, Project: "Acme | Payments", Forwarding: test.replay == forwarding, Prompt: true})
			model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
			model = typeLine(model, test.line)
			if got := model.(Stream).reply; got != test.want {
				t.Errorf("reply = %q, want %q", got, test.want)
			}
			if test.replays != (replayer.replays == 1) {
				t.Errorf("replays = %d", replayer.replays)
			}
			if got := model.(Stream).input; got != "" {
				t.Errorf("input after ↵ = %q", got)
			}
		})
	}

	t.Run("prompt", func(t *testing.T) {
		model := tea.Model(Stream{Replayer: forwarding, Project: "Acme | Payments", Forwarding: true, Prompt: true})
		model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
		model = typeLine(model, "r 9")
		for _, key := range []tea.KeyPressMsg{{Code: 'r', Text: "r"}, {Code: ' ', Text: " "}, {Code: '4', Text: "4"}, {Code: '2', Text: "2"}, {Code: tea.KeyBackspace}} {
			model, _ = model.Update(key)
		}
		golden.RequireEqual(t, view(model))
	})
}

func TestStreamCopiesAndExports(t *testing.T) {
	t.Chdir(t.TempDir())
	fixtures, err := filepath.Abs("hookspot-fixtures")
	if err != nil {
		t.Fatal(err)
	}
	sess := session.New(context.Background(), sources, forwarder{}, discard{})
	d := delivery(1, "/hooks")
	d.RequestUID = "req_1"
	d.Headers = http.Header{"Authorization": []string{"Bearer secret"}}
	d.Body = []byte("a\r\nb")
	if _, err := sess.Handle(d); err != nil {
		t.Fatal(err)
	}
	full, err := sess.Curl(1, false)
	if err != nil {
		t.Fatal(err)
	}
	redacted, err := sess.Curl(1, true)
	if err != nil {
		t.Fatal(err)
	}
	copied := "copied #1 as cURL\ncopying needs a terminal with OSC 52 (Terminal.app has none)"

	for _, test := range []struct {
		name  string
		show  bool
		reply string
		shown string
	}{
		{name: "redacted", reply: copied + "\nsensitive headers are hidden; --show-sensitive-headers shows the full command", shown: redacted.Shown},
		{name: "--show-sensitive-headers", show: true, reply: copied, shown: full.Command},
	} {
		t.Run("copy "+test.name, func(t *testing.T) {
			model := tea.Model(Stream{Replayer: sess, Exporter: sess, Forwarding: true, Prompt: true, ShowSensitiveHeaders: test.show})
			for _, r := range "c 1" {
				model, _ = model.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			model, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			model, cmd = model.Update(cmd())
			if got := model.(Stream).reply; got != test.reply {
				t.Errorf("reply = %q, want %q", got, test.reply)
			}
			var got []tea.Msg
			for _, cmd := range cmd().(tea.BatchMsg) {
				got = append(got, cmd())
			}
			// The full command goes to the clipboard; the shown one prints.
			want := []tea.Msg{tea.SetClipboard(full.Command)(), tea.Println(cards.Sanitize(test.shown))()}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("commands = %q, want %q", got, want)
			}
		})
	}

	for _, test := range []struct {
		name, line, want string
	}{
		{name: "export", line: "e 1", want: "exported #1 to " + filepath.Join(fixtures, "req_1_rte_stripe.json") + " and " + filepath.Join(fixtures, "req_1_rte_stripe.body") + " · sensitive headers redacted"},
		{name: "copy a missing number", line: "c 9", want: "#9: no such request"},
		{name: "export a missing number", line: "e 9", want: "#9: no such request"},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := typeLine(Stream{Replayer: sess, Exporter: sess, Forwarding: true, Prompt: true}, test.line)
			if got := model.(Stream).reply; got != test.want {
				t.Errorf("reply = %q, want %q", got, test.want)
			}
		})
	}
}

// countingReplayer counts successful replays.
type countingReplayer struct {
	Replayer
	replays int
}

func (r *countingReplayer) Replay(n int) error {
	err := r.Replayer.Replay(n)
	if err == nil {
		r.replays++
	}
	return err
}

func (r *countingReplayer) ReplayLast() error {
	err := r.Replayer.ReplayLast()
	if err == nil {
		r.replays++
	}
	return err
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
		received(t, called)

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

	t.Run("Stop works like the first press", func(t *testing.T) {
		keys, typeKeys := keyboard(t)
		h := newHarness(t, forwarder{}, keys)
		h.run(stopOnQ{})
		typeKeys("qq")
		received(t, h.ctx.Done())
		h.program.Quit()
		r := h.wait(t)
		if r.err != nil || !r.model.(stopOnQ).stopping {
			t.Fatalf("Run = %v, stopping %v", r.err, r.model.(stopOnQ).stopping)
		}
		select {
		case code := <-h.exits:
			t.Fatalf("exit(%d) after Stop", code)
		default:
		}
	})
}

// stopOnQ stops listening on q, as full-screen listen does.
type stopOnQ struct{ stopping bool }

func (m stopOnQ) Init() tea.Cmd { return nil }

func (m stopOnQ) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == "q" {
			return m, Stop
		}
	case stoppingMsg:
		m.stopping = true
	}
	return m, nil
}

func (m stopOnQ) View() tea.View { return tea.NewView("") }

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
		listened <- ws.New("ws"+strings.TrimPrefix(server.URL, "http"), "key", "project:proj_1", nil).Listen(h.ctx, h.session.Handle)
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
		received(t, called)
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
		received(t, called)

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
