package tui

import (
	"bytes"
	"errors"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

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
			{SourceUID: "src_stripe", Source: "stripe", PublicURL: "https://in.hookspot.test/src_stripe", RouteUID: "rte_stripe", Path: "/hooks", Destination: "http://localhost:3000/hooks", Label: "payments"},
			{SourceUID: "src_github", Source: "github", PublicURL: "https://in.hookspot.test/src_github", RouteUID: "rte_github", Path: "/github", Destination: "http://localhost:3000/github", Label: "/github"},
		},
		Target:      "http://localhost:3000",
		RequestsURL: "https://hookspot.test/acme/payments/requests",
		now:         func() time.Time { return clock },
		toastFor:    time.Hour,
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

// printedOnly is request n from source without --forward-to.
func printedOnly(n int, source string) session.Entry {
	e := entry(n, source, 0)
	e.Target, e.Response, e.Latency = "", ws.Response{}, 0
	return e
}

// tally reports entries as a session would: each but a replay counts in the
// totals and its route's stats, all in the clock's minute, with p50 at half
// the slowest response and p95 a millisecond under it.
type tally struct {
	totals session.Stats
	routes map[string]session.Stats
}

func (t *tally) recorded(e session.Entry, evicted ...int) session.Recorded {
	if e.ReplayOf == 0 {
		t.totals = add(t.totals, e)
		if e.RouteUID != "" {
			if t.routes == nil {
				t.routes = map[string]session.Stats{}
			}
			t.routes[e.RouteUID] = add(t.routes[e.RouteUID], e)
		}
	}
	return session.Recorded{Entry: e, Route: t.routes[e.RouteUID], Totals: t.totals, Evicted: evicted}
}

func add(s session.Stats, e session.Entry) session.Stats {
	s.Count++
	s.Last = e
	s.PerMinute[14]++
	s.Minute = clock.Truncate(time.Minute)
	if e.Target == "" {
		return s
	}
	if e.Failed() {
		s.Failed++
	} else {
		s.OK++
	}
	o := session.Outcome{Status: e.Response.Status}
	if e.Failure != nil {
		o = session.Outcome{Failure: e.Failure.Kind}
	}
	// Earlier snapshots keep their own counts.
	s.Outcomes = maps.Clone(s.Outcomes)
	if s.Outcomes == nil {
		s.Outcomes = map[session.Outcome]int{}
	}
	s.Outcomes[o]++
	if e.Timed() {
		s.Max = max(s.Max, e.Latency)
		s.P50, s.P95 = s.Max/2, s.Max-time.Millisecond
	}
	return s
}

var (
	up       = tea.KeyPressMsg{Code: tea.KeyUp}
	down     = tea.KeyPressMsg{Code: tea.KeyDown}
	left     = tea.KeyPressMsg{Code: tea.KeyLeft}
	right    = tea.KeyPressMsg{Code: tea.KeyRight}
	pageDown = tea.KeyPressMsg{Code: tea.KeyPgDown}

	updateAvailable = session.UpdateAvailable{Latest: "1.3.0", Upgrade: "brew upgrade hookspot-cli"}
	deprecation     = "Hookspot CLI 1.1.0 stops working on December 1, 2026: upgrade to 1.2.0 or later."
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
		events        func(r *tally) []tea.Msg
	}{
		{name: "empty", width: 80, height: 24, events: func(*tally) []tea.Msg {
			return []tea.Msg{
				session.DisabledSource{Name: "github"},
				session.Ready{},
				session.TestHint{Sources: []api.Source{{Name: "stripe", URL: "https://in.hookspot.test/src_stripe"}, {Name: "github", URL: "https://in.hookspot.test/src_github"}}},
			}
		}},
		{name: "arrival while following", width: 140, height: 24, events: func(r *tally) []tea.Msg {
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), r.recorded(refused(3))}
		}},
		{name: "arrival while paused", width: 80, height: 24, events: func(r *tally) []tea.Msg {
			// ↓ on the newest row pauses there.
			return []tea.Msg{
				session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), r.recorded(entry(3, "src_stripe", 200)),
				down, r.recorded(entry(4, "src_stripe", 200)), r.recorded(entry(5, "src_github", 200)),
			}
		}},
		{name: "follow resumes", width: 80, height: 24, events: func(r *tally) []tea.Msg {
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_stripe", 200)), up, letter('f'), r.recorded(entry(3, "src_stripe", 200))}
		}},
		{name: "request tab", width: 80, height: 30, events: func(r *tally) []tea.Msg {
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_github", 500)), right}
		}},
		{name: "response tab", width: 80, height: 30, events: func(r *tally) []tea.Msg {
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_github", 500)), right, right}
		}},
		{name: "response scrolled", width: 80, height: 24, events: func(r *tally) []tea.Msg {
			e := entry(1, "src_stripe", 500)
			for _, name := range []string{"Cache-Control", "Date", "Etag", "Server", "Vary", "X-Request-Id", "X-Runtime"} {
				e.Response.Headers.Set(name, "value")
			}
			// Scrolling pauses on the request, and the body comes into view.
			return []tea.Msg{session.Ready{}, r.recorded(e), right, right, pageDown, pageDown}
		}},
		{name: "timing tab", width: 80, height: 30, events: func(r *tally) []tea.Msg {
			// ← wraps around from Overview.
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_github", 500)), left}
		}},
		{name: "timeout timing", width: 80, height: 30, events: func(r *tally) []tea.Msg {
			timeout := refused(2)
			timeout.Failure.Kind = proxy.TransportTimeout
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(timeout), left}
		}},
		{name: "replay overview", width: 80, height: 30, events: func(r *tally) []tea.Msg {
			replay := entry(2, "src_github", 200)
			replay.Delivery = entry(1, "src_github", 500).Delivery
			replay.ReplayOf = 1
			replay.Replay = &session.Comparison{Original: 1, Status: 500, Latency: 7 * time.Millisecond, Removed: []string{`  "ok": false`}, Added: []string{`  "ok": true`}}
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_github", 500)), r.recorded(replay)}
		}},
		{name: "redirect overview", width: 80, height: 30, events: func(r *tally) []tea.Msg {
			redirected := entry(1, "src_stripe", 302)
			redirected.Response.Headers.Set("Location", "https://localhost:3000/hooks")
			return []tea.Msg{session.Ready{}, r.recorded(redirected)}
		}},
		{name: "test event overview", width: 80, height: 30, events: func(r *tally) []tea.Msg {
			test := entry(1, "src_stripe", 200)
			test.Test = true
			return []tea.Msg{session.Ready{}, r.recorded(test)}
		}},
		{name: "narrow", width: 50, height: 16, events: func(r *tally) []tea.Msg {
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), r.recorded(refused(3))}
		}},
		{name: "evicted", width: 80, height: 24, events: func(r *tally) []tea.Msg {
			return []tea.Msg{
				session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_stripe", 200)), r.recorded(entry(3, "src_github", 200)),
				r.recorded(entry(4, "src_stripe", 200), 1, 2),
			}
		}},
		{name: "help", width: 80, height: 24, events: func(r *tally) []tea.Msg {
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), letter('?')}
		}},
		{name: "inspect", width: 80, height: 24, inspect: true, events: func(r *tally) []tea.Msg {
			// Nothing replays without --forward-to.
			return []tea.Msg{session.Ready{}, r.recorded(printedOnly(1, "src_stripe")), letter('r')}
		}},
		{name: "offline", width: 80, height: 24, events: func(r *tally) []tea.Msg {
			return []tea.Msg{
				session.Ready{}, r.recorded(entry(1, "src_stripe", 404)),
				session.RootNotFound{Root: "http://localhost:3000/", Status: 404},
				session.ConnectionLost{Err: errors.New("dial tcp: connection refused"), RetryIn: 2 * time.Second},
			}
		}},
		{name: "reconnected", width: 80, height: 24, events: func(r *tally) []tea.Msg {
			lost := session.ConnectionLost{Err: errors.New("dial tcp: connection refused"), RetryIn: 2 * time.Second}
			replay := entry(2, "src_stripe", 200)
			replay.ReplayOf = 1
			replay.Replay = &session.Comparison{Original: 1, Status: 200, Latency: 7 * time.Millisecond}
			// The notice sums both outages, and the replay's toast leaves it.
			return []tea.Msg{
				session.Ready{}, r.recorded(entry(1, "src_stripe", 200)),
				lost, session.Reconnected{Offline: 3 * time.Second},
				lost, session.Reconnected{Offline: 2 * time.Second},
				r.recorded(replay),
			}
		}},
		{name: "update narrow", width: 26, height: 16, events: func(r *tally) []tea.Msg {
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), updateAvailable}
		}},
		{name: "update help", width: 80, height: 24, events: func(r *tally) []tea.Msg {
			// The help keeps every key, above the alert.
			return []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), updateAvailable, letter('?')}
		}},
		{name: "notice", width: 80, height: 24, events: func(r *tally) []tea.Msg {
			// The notice, over the update alert, is cut to fit.
			return []tea.Msg{session.Ready{Notice: deprecation}, r.recorded(entry(1, "src_stripe", 200)), updateAvailable}
		}},
		{name: "esc dismisses alerts", width: 80, height: 24, events: func(r *tally) []tea.Msg {
			msgs := []tea.Msg{
				session.Ready{}, r.recorded(entry(1, "src_stripe", 404)), r.recorded(entry(2, "src_github", 200)),
				session.RootNotFound{Root: "http://localhost:3000/", Status: 404},
				session.ConnectionLost{Err: errors.New("dial tcp: connection refused"), RetryIn: 2 * time.Second},
				session.Reconnected{Offline: 3 * time.Second},
				letter('/'),
			}
			// esc dismisses the alerts first, keeping the filter.
			return append(append(msgs, typed("source:github")...), enter, esc)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := screen()
			if test.inspect {
				m = inspected(m)
			}
			tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(test.width, test.height))
			for _, msg := range test.events(&tally{}) {
				tm.Send(msg)
			}
			golden.RequireEqual(t, final(t, tm))
		})
	}
}

func TestFullscreenKeepsOnlyHistorysEntries(t *testing.T) {
	r := &tally{}
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
	Requests
	send     func(tea.Msg)
	tally    *tally
	replayed chan int
}

func (r *replayer) Replay(n int) error {
	replay := entry(4, "src_github", 200)
	replay.ReplayOf = n
	replay.Replay = &session.Comparison{Original: n, Status: 500, Latency: 14 * time.Millisecond}
	r.send(r.tally.recorded(replay))
	r.replayed <- n
	return nil
}

func TestFullscreenReplayAddsAMarkedRow(t *testing.T) {
	r := &tally{}
	replays := &replayer{tally: r, replayed: make(chan int, 1)}
	m := screen()
	m.Requests = replays
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	replays.send = tm.Send
	for _, msg := range []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), r.recorded(entry(3, "src_stripe", 200)), up, letter('r')} {
		tm.Send(msg)
	}
	if n := receive(t, replays.replayed); n != 2 {
		t.Fatalf("replayed #%d, want the selected #2", n)
	}
	// The selection stays on #2; the toast names the replay.
	golden.RequireEqual(t, final(t, tm))
}

// fake answers commands as a session would, with err, noting each one it's
// asked in asked. A test event goes to the source asked for.
type fake struct {
	asked   chan string
	curl    session.Curl
	fixture session.Fixture
	err     error
}

func newFake() fake { return fake{asked: make(chan string, 8)} }

func (f fake) Replay(n int) error {
	f.asked <- "replay #" + strconv.Itoa(n)
	return f.err
}

func (f fake) ReplayLast() error {
	f.asked <- "replay last"
	return f.err
}

func (f fake) Curl(n int, redact bool) (session.Curl, error) {
	f.asked <- "curl #" + strconv.Itoa(n) + " redact " + strconv.FormatBool(redact)
	return f.curl, f.err
}

func (f fake) ExportFixture(n int, redact bool) (session.Fixture, error) {
	f.asked <- "fixture #" + strconv.Itoa(n) + " redact " + strconv.FormatBool(redact)
	return f.fixture, f.err
}

func (f fake) SendTest(source string) (string, error) {
	f.asked <- "test " + source
	return source, f.err
}

func TestFullscreenCopyToast(t *testing.T) {
	r := &tally{}
	command := "curl -X POST 'http://localhost:3000/hooks' \\\n  -H 'Authorization: Bearer secret'"
	requests := newFake()
	requests.curl = session.Curl{Command: command, Redacted: true}
	m := screen()
	m.Requests = requests
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	for _, msg := range []tea.Msg{session.Ready{}, r.recorded(entry(1, "src_stripe", 200)), letter('c')} {
		tm.Send(msg)
	}
	if asked := receive(t, requests.asked); asked != "curl #1 redact true" {
		t.Fatalf("asked for %s", asked)
	}
	// The full command goes to the clipboard as the toast shows.
	waitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte(ansi.SetSystemClipboard(command)))
	})
	golden.RequireEqual(t, final(t, tm))
}

// press sends key to model, then runs the command it starts the way the
// program would.
func press(model tea.Model, key tea.Msg) tea.Model {
	model, cmd := model.Update(key)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			model, _ = model.Update(msg)
		}
	}
	return model
}

func TestFullscreenCommands(t *testing.T) {
	r := &tally{}
	requests := newFake()
	requests.fixture = session.Fixture{JSON: "/work/hookspot-fixtures/req_2.json", Body: "/work/hookspot-fixtures/req_2.body", Redacted: true}
	m := screen()
	m.Requests = requests
	model := tea.Model(m)

	model = press(model, letter('t'))
	if asked := receive(t, requests.asked); asked != "test stripe" || model.(Fullscreen).toast != "test event sent to stripe" {
		t.Fatalf("t before any request asked for %s, toast %q; want the first source", asked, model.(Fullscreen).toast)
	}

	model, _ = model.Update(r.recorded(entry(1, "src_stripe", 200)))
	model, _ = model.Update(r.recorded(entry(2, "src_github", 200)))
	model = press(model, letter('t'))
	if asked := receive(t, requests.asked); asked != "test github" {
		t.Fatalf("t asked for %s, want the selected request's source", asked)
	}

	model = press(model, letter('e'))
	if asked := receive(t, requests.asked); asked != "fixture #2 redact true" {
		t.Fatalf("asked for %s", asked)
	}
	if got, want := model.(Fullscreen).toast, "exported #2 to /work/hookspot-fixtures/req_2.json and /work/hookspot-fixtures/req_2.body · sensitive headers redacted"; got != want {
		t.Fatalf("toast = %q, want %q", got, want)
	}

	// An earlier toast's time running out leaves a later one.
	id := model.(Fullscreen).toastID
	if model, _ = model.Update(toastExpiredMsg(id - 1)); model.(Fullscreen).toast == "" {
		t.Fatal("an earlier toast's expiry ended the newest")
	}
	model, cmd := model.Update(toastExpiredMsg(id))
	if cmd != nil || model.(Fullscreen).toast != "" {
		t.Fatalf("toast %q outlived its time", model.(Fullscreen).toast)
	}

	m = model.(Fullscreen)
	m.ShowSensitiveHeaders = true
	model = press(press(m, letter('c')), letter('e'))
	if asked := []string{receive(t, requests.asked), receive(t, requests.asked)}; !slices.Equal(asked, []string{"curl #2 redact false", "fixture #2 redact false"}) {
		t.Fatalf("with --show-sensitive-headers asked for %v", asked)
	}
}

// hostile is text a server or a delivery may carry to break a screen:
// escape sequences, C0 and C1 controls, invalid UTF-8, and wide and combining
// characters.
const hostile = "\x1b]0;title\x07\x1b[2J\u009b31m\r\n\t\x00\x7f\xff界🙂e\u0301"

// hostileEntries carry hostile text in every field, binary bodies and bodies
// of a megabyte or more.
func hostileEntries() []session.Entry {
	failed := entry(1, "src_stripe", http.StatusInternalServerError)
	failed.Delivery.Method, failed.Delivery.Path, failed.Delivery.Query, failed.Delivery.RequestUID = hostile, "/"+hostile, hostile+"="+hostile, hostile
	failed.Delivery.Headers = http.Header{"Content-Type": {"application/json"}, hostile: {hostile}}
	failed.Delivery.Body = []byte(`[` + strings.Repeat(`{"type":"\u001b[2J\u0007\u009b31m","界🙂":"`+strings.Repeat("x", 300)+`"},`, 1<<12) + `{}]`)
	failed.Response = ws.Response{Status: http.StatusInternalServerError, Headers: http.Header{hostile: {hostile}}, Body: []byte(strings.Repeat(hostile+"\n", 1<<15))}

	binary := entry(2, "src_github", http.StatusFound)
	binary.Delivery.Headers = http.Header{"Content-Type": {"application/octet-stream"}}
	binary.Delivery.Body = bytes.Repeat([]byte{0x00, 0x1b, 0x9b, 0xff, '\n', '\r', 0x07}, 1<<18)
	binary.Response = ws.Response{Status: http.StatusFound, Headers: http.Header{"Location": {hostile}}, Body: binary.Delivery.Body}

	unreachable := refused(3)
	unreachable.Failure = &proxy.TransportFailure{Kind: proxy.TransportOther, Err: errors.New(hostile)}

	replay := failed
	replay.Number, replay.ReplayOf = 4, 1
	replay.Replay = &session.Comparison{Original: 1, Status: http.StatusInternalServerError, Removed: []string{hostile}, Added: []string{strings.Repeat(hostile, 100)}, More: 3}

	test := entry(5, "src_stripe", http.StatusOK)
	test.Test = true
	test.Delivery.Body = []byte(strings.Repeat(hostile, 1<<16))

	unmatched := entry(6, "src_"+hostile, http.StatusOK)
	unmatched.RouteUID = ""
	unmatched.Delivery.Method = strings.Repeat("PROPFIND", 100)

	printed := printedOnly(7, "src_github")
	printed.Delivery.Query = strings.Repeat(hostile, 100)
	printed.Delivery.Headers = http.Header{strings.Repeat("X-"+hostile, 100): {strings.Repeat(hostile, 100)}}
	return []session.Entry{failed, binary, unreachable, replay, test, unmatched, printed}
}

var styling = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// requireContained fails unless text has nothing but styling and printable
// characters, and fits in width columns and, when height is set, height
// lines.
func requireContained(t *testing.T, name, text string, width, height int) {
	t.Helper()
	lines := strings.Split(styling.ReplaceAllString(text, ""), "\n")
	if height > 0 && len(lines) > height {
		t.Errorf("%s: %d lines, more than %d", name, len(lines), height)
	}
	for i, line := range lines {
		if strings.IndexFunc(line, unicode.IsControl) >= 0 {
			t.Errorf("%s: line %d has a control character: %q", name, i, line)
		}
		if w := ansi.StringWidth(line); w > width {
			t.Errorf("%s: line %d is %d columns, more than %d: %q", name, i, w, width, line)
		}
	}
}

func TestHostileRequestsKeepTheLayout(t *testing.T) {
	entries := hostileEntries()
	for _, width := range []int{40, 80, cards.DefaultWidth, splitWidth} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			m := sourcesScreen()
			m.Project = hostile
			m.Listen.Sources["src_stripe"] = hostile
			m.Routes[0] = cards.BannerRoute{SourceUID: "src_stripe", Source: hostile, PublicURL: "https://in.hookspot.test/" + hostile, RouteUID: "rte_stripe", Path: "/" + hostile, Destination: "http://localhost:3000/" + hostile, Label: hostile}
			full := tea.Model(m)
			requireFull := func(name string) {
				t.Helper()
				requireContained(t, name, full.View().Content, width, 24)
			}
			stream := tea.Model(Stream{Project: hostile, Forwarding: true, Prompt: true})
			for _, msg := range []tea.Msg{
				tea.WindowSizeMsg{Width: width, Height: 24},
				session.DisabledSource{Name: hostile}, session.SkippedSource{Name: hostile}, session.Ready{},
				session.TestHint{Sources: []api.Source{{Name: hostile, URL: "https://in.hookspot.test/" + hostile}, {Name: "github"}}},
				session.ConnectionLost{Err: errors.New(hostile)},
			} {
				full, _ = full.Update(msg)
				stream, _ = stream.Update(msg)
			}
			requireFull("empty")
			full, _ = full.Update(session.RootNotFound{Root: hostile, Status: http.StatusNotFound})
			requireFull("root hint")

			r := &tally{}
			for _, e := range entries {
				recorded := r.recorded(e)
				full, _ = full.Update(recorded)
				stream, _ = stream.Update(recorded)
				// Narrower streams keep the rows' minimum columns and wrap.
				if width >= 80 {
					requireContained(t, "card #"+strconv.Itoa(e.Number), m.Listen.Entry(e, width), width, 0)
					requireContained(t, "unlimited card #"+strconv.Itoa(e.Number), cards.Listen{Sources: m.Listen.Sources}.Entry(e, width), width, 0)
				}
			}
			requireContained(t, "status and prompt", stream.View().Content, width, 0)

			for range entries {
				for tab := range tabCount {
					name := "#" + strconv.Itoa(full.(Fullscreen).entries[full.(Fullscreen).selectedIndex()].Number) + " " + tabTitles[tab]
					requireFull(name)
					full, _ = full.Update(pageDown)
					requireFull(name + " scrolled")
					full, _ = full.Update(right)
				}
				full, _ = full.Update(up)
			}
			full, _ = full.Update(letter('s'))
			for range m.Routes {
				requireFull("sources")
				full, _ = full.Update(down)
			}
			full, _ = full.Update(letter('c'))
			requireFull("copy mode")
			full, _ = full.Update(letter('1'))
			requireFull("copied")
		})
	}
}
