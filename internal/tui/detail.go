package tui

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"hookspot/internal/cards"
	"hookspot/internal/session"
)

// tab is one of the detail's tabs.
type tab int

const (
	overviewTab tab = iota
	requestTab
	responseTab
	timingTab
	tabCount
)

var tabTitles = [tabCount]string{"Overview", "Request", "Response", "Timing"}

// replaysAreLocal notes that a replay only resends to the local target.
const replaysAreLocal = "replays are local: they never change the delivery's status in Hookspot"

// detail frames the selected request's open tab from the line it's scrolled
// to.
func (m Fullscreen) detail(width, height int) []string {
	i := m.selectedIndex()
	if i < 0 {
		// The filter shows nothing.
		return make([]string, height)
	}
	e := m.entries[i]
	content, rows := m.tabLines(e, width), detailRows(height)
	content = content[m.scrolled(e, len(content), rows):]
	if len(content) > rows {
		more := faintStyle.Render(fmt.Sprintf("… %d more lines · pgdn scrolls", len(content)-rows+1))
		content = append(content[:rows-1:rows-1], more)
	}
	lines := append(m.tabs(), content...)
	title := faintStyle.Render("#"+strconv.Itoa(e.Number)) + " " + sourceStyle(e.Delivery.SourceUID).Render(cards.Line(m.sourceName(e.Delivery.SourceUID))) +
		faintStyle.Render(" · ") + cards.Line(method(e)) + " " + cards.Line(e.Delivery.Path)
	label := ""
	if e.Delivery.RequestUID != "" {
		label = faintStyle.Render(cards.Line(e.Delivery.RequestUID))
	}
	return panel(title, label, width, height, lines)
}

// tabLines are the open tab's lines for e, in a detail width wide.
func (m Fullscreen) tabLines(e session.Entry, width int) []string {
	inner := max(1, width-4)
	switch m.tab {
	case requestTab:
		return m.request(e)
	case responseTab:
		return m.response(e, inner)
	case timingTab:
		return m.timing(e)
	}
	return m.overview(e, inner)
}

// detailRows is how many of a tab's lines a detail height high shows: its
// borders and the tab names take four.
func detailRows(height int) int {
	return max(1, height-4)
}

// detailScroll is how many lines request number's tab is scrolled down.
type detailScroll struct {
	number int
	tab    tab
	lines  int
}

// scrolled is the first of e's open tab's lines to show, rows at a time; any
// other request or tab shows from the top.
func (m Fullscreen) scrolled(e session.Entry, lines, rows int) int {
	if m.scroll.number != e.Number || m.scroll.tab != m.tab {
		return 0
	}
	return max(0, min(m.scroll.lines, lines-rows))
}

// scrollDetail scrolls the selected request's open tab by pages, pausing on
// the request so an arrival doesn't take its place.
func (m Fullscreen) scrollDetail(pages int) Fullscreen {
	i := m.selectedIndex()
	if i < 0 {
		return m
	}
	e, l := m.entries[i], m.layout()
	lines, rows := len(m.tabLines(e, l.detailWidth)), detailRows(l.detailHeight)
	// A page goes on from the line the "more lines" note hid.
	first := m.scrolled(e, lines, rows) + pages*max(1, rows-1)
	m.paused, m.selected = true, e.Number
	m.scroll = detailScroll{number: e.Number, tab: m.tab, lines: max(0, min(first, lines-rows))}
	return m
}

// tabs names the tabs and underlines the open one.
func (m Fullscreen) tabs() []string {
	var titles, underline strings.Builder
	for t, title := range tabTitles {
		if t > 0 {
			titles.WriteString("   ")
			underline.WriteString("   ")
		}
		if tab(t) == m.tab {
			titles.WriteString(boldStyle.Render(title))
			underline.WriteString(strings.Repeat("━", len(title)))
		} else {
			titles.WriteString(faintStyle.Render(title))
			underline.WriteString(strings.Repeat(" ", len(title)))
		}
	}
	return []string{titles.String(), underline.String()}
}

// overview sums up what happened to e, where it came from and went, and when.
// A failure says first why and what to do, and later how the target answered
// since.
func (m Fullscreen) overview(e session.Entry, width int) []string {
	var lines []string
	switch {
	case e.Replay != nil:
		lines = strings.Split(m.Listen.Entry(e, width), "\n")
	case e.Target == "":
		lines = wrap(width, []string{faintStyle.Render("printed only: without --forward-to nothing is forwarded")})
	default:
		lines = []string{outcomeBadge(e) + " " + faintStyle.Render(cards.FormatLatency(e.Latency)+"  → "+cards.Line(e.Target))}
	}
	if e.Failed() {
		lines = append(lines, m.failure(e, width)...)
	}
	lines = append(lines, "",
		field("source", sourceStyle(e.Delivery.SourceUID).Render(cards.Line(m.sourceName(e.Delivery.SourceUID)))),
		field("route", m.route(e.RouteUID)),
		field("received", e.Received.Format(time.TimeOnly+".000")),
	)
	if e.Failed() {
		lines = append(lines, field("target", m.targetState()), field("last ok", m.lastSuccess()))
	}
	var prose []string
	if e.Test {
		prose = append(prose, "", cards.PathWorks(e.Target, e.Failure, width))
	}
	if e.Target != "" {
		prose = append(prose, "", faintStyle.Render(replaysAreLocal))
	}
	return append(lines, wrap(width, prose)...)
}

// route names e's route and its destination.
func (m Fullscreen) route(uid string) string {
	for _, route := range m.Routes {
		if uid != "" && route.RouteUID == uid {
			if route.Destination == "" {
				return cards.Line(route.Label)
			}
			return cards.Line(route.Label) + faintStyle.Render(" → ") + cards.Line(route.Destination)
		}
	}
	return faintStyle.Render("unmatched")
}

// request shows what Hookspot delivered: method, path and query, headers and
// body.
func (m Fullscreen) request(e session.Entry) []string {
	d := e.Delivery
	target := cards.Line(d.Path)
	if d.Query != "" {
		target += "?" + cards.Line(d.Query)
	}
	lines := []string{boldStyle.Render(cards.Line(method(e))) + " " + target}
	return append(lines, m.message(d.Headers, d.Body)...)
}

// response shows what the local target answered.
func (m Fullscreen) response(e session.Entry, width int) []string {
	switch {
	case e.Target == "":
		return wrap(width, []string{faintStyle.Render("not forwarded: listen with --forward-to to send requests to a local server")})
	case e.Failure != nil:
		lines := []string{outcomeBadge(e) + " " + faintStyle.Render(cards.FormatLatency(e.Latency)), ""}
		return append(lines, wrap(width, m.Listen.TransportHints(e.Target, e.Failure))...)
	}
	r := e.Response
	lines := []string{outcomeBadge(e) + " " + faintStyle.Render(cards.FormatLatency(e.Latency))}
	if hint := cards.RedirectHint(r); hint != nil {
		lines = append(append(lines, ""), wrap(width, hint)...)
	}
	return append(lines, m.message(r.Headers, r.Body)...)
}

// message lists headers, then the body.
func (m Fullscreen) message(headers http.Header, body []byte) []string {
	count := "none"
	if len(headers) > 0 {
		count = strconv.Itoa(len(headers))
	}
	lines := append([]string{"", faintStyle.Render("headers · " + count)}, m.Listen.Headers(headers)...)
	lines = append(lines, "", faintStyle.Render("body · "+cards.BodyTitle(body, headers)))
	return append(lines, m.Listen.Body(body, headers)...)
}

// timing shows when e arrived, how long the target took, and the route's
// latencies so far.
func (m Fullscreen) timing(e session.Entry) []string {
	latency := cards.FormatLatency(e.Latency)
	lines := []string{field("received", e.Received.Format(time.TimeOnly+".000"))}
	switch {
	case e.Target == "":
		lines = append(lines, field("forwarded", faintStyle.Render("no, without --forward-to")))
	case e.Failure != nil:
		lines = append(lines, field("failed", cards.TransportLabel(e.Failure.Kind)+" after "+latency))
	default:
		lines = append(lines, field("answered", "in "+latency))
	}
	if c := e.Replay; c != nil {
		lines = append(lines, field("replayed", "#"+strconv.Itoa(c.Original)+" took "+cards.FormatLatency(c.Latency)))
	}
	if stats := m.routes[e.RouteUID]; e.RouteUID != "" && stats.Max > 0 {
		lines = append(lines, field("route", "p50 "+cards.FormatLatency(stats.P50)+" · p95 "+cards.FormatLatency(stats.P95)+" · max "+cards.FormatLatency(stats.Max)))
	}
	return lines
}

// field is a labelled line of the detail.
func field(label, value string) string {
	return faintStyle.Render(pad(label, 10)) + value
}

// outcomeBadge is what forwarding got: the status, or the transport failure.
func outcomeBadge(e session.Entry) string {
	if e.Failure != nil {
		return cards.ColorBadge(cards.StatusColor(0), "✗ "+cards.TransportLabel(e.Failure.Kind))
	}
	return cards.ColorBadge(cards.StatusColor(e.Response.Status), statusText(e.Response.Status))
}

// statusText is a status with its name, such as 500 Internal Server Error.
func statusText(status int) string {
	if text := http.StatusText(status); text != "" {
		return strconv.Itoa(status) + " " + text
	}
	return strconv.Itoa(status)
}

// failure says what happened to failed e, then what to do: replay now or,
// when nothing answered, wait for the target and then replay. A replay's
// summary already says what happened.
func (m Fullscreen) failure(e session.Entry, width int) []string {
	var happened []string
	switch {
	case e.Replay != nil:
	case e.Failure != nil:
		happened = m.Listen.TransportHints(e.Target, e.Failure)
	default:
		happened = cards.RedirectHint(e.Response)
	}
	lines := append(wrap(width, happened), "", cards.Badge("r")+" replay now")
	switch {
	case m.wait.number == e.Number:
		lines = append(lines, m.waitLine())
	case e.Failure != nil:
		lines = append(lines, cards.Badge("w")+" wait for "+m.targetAddress()+", then replay")
	}
	return lines
}

// targetState is the target and how it answered the newest request.
func (m Fullscreen) targetState() string {
	e := m.entries[len(m.entries)-1]
	state := "answered " + lipgloss.NewStyle().Foreground(cards.StatusColor(e.Response.Status)).Render(strconv.Itoa(e.Response.Status))
	if e.Failure != nil {
		state = errorStyle.Render("✗ " + cards.TransportLabel(e.Failure.Kind))
	}
	return m.targetAddress() + "  " + state + faintStyle.Render(" · #"+strconv.Itoa(e.Number)+" at "+e.Received.Format(time.TimeOnly))
}

// lastSuccess is the newest request the target answered with a 2xx.
func (m Fullscreen) lastSuccess() string {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if e := m.entries[i]; e.Target != "" && !e.Failed() {
			return "#" + strconv.Itoa(e.Number) + " at " + e.Received.Format(time.TimeOnly) + faintStyle.Render(" · "+cards.FormatLatency(e.Latency))
		}
	}
	return faintStyle.Render("none yet")
}

// targetAddress is the --forward-to host and port, which w dials. Target is
// a URL proxy.New already parsed.
func (m Fullscreen) targetAddress() string {
	target, _ := url.Parse(m.Target)
	port := target.Port()
	switch {
	case port != "":
	case target.Scheme == "https":
		port = "443"
	default:
		port = "80"
	}
	return net.JoinHostPort(target.Hostname(), port)
}

const dialTimeout = time.Second

var waitKey = key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "wait, then replay"))

// wait is w's: it dials the target until it answers, then replays request
// number. id tells its messages from an earlier wait's.
type wait struct {
	id, number int
	// replaying is set once the target answered; result is the replay's
	// outcome.
	replaying bool
	result    string
}

type (
	// dialedMsg is a dial of wait id's target; err is nil when it answered.
	dialedMsg struct {
		id  int
		err error
	}
	// waitReplayedMsg ends wait id with its replay's error.
	waitReplayedMsg struct {
		id  int
		err error
	}
)

// waiting reports whether a wait hasn't ended yet.
func (m Fullscreen) waiting() bool {
	return m.wait.number != 0 && m.wait.result == ""
}

// startWait waits on the selected request when it found nothing listening,
// pausing on it so its detail shows the wait.
func (m Fullscreen) startWait(i int) (Fullscreen, tea.Cmd) {
	switch {
	case m.Target == "":
		return m.show(session.ErrNoTarget.Error())
	case i < 0:
		return m, nil
	case m.entries[i].Failure == nil:
		return m.show("#" + strconv.Itoa(m.entries[i].Number) + " got a response; r replays it now")
	}
	m.paused, m.selected = true, m.entries[i].Number
	m.wait = wait{id: m.wait.id + 1, number: m.selected}
	return m, dial(m.targetAddress(), 0, m.wait.id)
}

// dial tries address after a pause, off the event loop.
func dial(address string, after time.Duration, id int) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(after)
		conn, err := net.DialTimeout("tcp", address, dialTimeout)
		if err == nil {
			_ = conn.Close()
		}
		return dialedMsg{id: id, err: err}
	}
}

// dialed replays once the target answers, else dials again after dialEvery.
func (m Fullscreen) dialed(msg dialedMsg) (Fullscreen, tea.Cmd) {
	if msg.id != m.wait.id || !m.waiting() {
		return m, nil
	}
	if msg.err != nil {
		return m, dial(m.targetAddress(), m.dialInterval(), msg.id)
	}
	m.wait.replaying = true
	requests, n := m.Requests, m.wait.number
	return m, func() tea.Msg { return waitReplayedMsg{id: msg.id, err: requests.Replay(n)} }
}

// waitReplayed puts the replay's outcome in place of the waiting line. The
// replay's entry was recorded before Replay returned.
func (m Fullscreen) waitReplayed(msg waitReplayedMsg) Fullscreen {
	if msg.id != m.wait.id || !m.waiting() {
		return m
	}
	if msg.err != nil {
		m.wait.result = errorStyle.Render("✗") + " " + cards.Line(msg.err.Error())
		return m
	}
	m.wait.result = markStyle.Render("↻") + " replayed"
	for i := len(m.entries) - 1; i >= 0; i-- {
		if e := m.entries[i]; e.ReplayOf == m.wait.number {
			m.wait.result += " as #" + strconv.Itoa(e.Number) + " " + outcomeBadge(e) + " " + faintStyle.Render(cards.FormatLatency(e.Latency))
			break
		}
	}
	return m
}

// waitLine is w's progress: waiting, replaying, then the replay's outcome.
func (m Fullscreen) waitLine() string {
	address := m.targetAddress()
	switch {
	case m.wait.result != "":
		return m.wait.result
	case m.wait.replaying:
		return markStyle.Render("↻") + " " + address + " answered, replaying #" + strconv.Itoa(m.wait.number) + "…"
	}
	return markStyle.Render("○") + " waiting for " + address + faintStyle.Render(" · checking every "+m.dialInterval().String()+" · esc stops")
}

func (m Fullscreen) dialInterval() time.Duration {
	if m.dialEvery == 0 {
		return time.Second
	}
	return m.dialEvery
}
