package tui

import (
	"bytes"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"hookspot/internal/api"
	"hookspot/internal/cards"
	"hookspot/internal/proxy"
	"hookspot/internal/session"
	"hookspot/internal/ws"
)

var clock = time.Date(2026, 10, 1, 14, 2, 0, 0, time.UTC)

// screen is a full-screen view of two sources forwarding to localhost:3000,
// with a fixed clock and toasts that outlast the test.
func screen() Fullscreen {
	return Fullscreen{
		Listen: cards.Listen{
			Sources: map[string]string{"src_stripe": "stripe", "src_github": "github"},
			Limits:  cards.Limits{MaxBodyLines: 12, MaxHeaders: 20, MaxValueChars: 160},
		},
		Project: "Acme | Payments",
		Routes: []cards.BannerRoute{
			{SourceUID: "src_stripe", Source: "stripe", PublicURL: "https://in.hookspot.test/src_stripe", RouteUID: "rte_stripe", Destination: "http://localhost:3000/hooks", Label: "payments"},
			{SourceUID: "src_github", Source: "github", PublicURL: "https://in.hookspot.test/src_github", RouteUID: "rte_github", Destination: "http://localhost:3000/github", Label: "/github"},
		},
		Target:   "http://localhost:3000",
		now:      func() time.Time { return clock },
		toastFor: time.Hour,
	}
}

// entry is request n from source, src_stripe or src_github, answered with
// status in 7n ms.
func entry(n int, source string, status int) session.Entry {
	path, route := "/hooks", "rte_stripe"
	if source == "src_github" {
		path, route = "/github", "rte_github"
	}
	number := strconv.Itoa(n)
	return session.Entry{
		Number: n,
		Delivery: ws.Delivery{
			AttemptUID: "att_" + number, RequestUID: "req_" + number, SourceUID: source, Method: "POST", Path: path,
			Headers: http.Header{"Content-Type": {"application/json"}, "Authorization": {"Bearer secret"}},
			Body:    []byte(`{"type":"invoice.paid","id":` + number + `}`),
		},
		RouteUID: route,
		Target:   "http://localhost:3000" + path,
		Response: ws.Response{Status: status, Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"ok":` + strconv.FormatBool(status < 300) + `}`)},
		Latency:  time.Duration(7*n) * time.Millisecond,
		Received: clock.Add(time.Duration(n) * time.Second),
	}
}

// refused is entry n to stripe, which found nothing listening.
func refused(n int) session.Entry {
	e := entry(n, "src_stripe", 0)
	e.Response = ws.Response{}
	e.Failure = &proxy.TransportFailure{Kind: proxy.TransportConnectionRefused}
	return e
}

// requests reports entries as a session would, counting deliveries in the
// totals, and forwarded ones as ok or failed.
type requests struct {
	totals session.Stats
}

func (r *requests) recorded(e session.Entry, evicted ...int) session.Recorded {
	if e.ReplayOf == 0 {
		r.totals.Count++
	}
	if e.ReplayOf == 0 && e.Target != "" {
		if e.Failure == nil && e.Response.Status < 300 {
			r.totals.OK++
		} else {
			r.totals.Failed++
		}
		r.totals.P50, r.totals.P95, r.totals.Max = 14*time.Millisecond, 21*time.Millisecond, 21*time.Millisecond
	}
	return session.Recorded{Entry: e, Route: r.totals, Totals: r.totals, Evicted: evicted}
}

var (
	up    = tea.KeyPressMsg{Code: tea.KeyUp}
	down  = tea.KeyPressMsg{Code: tea.KeyDown}
	left  = tea.KeyPressMsg{Code: tea.KeyLeft}
	right = tea.KeyPressMsg{Code: tea.KeyRight}
)

func letter(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

// final quits tm and returns its last view without color.
func final(t *testing.T, tm *teatest.TestModel) string {
	t.Helper()
	if err := tm.Quit(); err != nil {
		t.Fatal(err)
	}
	return ansi.Strip(tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).View().Content)
}

func TestFullscreen(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		inspect       bool
		events        func(r *requests) []tea.Msg
	}{
		{name: "empty", width: 80, height: 24, events: func(*requests) []tea.Msg {
			return []tea.Msg{
				session.DisabledSource{Name: "github"},
				session.Ready{},
				session.TestHint{Sources: []api.Source{{Name: "stripe", URL: "https://in.hookspot.test/src_stripe"}, {Name: "github", URL: "https://in.hookspot.test/src_github"}}},
			}
		}},
		{name: "arrival while following", width: 140, height: 24, events: func(r *requests) []tea.Msg {
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), r.recorded(refused(3))}
		}},
		{name: "arrival while paused", width: 80, height: 24, events: func(r *requests) []tea.Msg {
			return []tea.Msg{
				session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), r.recorded(entry(3, "src_stripe", 200)),
				up, r.recorded(entry(4, "src_stripe", 200)), r.recorded(entry(5, "src_github", 200)),
			}
		}},
		{name: "follow resumes", width: 80, height: 24, events: func(r *requests) []tea.Msg {
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_stripe", 200)), up, letter('f'), r.recorded(entry(3, "src_stripe", 200))}
		}},
		{name: "request tab", width: 80, height: 30, events: func(r *requests) []tea.Msg {
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_github", 500)), right}
		}},
		{name: "response tab", width: 80, height: 30, events: func(r *requests) []tea.Msg {
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_github", 500)), right, right}
		}},
		{name: "timing tab", width: 80, height: 30, events: func(r *requests) []tea.Msg {
			// ← wraps around from Overview.
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_github", 500)), left}
		}},
		{name: "replay overview", width: 80, height: 30, events: func(r *requests) []tea.Msg {
			replay := entry(2, "src_github", 200)
			replay.Delivery = entry(1, "src_github", 500).Delivery
			replay.ReplayOf = 1
			replay.Replay = &session.Comparison{Original: 1, Status: 500, Latency: 7 * time.Millisecond, Removed: []string{`  "ok": false`}, Added: []string{`  "ok": true`}}
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_github", 500)), r.recorded(replay)}
		}},
		{name: "narrow", width: 50, height: 16, events: func(r *requests) []tea.Msg {
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), r.recorded(refused(3))}
		}},
		{name: "evicted", width: 80, height: 24, events: func(r *requests) []tea.Msg {
			return []tea.Msg{
				session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_stripe", 200)), r.recorded(entry(3, "src_github", 200)),
				r.recorded(entry(4, "src_stripe", 200), 1, 2),
			}
		}},
		{name: "help", width: 80, height: 24, events: func(r *requests) []tea.Msg {
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), letter('?')}
		}},
		{name: "inspect", width: 80, height: 24, inspect: true, events: func(r *requests) []tea.Msg {
			e := entry(1, "src_stripe", 0)
			e.Target, e.Response, e.Latency = "", ws.Response{}, 0
			// Nothing replays without --forward-to.
			return []tea.Msg{session.Ready{}, r.recorded(e), letter('r')}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := screen()
			if test.inspect {
				m.Target = ""
				for i := range m.Routes {
					m.Routes[i].Destination = ""
				}
			}
			tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(test.width, test.height))
			for _, msg := range test.events(&requests{}) {
				tm.Send(msg)
			}
			golden.RequireEqual(t, final(t, tm))
		})
	}
}

func TestFullscreenKeepsOnlyHistorysEntries(t *testing.T) {
	r := &requests{}
	model := tea.Model(screen())
	for n := 1; n <= 3; n++ {
		model, _ = model.Update(r.recorded(entry(n, "src_stripe", 200)))
	}
	// Paused on #1, which history then drops with #2.
	model, _ = model.Update(up)
	model, _ = model.Update(up)
	model, _ = model.Update(r.recorded(entry(4, "src_stripe", 200), 1, 2))
	m := model.(Fullscreen)
	if len(m.entries) != 2 || m.entries[0].Number != 3 {
		t.Fatalf("entries = %d from #%d, want #3 and #4", len(m.entries), m.entries[0].Number)
	}
	if i := m.selectedIndex(); m.entries[i].Number != 3 {
		t.Fatalf("selected #%d, want the oldest left, #3", m.entries[i].Number)
	}
}

// replayer replays #n as entry 4, as a session would, and hands over n.
type replayer struct {
	send     func(tea.Msg)
	requests *requests
	replayed chan int
}

func (r *replayer) Replay(n int) error {
	replay := entry(4, "src_github", 200)
	replay.ReplayOf = n
	replay.Replay = &session.Comparison{Original: n, Status: 500, Latency: 14 * time.Millisecond}
	r.send(r.requests.recorded(replay))
	r.replayed <- n
	return nil
}

func (*replayer) ReplayLast() error { return errors.New("full-screen replays the selection") }

func TestFullscreenReplayAddsAMarkedRow(t *testing.T) {
	r := &requests{}
	replays := &replayer{requests: r, replayed: make(chan int, 1)}
	m := screen()
	m.Replayer = replays
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	replays.send = tm.Send
	for _, msg := range []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), r.recorded(entry(3, "src_stripe", 200)), up, letter('r')} {
		tm.Send(msg)
	}
	select {
	case n := <-replays.replayed:
		if n != 2 {
			t.Fatalf("replayed #%d, want the selected #2", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("r did not replay")
	}
	// The selection stays on #2; the toast names the replay.
	golden.RequireEqual(t, final(t, tm))
}

// exporter answers every request with curl and fixture, noting the number
// and redaction asked for.
type exporter struct {
	curl    session.Curl
	fixture session.Fixture
	asked   chan string
}

func (e exporter) Curl(n int, redact bool) (session.Curl, error) {
	e.asked <- "curl #" + strconv.Itoa(n) + " redact " + strconv.FormatBool(redact)
	return e.curl, nil
}

func (e exporter) ExportFixture(n int, redact bool) (session.Fixture, error) {
	e.asked <- "fixture #" + strconv.Itoa(n) + " redact " + strconv.FormatBool(redact)
	return e.fixture, nil
}

func TestFullscreenCopyToast(t *testing.T) {
	r := &requests{}
	command := "curl -X POST 'http://localhost:3000/hooks' \\\n  -H 'Authorization: Bearer secret'"
	m := screen()
	m.Exporter = exporter{curl: session.Curl{Command: command, Redacted: true}, asked: make(chan string, 1)}
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	for _, msg := range []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), letter('c')} {
		tm.Send(msg)
	}
	if asked := <-m.Exporter.(exporter).asked; asked != "curl #1 redact true" {
		t.Fatalf("asked for %s", asked)
	}
	// The full command goes to the clipboard as the toast shows.
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte(ansi.SetSystemClipboard(command)))
	}, teatest.WithDuration(5*time.Second))
	golden.RequireEqual(t, final(t, tm))
}

// tester sends nothing and notes the source asked for.
type tester struct{ asked *string }

func (t tester) SendTest(source string) (string, error) {
	*t.asked = source
	return source, nil
}

// press sends key to model, then runs the command it starts the way the
// program would.
func press(model tea.Model, key tea.KeyPressMsg) tea.Model {
	model, cmd := model.Update(key)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			model, _ = model.Update(msg)
		}
	}
	return model
}

func TestFullscreenCommands(t *testing.T) {
	r := &requests{}
	var asked string
	m := screen()
	m.Tester = tester{asked: &asked}
	m.Exporter = exporter{fixture: session.Fixture{JSON: "/work/hookspot-fixtures/req_2.json", Body: "/work/hookspot-fixtures/req_2.body", Redacted: true}, asked: make(chan string, 1)}
	model := tea.Model(m)

	model = press(model, letter('t'))
	if asked != "stripe" || model.(Fullscreen).toast != "test event sent to stripe" {
		t.Fatalf("t before any request tested %q, toast %q; want the first source", asked, model.(Fullscreen).toast)
	}

	model, _ = model.Update(r.recorded(entry(1, "src_stripe", 200)))
	model, _ = model.Update(r.recorded(entry(2, "src_github", 200)))
	model = press(model, letter('t'))
	if asked != "github" {
		t.Fatalf("t tested %q, want the selected request's source", asked)
	}

	model = press(model, letter('e'))
	if asked := <-m.Exporter.(exporter).asked; asked != "fixture #2 redact true" {
		t.Fatalf("asked for %s", asked)
	}
	if got, want := model.(Fullscreen).toast, "exported #2 to /work/hookspot-fixtures/req_2.json and /work/hookspot-fixtures/req_2.body · sensitive headers redacted"; got != want {
		t.Fatalf("toast = %q, want %q", got, want)
	}

	model, cmd := model.Update(toastExpiredMsg(model.(Fullscreen).toastID))
	if cmd != nil || model.(Fullscreen).toast != "" {
		t.Fatalf("toast %q outlived its time", model.(Fullscreen).toast)
	}
}

var timePattern = regexp.MustCompile(`\d\d:\d\d:\d\d(\.\d{3})?`)

// maskTimes hides the times a session takes from the clock, keeping widths.
func maskTimes(view string) string {
	return timePattern.ReplaceAllStringFunc(view, func(time string) string {
		return "hh:mm:ss.mmm"[:len(time)]
	})
}

func TestFullscreenQuitStopsListening(t *testing.T) {
	keys, typeKeys := keyboard(t)
	h := newHarness(t, nil, keys)
	h.session = session.New(h.ctx, sources, nil, h.program.FullscreenSink())
	m := Fullscreen{
		Replayer: h.session, Exporter: h.session, Tester: h.session,
		Listen:  cards.Listen{Sources: map[string]string{"src_stripe": "stripe"}},
		Project: "Acme | Payments",
		Routes:  []cards.BannerRoute{{SourceUID: "src_stripe", Source: "stripe", RouteUID: "rte_stripe", Label: "/hooks"}},
		now:     func() time.Time { return clock },
	}
	h.run(m)
	h.emit(t, session.Ready{})
	h.handle(t, delivery(1, "/hooks"))

	typeKeys("q")
	received(t, h.ctx.Done())
	// A second q isn't a second Ctrl-C.
	typeKeys("q")
	// listen has returned.
	h.program.Quit()
	r := h.wait(t)
	if r.err != nil {
		t.Fatal(r.err)
	}
	select {
	case code := <-h.exits:
		t.Fatalf("exit(%d) after q", code)
	default:
	}
	golden.RequireEqual(t, maskTimes(ansi.Strip(r.model.View().Content)))
}
