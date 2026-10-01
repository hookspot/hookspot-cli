package tui

import (
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"hookspot/internal/session"
	"hookspot/internal/ws"
)

var (
	enter     = tea.KeyPressMsg{Code: tea.KeyEnter}
	esc       = tea.KeyPressMsg{Code: tea.KeyEscape}
	backspace = tea.KeyPressMsg{Code: tea.KeyBackspace}
)

// typed is text as key presses.
func typed(text string) []tea.Msg {
	var keys []tea.Msg
	for _, r := range text {
		if r == ' ' {
			keys = append(keys, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
		} else {
			keys = append(keys, letter(r))
		}
	}
	return keys
}

func TestParseFilter(t *testing.T) {
	tests := []struct {
		text string
		want []term
	}{
		{text: "", want: nil},
		{text: "status:error", want: []term{{key: "status", value: "error"}}},
		{text: " source:stripe  path:/hooks invoice ", want: []term{{key: "source", value: "stripe"}, {key: "path", value: "/hooks"}, {value: "invoice"}}},
		// Unknown keys and empty values are plain text.
		{text: "type:invoice.paid status:", want: []term{{value: "type:invoice.paid"}, {value: "status:"}}},
	}
	for _, test := range tests {
		if got := parseFilter(test.text); !reflect.DeepEqual(got, test.want) {
			t.Errorf("parseFilter(%q) = %v, want %v", test.text, got, test.want)
		}
	}
}

func TestFilterMatches(t *testing.T) {
	push := entry(4, "src_github", 404)
	push.Delivery.Body = []byte(`{"action":"push"}`)
	printed := entry(5, "src_stripe", 0)
	printed.Target, printed.Response = "", ws.Response{}
	redirected := entry(6, "src_stripe", 302)
	redirected.Delivery.Path = "/hooks/v2"
	entries := []session.Entry{entry(1, "src_stripe", 200), entry(2, "src_github", 500), refused(3), push, printed, redirected}
	listen := screen().Listen

	tests := []struct {
		filter string
		want   []int
	}{
		// Errors are what the failed count counts: neither printed-only
		// requests nor 2xx.
		{filter: "status:error", want: []int{2, 3, 4, 6}},
		{filter: "status:5xx", want: []int{2}},
		{filter: "status:404", want: []int{4}},
		{filter: "status:2xx source:stripe", want: []int{1}},
		{filter: "status:error source:stripe", want: []int{3, 6}},
		{filter: "source:GitHub", want: []int{2, 4}},
		{filter: "path:/hooks/", want: []int{6}},
		{filter: "path:hooks", want: nil},
		// Free text looks in the path and the summary, ignoring case.
		{filter: "HOOKS", want: []int{1, 3, 5, 6}},
		{filter: "Push", want: []int{4}},
		{filter: "invoice path:/hooks status:2xx", want: []int{1}},
		{filter: "type:push", want: nil},
	}
	for _, test := range tests {
		var got []int
		for _, e := range entries {
			if matches(e, parseFilter(test.filter), listen) {
				got = append(got, e.Number)
			}
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("%q matches %v, want %v", test.filter, got, test.want)
		}
	}
}

func TestFullscreenFilter(t *testing.T) {
	tests := []struct {
		name   string
		events func(r *requests) []tea.Msg
	}{
		{name: "typing", events: func(r *requests) []tea.Msg {
			msgs := []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), letter('/')}
			return append(append(msgs, typed("path:/hooks invo")...), backspace)
		}},
		{name: "applied", events: func(r *requests) []tea.Msg {
			msgs := []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), r.recorded(refused(3)), letter('/')}
			msgs = append(append(msgs, typed("status:error")...), enter)
			// Later requests join when they match; evicted ones leave.
			return append(msgs, r.recorded(entry(4, "src_github", 503)), r.recorded(entry(5, "src_stripe", 200), 1, 2))
		}},
		{name: "no match", events: func(r *requests) []tea.Msg {
			msgs := []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), letter('/')}
			return append(append(msgs, typed("source:shopify")...), enter)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tm := teatest.NewTestModel(t, screen(), teatest.WithInitialTermSize(80, 24))
			for _, msg := range test.events(&requests{}) {
				tm.Send(msg)
			}
			golden.RequireEqual(t, final(t, tm))
		})
	}
}

func TestFullscreenFilterEscClears(t *testing.T) {
	r := &requests{}
	model := tea.Model(screen())
	for _, msg := range []tea.Msg{r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), letter('/'), letter('x'), esc} {
		model, _ = model.Update(msg)
	}
	if f := model.(Fullscreen).filter; f.editing || f.text != "" {
		t.Fatalf("esc while typing left %+v", f)
	}

	for _, msg := range append(append([]tea.Msg{letter('/')}, typed("source:github")...), enter) {
		model, _ = model.Update(msg)
	}
	if shown := model.(Fullscreen).shown(); len(shown) != 1 {
		t.Fatalf("source:github shows %d requests, want 1", len(shown))
	}
	model, _ = model.Update(esc)
	if shown := model.(Fullscreen).shown(); len(shown) != 2 {
		t.Fatalf("esc left %d requests shown, want both", len(shown))
	}
}

// waitingScreen lists a refused request, #3, to a target at address, and
// waits on it.
func waitingScreen(t *testing.T, address string, replayer Replayer) (tea.Model, tea.Cmd) {
	t.Helper()
	r := &requests{}
	m := screen()
	m.Replayer = replayer
	m.dialEvery = 10 * time.Millisecond
	if address != "" {
		m.Target = "http://" + address
	}
	model := tea.Model(m)
	for _, msg := range []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), r.recorded(refused(3))} {
		model, _ = model.Update(msg)
	}
	return model.Update(letter('w'))
}

func TestFullscreenWaitReplaysWhenTargetAnswers(t *testing.T) {
	// The address of a server that isn't running yet.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()

	// The replay's events reach the model before Replay returns, as through
	// the program.
	var events []tea.Msg
	replays := &replayer{requests: &requests{}, replayed: make(chan int, 1), send: func(msg tea.Msg) { events = append(events, msg) }}
	model, cmd := waitingScreen(t, address, replays)

	msg := cmd()
	if dialed, ok := msg.(dialedMsg); !ok || dialed.err == nil {
		t.Fatalf("first dial got %#v, want refused", msg)
	}
	model, cmd = model.Update(msg)
	if view := ansi.Strip(model.View().Content); !strings.Contains(view, "waiting for "+address+" · checking every 10ms") {
		t.Fatalf("no waiting line in\n%s", view)
	}

	listener, err = net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	msg = cmd()
	if dialed, ok := msg.(dialedMsg); !ok || dialed.err != nil {
		t.Fatalf("dial after the server started got %#v", msg)
	}
	model, cmd = model.Update(msg)
	if cmd == nil {
		t.Fatal("the answer started no replay")
	}
	msg = cmd()
	if n := <-replays.replayed; n != 3 {
		t.Fatalf("replayed #%d, want the waited #3", n)
	}
	for _, event := range events {
		model, _ = model.Update(event)
	}
	model, _ = model.Update(msg)

	view := ansi.Strip(model.View().Content)
	if !strings.Contains(view, "↻ replayed as #4  200 OK  28ms") || strings.Contains(view, "waiting for") {
		t.Fatalf("the replay's outcome doesn't replace the waiting line in\n%s", view)
	}
}

func TestFullscreenEscStopsWaiting(t *testing.T) {
	// No dial runs, so nothing answers.
	model, cmd := waitingScreen(t, "", nil)
	if cmd == nil {
		t.Fatal("w started no dial")
	}
	golden.RequireEqual(t, ansi.Strip(model.View().Content))

	id := model.(Fullscreen).wait.id
	model, _ = model.Update(esc)
	if view := ansi.Strip(model.View().Content); !strings.Contains(view, "w  wait for localhost:3000, then replay") {
		t.Fatalf("esc kept waiting:\n%s", view)
	}
	// The stopped wait's dial answers too late to replay.
	if _, cmd = model.Update(dialedMsg{id: id}); cmd != nil {
		t.Fatal("a stopped wait replayed")
	}
}
