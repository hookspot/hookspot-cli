package tui

import (
	"net/http"
	"strconv"
	"strings"
	"time"

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

// detail frames the selected request's open tab.
func (m Fullscreen) detail(width, height int) []string {
	e := m.entries[m.selectedIndex()]
	inner := max(1, width-4)
	lines := m.tabs()
	switch m.tab {
	case overviewTab:
		lines = append(lines, m.overview(e, inner)...)
	case requestTab:
		lines = append(lines, m.request(e)...)
	case responseTab:
		lines = append(lines, m.response(e, inner)...)
	case timingTab:
		lines = append(lines, m.timing(e)...)
	}
	title := faintStyle.Render("#"+strconv.Itoa(e.Number)) + " " + sourceStyle(e.Delivery.SourceUID).Render(cards.Line(m.sourceName(e.Delivery.SourceUID))) +
		faintStyle.Render(" · ") + cards.Line(method(e)) + " " + cards.Line(e.Delivery.Path)
	label := ""
	if e.Delivery.RequestUID != "" {
		label = faintStyle.Render(cards.Line(e.Delivery.RequestUID))
	}
	return panel(title, label, width, height, lines)
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
	lines = append(lines, "",
		field("source", sourceStyle(e.Delivery.SourceUID).Render(cards.Line(m.sourceName(e.Delivery.SourceUID)))),
		field("route", m.route(e.RouteUID)),
		field("received", e.Received.Format(time.TimeOnly+".000")),
	)
	var prose []string
	if e.Test {
		prose = append(prose, "", cards.PathWorks(cards.Request{Target: e.Target, Failure: e.Failure}, width))
	}
	if e.Failure != nil && e.Replay == nil {
		prose = append(append(prose, ""), m.Listen.TransportHints(e.Target, e.Failure)...)
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
	status := strconv.Itoa(e.Response.Status)
	if text := http.StatusText(e.Response.Status); text != "" {
		status += " " + text
	}
	return cards.ColorBadge(cards.StatusColor(e.Response.Status), status)
}
