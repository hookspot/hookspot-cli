package tui

import (
	"errors"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"hookspot/internal/session"
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

func TestFilterMatches(t *testing.T) {
	push := entry(4, "src_github", 404)
	push.Delivery.Body = []byte(`{"action":"push"}`)
	printed := printedOnly(5, "src_stripe")
	redirected := entry(6, "src_stripe", 302)
	redirected.Delivery.Path = "/hooks/v2"
	entries := []session.Entry{entry(1, "src_stripe", 200), entry(2, "src_github", 500), refused(3), push, printed, redirected}
	listen := screen().Listen
	// A source name may hold spaces, which a filter word can't.
	listen.Sources["src_stripe"] = "Stripe Prod"

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
		{filter: "source:prod", want: nil},
		{filter: "path:/hooks/", want: []int{6}},
		{filter: "path:hooks", want: nil},
		// Free text looks in the path and the summary, ignoring case.
		{filter: "HOOKS", want: []int{1, 3, 5, 6}},
		{filter: "Push", want: []int{4}},
		{filter: "invoice path:/hooks status:2xx", want: []int{1}},
		// Unknown keys and empty values are plain text.
		{filter: "type:push", want: nil},
		{filter: "status:", want: nil},
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
	githubOnly := append(append([]tea.Msg{letter('/')}, typed("source:github")...), enter)
	tests := []struct {
		name   string
		events func(r *tally) []tea.Msg
	}{
		{name: "typing", events: func(r *tally) []tea.Msg {
			msgs := []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), letter('/')}
			// A paste is typed on the filter line as one.
			msgs = append(msgs, tea.PasteMsg{Content: "path:/hooks\n"})
			return append(append(msgs, typed("invo")...), backspace)
		}},
		{name: "applied", events: func(r *tally) []tea.Msg {
			msgs := []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), r.recorded(refused(3)), letter('/')}
			msgs = append(append(msgs, typed("status:error")...), enter)
			// Later requests join when they match; evicted ones leave.
			return append(msgs, r.recorded(entry(4, "src_github", 503)), r.recorded(entry(5, "src_stripe", 200), 1, 2))
		}},
		{name: "no match", events: func(r *tally) []tea.Msg {
			msgs := []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), letter('/')}
			return append(append(msgs, typed("source:shopify")...), enter)
		}},
		{name: "reopened", events: func(r *tally) []tea.Msg {
			msgs := append([]tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500))}, githubOnly...)
			return append(msgs, letter('/'))
		}},
		{name: "emptied", events: func(r *tally) []tea.Msg {
			msgs := append([]tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500))}, githubOnly...)
			msgs = append(append(msgs, letter('/')), slices.Repeat([]tea.Msg{backspace}, len("source:github"))...)
			return append(msgs, enter)
		}},
		{name: "esc clears", events: func(r *tally) []tea.Msg {
			// esc ends typing, and then clears the applied filter.
			msgs := []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), letter('/'), letter('x'), esc}
			return append(append(msgs, githubOnly...), esc)
		}},
		{name: "hidden selection", events: func(r *tally) []tea.Msg {
			// Paused on #1, which the filter hides, so #2 is selected.
			msgs := []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), r.recorded(entry(3, "src_stripe", 200)), up, up}
			return append(msgs, githubOnly...)
		}},
		{name: "hidden requests evicted", events: func(r *tally) []tea.Msg {
			msgs := []tea.Msg{session.Ready{}}
			for n := 1; n <= 14; n++ {
				status := http.StatusOK
				if n%2 == 0 {
					status = http.StatusInternalServerError
				}
				msgs = append(msgs, r.recorded(entry(n, "src_stripe", status)))
			}
			msgs = append(append(append(msgs, letter('/')), typed("status:error")...), enter, up)
			// The paused list stays put as a request it hides leaves.
			return append(msgs, r.recorded(entry(15, "src_stripe", 200), 1))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tm := teatest.NewTestModel(t, screen(), teatest.WithInitialTermSize(80, 24))
			for _, msg := range test.events(&tally{}) {
				tm.Send(msg)
			}
			golden.RequireEqual(t, final(t, tm))
		})
	}
}

// waitingScreen lists a refused request, #3, to a target at address, and
// waits on it.
func waitingScreen(t *testing.T, address string, requests Requests) (tea.Model, tea.Cmd) {
	t.Helper()
	r := &tally{}
	m := screen()
	m.Requests = requests
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
	// The address of a server that isn't running yet. Another process could
	// take the port before the server does, which is unlikely enough here.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()

	// The replay's events reach the model before Replay returns, as through
	// the program.
	var events []tea.Msg
	replays := &replayer{tally: &tally{}, replayed: make(chan int, 1), send: func(msg tea.Msg) { events = append(events, msg) }}
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
	if view := ansi.Strip(model.View().Content); cmd == nil || !strings.Contains(view, "↻ "+address+" answered, replaying #3…") {
		t.Fatalf("the answer started no replay:\n%s", view)
	}
	msg = cmd()
	if n := receive(t, replays.replayed); n != 3 {
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

func TestFullscreenWaitShowsAFailedReplay(t *testing.T) {
	requests := newFake()
	requests.err = errors.New("#3: request dropped from history\x1b")
	model, _ := waitingScreen(t, "", requests)
	model = press(model, dialedMsg{id: model.(Fullscreen).wait.id})
	if view := ansi.Strip(model.View().Content); !strings.Contains(view, `✗ #3: request dropped from history\x1b`) {
		t.Fatalf("no failed replay in\n%s", view)
	}
}

func TestFullscreenWaitRefuses(t *testing.T) {
	for _, test := range []struct {
		name    string
		inspect bool
		keys    []tea.Msg
		want    string
	}{
		{name: "in inspect mode", inspect: true, want: "nothing to replay without --forward-to"},
		{name: "a request that got a response", keys: []tea.Msg{up}, want: "#2 got a response; r replays it now"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := &tally{}
			m := screen()
			if test.inspect {
				m.Target = ""
			}
			model := tea.Model(m)
			for _, msg := range append([]tea.Msg{r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), r.recorded(refused(3))}, test.keys...) {
				model, _ = model.Update(msg)
			}
			model, _ = model.Update(letter('w'))
			if m := model.(Fullscreen); m.toast != test.want || m.waiting() {
				t.Fatalf("w toasts %q, waiting %t; want %q", m.toast, m.waiting(), test.want)
			}
		})
	}
}

func TestTargetAddress(t *testing.T) {
	for target, want := range map[string]string{
		"http://localhost:3000": "localhost:3000",
		"http://app.test":       "app.test:80",
		"https://app.test":      "app.test:443",
	} {
		if got := (Fullscreen{Target: target}).targetAddress(); got != want {
			t.Errorf("targetAddress of %s = %s, want %s", target, got, want)
		}
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
	for _, msg := range append(append([]tea.Msg{letter('/')}, typed("status:error")...), enter, esc) {
		model, _ = model.Update(msg)
	}
	// esc stops the wait and keeps the filter.
	if view := ansi.Strip(model.View().Content); !strings.Contains(view, "w  wait for localhost:3000, then replay") || !strings.Contains(view, "/ status:error") {
		t.Fatalf("esc kept waiting or cleared the filter:\n%s", view)
	}
	// The stopped wait's dial answers too late to replay.
	if _, cmd = model.Update(dialedMsg{id: id}); cmd != nil {
		t.Fatal("a stopped wait replayed")
	}
}
