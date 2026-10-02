package tui

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"hookspot/internal/cards"
	"hookspot/internal/session"
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

// traffic is a run's first six requests, one of them unmatched.
func traffic(r *tally) []tea.Msg {
	return []tea.Msg{
		session.Ready{},
		r.recorded(entry(1, "src_stripe", 200)), r.recorded(entry(2, "src_github", 500)), r.recorded(refused(3)),
		r.recorded(refunds(4, 200)), r.recorded(unmatched(5)), r.recorded(entry(6, "src_stripe", 200)),
	}
}

func TestSources(t *testing.T) {
	open := letter('s')
	tests := []struct {
		name          string
		width, height int
		inspect       bool
		events        func(r *tally) []tea.Msg
	}{
		{name: "open", width: 80, height: 30, events: func(r *tally) []tea.Msg {
			return append(traffic(r), open)
		}},
		{name: "move", width: 140, height: 24, events: func(r *tally) []tea.Msg {
			return append(traffic(r), open, down, down, down, up)
		}},
		{name: "copy mode", width: 80, height: 30, events: func(r *tally) []tea.Msg {
			return append(traffic(r), open, letter('c'))
		}},
		{name: "live update", width: 80, height: 30, events: func(r *tally) []tea.Msg {
			return append([]tea.Msg{open}, append(traffic(r), r.recorded(entry(7, "src_stripe", 422)))...)
		}},
		{name: "inspect", width: 80, height: 24, inspect: true, events: func(r *tally) []tea.Msg {
			return []tea.Msg{session.Ready{}, r.recorded(printedOnly(1, "src_stripe")), open}
		}},
		{name: "help", width: 80, height: 30, events: func(r *tally) []tea.Msg {
			return append(traffic(r), open, letter('?'))
		}},
		{name: "back to the list", width: 80, height: 24, events: func(r *tally) []tea.Msg {
			// The requests view stays paused on #5.
			return append(traffic(r), up, open, down, open)
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
	waitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte(ansi.SetSystemClipboard("https://in.hookspot.test/src_github")))
	})
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
	requests := newFake()
	m := sourcesScreen()
	m.Requests = requests
	model := press(tea.Model(m), letter('s'))
	for i, want := range []string{"stripe", "stripe", "github"} {
		model = press(model, letter('t'))
		if asked := receive(t, requests.asked); asked != "test "+want || model.(Fullscreen).toast != "test event sent to "+want {
			t.Fatalf("t on route %d asked for %s, toast %q; want %s", i+1, asked, model.(Fullscreen).toast, want)
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
	if got, want := sparkline([session.StatsMinutes]int{1: 1, 2: 50, 14: 100}), "▁▂▅▁▁▁▁▁▁▁▁▁▁▁█"; got != want {
		t.Fatalf("sparkline = %s, want %s", got, want)
	}
}
