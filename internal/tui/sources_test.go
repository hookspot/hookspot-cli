package tui

import (
	"bytes"
	"maps"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"hookspot/internal/cards"
	"hookspot/internal/session"
	"hookspot/internal/ws"
)

// sourcesScreen is screen with a second stripe route, refunds, and every
// route's path.
func sourcesScreen() Fullscreen {
	m := screen()
	m.Routes = []cards.BannerRoute{
		m.Routes[0],
		{SourceUID: "src_stripe", Source: "stripe", PublicURL: "https://in.hookspot.test/src_stripe", RouteUID: "rte_refunds", Destination: "http://localhost:3000/refunds", Label: "refunds"},
		m.Routes[1],
	}
	for i, path := range []string{"/hooks", "/refunds", "/github"} {
		m.Routes[i].Path = path
	}
	return m
}

// inspected is m without --forward-to.
func inspected(m Fullscreen) Fullscreen {
	m.Target = ""
	for i := range m.Routes {
		m.Routes[i].Destination = ""
	}
	return m
}

// tally reports entries as a session would, with stats per route. The slowest
// response is p50, p95 and max at once, and every request falls in the
// clock's minute.
type tally struct {
	totals session.Stats
	routes map[string]session.Stats
}

func (t *tally) recorded(e session.Entry) session.Recorded {
	t.totals = add(t.totals, e)
	r := session.Recorded{Entry: e, Totals: t.totals}
	if e.RouteUID != "" {
		if t.routes == nil {
			t.routes = map[string]session.Stats{}
		}
		t.routes[e.RouteUID] = add(t.routes[e.RouteUID], e)
		r.Route = t.routes[e.RouteUID]
	}
	return r
}

func add(s session.Stats, e session.Entry) session.Stats {
	s.Count++
	s.Last = e
	s.PerMinute[14]++
	s.Minute = clock.Truncate(time.Minute)
	if e.Target == "" {
		return s
	}
	o := session.Outcome{Status: e.Response.Status}
	if e.Failure == nil && e.Response.Status < 300 {
		s.OK++
	} else {
		s.Failed++
	}
	if e.Failure != nil {
		o = session.Outcome{Failure: e.Failure.Kind}
	}
	// Earlier snapshots keep their own counts.
	s.Outcomes = maps.Clone(s.Outcomes)
	if s.Outcomes == nil {
		s.Outcomes = map[session.Outcome]int{}
	}
	s.Outcomes[o]++
	if e.Failure == nil {
		s.Max = max(s.Max, e.Latency)
		s.P50, s.P95 = s.Max, s.Max
	}
	return s
}

// refunds is entry n to stripe's refunds route.
func refunds(n, status int) session.Entry {
	e := entry(n, "src_stripe", status)
	e.Delivery.Path, e.RouteUID, e.Target = "/refunds", "rte_refunds", "http://localhost:3000/refunds"
	return e
}

// unmatched is entry n from github to a path no route has.
func unmatched(n int) session.Entry {
	e := entry(n, "src_github", 404)
	e.Delivery.Path, e.RouteUID, e.Target = "/other", "", "http://localhost:3000/other"
	return e
}

// traffic is a run's first five requests, one of them unmatched.
func traffic(t *tally) []tea.Msg {
	return []tea.Msg{
		session.Ready{},
		t.recorded(entry(1, "src_stripe", 200)), t.recorded(entry(2, "src_github", 500)), t.recorded(refused(3)),
		t.recorded(refunds(4, 200)), t.recorded(unmatched(5)),
	}
}

func TestSources(t *testing.T) {
	open := letter('s')
	tests := []struct {
		name          string
		width, height int
		inspect       bool
		events        func(t *tally) []tea.Msg
	}{
		{name: "open", width: 80, height: 30, events: func(t *tally) []tea.Msg {
			return append(traffic(t), open)
		}},
		{name: "move", width: 140, height: 24, events: func(t *tally) []tea.Msg {
			return append(traffic(t), open, down, down, down, up)
		}},
		{name: "copy mode", width: 80, height: 30, events: func(t *tally) []tea.Msg {
			return append(traffic(t), open, letter('c'))
		}},
		{name: "live update", width: 80, height: 30, events: func(t *tally) []tea.Msg {
			return append([]tea.Msg{open}, append(traffic(t), t.recorded(entry(6, "src_stripe", 422)))...)
		}},
		{name: "inspect", width: 80, height: 24, inspect: true, events: func(t *tally) []tea.Msg {
			e := entry(1, "src_stripe", 0)
			e.Target, e.Response, e.Latency = "", ws.Response{}, 0
			return []tea.Msg{session.Ready{}, t.recorded(e), open}
		}},
		{name: "help", width: 80, height: 30, events: func(t *tally) []tea.Msg {
			return append(traffic(t), open, letter('?'))
		}},
		{name: "back to the list", width: 80, height: 24, events: func(t *tally) []tea.Msg {
			// The requests view stays paused on #4.
			return append(traffic(t), up, open, down, tea.KeyPressMsg{Code: tea.KeyEscape})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := sourcesScreen()
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

func TestSourcesCopiesAField(t *testing.T) {
	tm := teatest.NewTestModel(t, sourcesScreen(), teatest.WithInitialTermSize(80, 30))
	for _, msg := range append(traffic(&tally{}), letter('s'), down, down, letter('c'), letter('1')) {
		tm.Send(msg)
	}
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte(ansi.SetSystemClipboard("https://in.hookspot.test/src_github")))
	}, teatest.WithDuration(5*time.Second))
	// The toast shows the copy and copy mode is over.
	golden.RequireEqual(t, final(t, tm))
}

func TestSourcesFields(t *testing.T) {
	tests := []struct {
		name    string
		inspect bool
		source  string
		want    []string
	}{
		{name: "forwarding", want: []string{
			"https://in.hookspot.test/src_stripe",
			"src_stripe",
			"http://localhost:3000/refunds",
			"rte_refunds",
			"hookspot listen stripe --forward-to http://localhost:3000",
			`curl -X POST 'https://in.hookspot.test/src_stripe' -H 'Content-Type: application/json' -d '{"type":"hookspot.test"}'`,
		}},
		{name: "inspect", inspect: true, want: []string{
			"https://in.hookspot.test/src_stripe",
			"src_stripe",
			"/refunds",
			"rte_refunds",
			"hookspot listen stripe",
			`curl -X POST 'https://in.hookspot.test/src_stripe' -H 'Content-Type: application/json' -d '{"type":"hookspot.test"}'`,
		}},
		{name: "quoted source", source: "Bob's shop\n", want: []string{4: `hookspot listen 'Bob'\''s shop\n' --forward-to http://localhost:3000`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := sourcesScreen()
			if test.inspect {
				m = inspected(m)
			}
			if test.source != "" {
				m.Routes[1].Source = test.source
			}
			model := press(press(tea.Model(m), letter('s')), down)
			for i, want := range test.want {
				if want == "" {
					continue
				}
				copied := press(press(model, letter('c')), letter(rune('1'+i))).(Fullscreen)
				if got, _, _ := strings.Cut(copied.toast, "\n"); got != "copied "+want {
					t.Errorf("field %d toast = %q, want %q", i+1, got, "copied "+want)
				}
				if copied.sources.copying {
					t.Errorf("field %d left copy mode on", i+1)
				}
			}
		})
	}
}

func TestSourcesCopyModeCancels(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, letter('7'), letter('x')} {
		m := press(press(press(tea.Model(sourcesScreen()), letter('s')), letter('c')), key).(Fullscreen)
		if m.sources.copying || !m.sources.open || m.toast != "" {
			t.Fatalf("%s after c: copying %t, open %t, toast %q; want copy mode cancelled on the page", key, m.sources.copying, m.sources.open, m.toast)
		}
	}
}

func TestSourcesTestEvent(t *testing.T) {
	var asked string
	m := sourcesScreen()
	m.Tester = tester{asked: &asked}
	model := press(tea.Model(m), letter('s'))
	for i, want := range []string{"stripe", "stripe", "github"} {
		model = press(model, letter('t'))
		if asked != want || model.(Fullscreen).toast != "test event sent to "+want {
			t.Fatalf("t on route %d tested %q, toast %q; want %s", i+1, asked, model.(Fullscreen).toast, want)
		}
		model = press(model, down)
	}
}

func TestSourcesScrollsToTheSelectedRoute(t *testing.T) {
	m := sourcesScreen()
	m.Routes = nil
	for i := range 12 {
		n := strconv.Itoa(i + 1)
		m.Routes = append(m.Routes, cards.BannerRoute{SourceUID: "src_" + n, Source: "source" + n, RouteUID: "rte_" + n, Label: "route" + n})
	}
	model, _ := tea.Model(m).Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = press(model, letter('s'))
	for range 11 {
		model = press(model, down)
	}
	if view := ansi.Strip(model.View().Content); !strings.Contains(view, "› source12 ") || !strings.Contains(view, "source12 → route12") {
		t.Fatalf("the last route isn't in view:\n%s", view)
	}
}

func TestSparklineShowsAnyRequest(t *testing.T) {
	if got, want := sparkline([15]int{1: 1, 2: 50, 14: 100}), "▁▂▅▁▁▁▁▁▁▁▁▁▁▁█"; got != want {
		t.Fatalf("sparkline = %s, want %s", got, want)
	}
}
