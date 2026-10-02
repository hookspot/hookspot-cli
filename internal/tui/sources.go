package tui

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"hookspot/internal/cards"
	"hookspot/internal/proxy"
	"hookspot/internal/session"
	"hookspot/internal/ws"
)

const (
	// activityWidth is the activity panel's width beside the route detail.
	activityWidth      = 50
	activityLabelWidth = 12
)

// sourcesPage is the Sources page's state. The requests view keeps its own
// selection while the page is open.
type sourcesPage struct {
	open bool
	// selected is the selected route's index in Routes.
	selected int
	// copying is set by c until the next key: a field's number copies it.
	copying bool
}

// sourcesKeys are the Sources page's own keys; ↑↓, t, ? and q work as in the
// requests view.
var sourcesKeys = struct {
	back, copy, field, cancel key.Binding
}{
	back:   key.NewBinding(key.WithKeys("esc", "s"), key.WithHelp("esc", "back")),
	copy:   key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy…")),
	field:  key.NewBinding(key.WithKeys("1", "2", "3", "4", "5", "6"), key.WithHelp("1-6", "copy that field")),
	cancel: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
}

func (m Fullscreen) sourcesKey(msg tea.KeyPressMsg) (Fullscreen, tea.Cmd) {
	if m.sources.copying {
		m.sources.copying = false
		if key.Matches(msg, sourcesKeys.field) {
			n, _ := strconv.Atoi(msg.String())
			return m.copyField(n)
		}
		return m, nil
	}
	switch {
	case key.Matches(msg, keys.up):
		m.sources.selected = max(0, m.sources.selected-1)
	case key.Matches(msg, keys.down):
		m.sources.selected = max(0, min(len(m.Routes)-1, m.sources.selected+1))
	case key.Matches(msg, sourcesKeys.copy):
		m.sources.copying = len(m.Routes) > 0
	case key.Matches(msg, keys.test):
		if len(m.Routes) > 0 {
			return m, sendTest(m.Requests, m.Routes[m.sources.selected].Source)
		}
	case key.Matches(msg, sourcesKeys.back):
		m.sources.open = false
	case key.Matches(msg, keys.help):
		m.help = !m.help
	case key.Matches(msg, keys.quit):
		return m, stopListening
	}
	return m, nil
}

// copyField copies the selected route's field n. Server data may hold control
// characters, which would end a bracketed paste, so the copy is the escaped
// value the toast shows.
func (m Fullscreen) copyField(n int) (Fullscreen, tea.Cmd) {
	value := cards.Line(m.routeFields(m.Routes[m.sources.selected])[n-1].value)
	m, expire := m.show("copied " + value + "\n" + cards.ClipboardNote)
	return m, tea.Batch(tea.SetClipboard(value), expire)
}

type routeField struct {
	label, value string
}

func (m Fullscreen) routeFields(route cards.BannerRoute) []routeField {
	listen := "hookspot listen " + shellWord(route.Source)
	if m.forwarding() {
		listen += " --forward-to " + shellWord(m.Target)
	}
	return []routeField{
		{"public URL", route.PublicURL},
		{"source ID", route.SourceUID},
		{"destination", m.routeDestination(route)},
		{"route ID", route.RouteUID},
		{"listen", listen},
		{"test", session.TestCurl(route.PublicURL)},
	}
}

// routeDestination is where the route's requests go: its URL, or in inspect
// mode only its path.
func (m Fullscreen) routeDestination(route cards.BannerRoute) string {
	if !m.forwarding() {
		return route.Path
	}
	return route.Destination
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellWord quotes s for a POSIX shell unless it's plain.
func shellWord(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	return session.Quote(s)
}

// sourcesView lists every route with its stats, then the selected route's
// fields and activity, beside each other when wide.
func (m Fullscreen) sourcesView() tea.View {
	width, height := m.size()
	toasts, footer := m.toastLines(width), m.sourcesFooter(width)
	panes := max(0, height-2-len(toasts)-len(footer))
	// Many routes leave half the room to the selected one.
	table := min(panes, len(m.Routes)+4, max(panes/2, 5))
	lines := append([]string{m.header(width), m.sourcesTitle()}, m.sourcesTable(width, table)...)
	if rest := 2 + panes - len(lines); len(m.Routes) > 0 && rest > 0 {
		route := m.Routes[m.sources.selected]
		fields, activity := m.routeDetail(route), m.activity(route)
		title := cards.SourceStyle(route.SourceUID).Render(cards.Line(route.Source)) + faintStyle.Render(" → ") + cards.Line(route.Label)
		label := faintStyle.Render("press c, then a number, to copy")
		if width >= splitWidth {
			left := panel(title, label, width-activityWidth-1, rest, fields)
			right := panel("Activity", "", activityWidth, rest, activity)
			for i := range left {
				lines = append(lines, left[i]+" "+right[i])
			}
		} else {
			detail := min(rest, len(fields)+2)
			lines = append(lines, panel(title, label, width, detail, fields)...)
			lines = append(lines, panel("Activity", "", width, rest-detail, activity)...)
		}
	}
	lines = append(lines, make([]string, max(0, 2+panes-len(lines)))...)
	return fullView(append(append(lines, toasts...), footer...), width, height)
}

// sourcesTitle names the page and what it counts.
func (m Fullscreen) sourcesTitle() string {
	sources := 0
	for i := range m.Routes {
		if firstOfSource(m.Routes, i) {
			sources++
		}
	}
	return boldStyle.Render("Sources") + faintStyle.Render(" · "+cards.Count(sources, "source")+" · "+cards.Count(len(m.Routes), "route")+" · stats since listen started")
}

// sourcesTable frames a row per route, the selected one marked as in the
// request list and scrolled into view, then the totals, the only row that
// counts unmatched requests. URLs are cut at the start, since they differ at
// the end.
func (m Fullscreen) sourcesTable(width, height int) []string {
	rows := make([]map[string]cell, len(m.Routes))
	matched := 0
	for i, route := range m.Routes {
		stats := m.routes[route.RouteUID]
		matched += stats.Count
		rows[i] = m.statsCells(stats)
		rows[i]["ROUTE"] = cell{text: cards.Line(route.Label)}
		rows[i]["DESTINATION"] = cell{text: cards.Line(m.routeDestination(route))}
		if firstOfSource(m.Routes, i) {
			rows[i]["SOURCE"] = cell{text: cards.Line(route.Source), style: cards.SourceStyle(route.SourceUID)}
			rows[i]["PUBLIC URL"] = cell{text: cards.Line(route.PublicURL), style: faintStyle}
		} else {
			rows[i]["SOURCE"] = cell{text: " └", style: faintStyle}
		}
	}
	totals := m.statsCells(m.totals)
	delete(totals, "LAST")

	inner := max(0, width-panelFrame-markerWidth)
	requests, last := column{title: "REQS", right: true}, column{title: "LAST"}
	numbers := []column{requests, {title: "OK", right: true}, {title: "FAIL", right: true}, {title: "P50", right: true}, last}
	if !m.forwarding() {
		numbers = []column{requests, last}
	}
	numberColumns := sized(numbers, slices.Concat(rows, []map[string]cell{totals}))
	names := sizedNames(sized([]column{{title: "SOURCE"}, {title: "PUBLIC URL"}, {title: "ROUTE"}, {title: "DESTINATION"}}, rows), inner-2-rowWidth(numberColumns))
	for _, cells := range rows {
		for _, c := range names {
			if text := cells[c.title].text; urlColumn(c) && lipgloss.Width(text) > c.width {
				cells[c.title] = cell{text: ansi.TruncateLeft(text, lipgloss.Width(text)-c.width+1, "…"), style: cells[c.title].style}
			}
		}
	}
	row := func(cells map[string]cell, plain bool) string {
		return render(names, rowCells(names, cells), plain) + "  " + render(numberColumns, rowCells(numberColumns, cells), plain)
	}

	titles := map[string]cell{}
	for _, c := range slices.Concat(names, numberColumns) {
		titles[c.title] = cell{text: c.title}
	}
	lines := []string{"  " + faintStyle.Render(row(titles, true))}
	// The borders, column titles and totals take four lines.
	visible := max(1, height-4)
	first := max(0, m.sources.selected-visible+1)
	for i := first; i < min(len(rows), first+visible); i++ {
		cells := rows[i]
		if i == m.sources.selected {
			lines = append(lines, "› "+selectedStyle.Render(cards.Pad(row(cells, true), inner)))
		} else {
			lines = append(lines, "  "+row(cells, false))
		}
	}
	label := "total"
	if unmatched := m.totals.Count - matched; unmatched > 0 {
		label += " · " + strconv.Itoa(unmatched) + " unmatched"
	}
	span := rowWidth(names)
	totalsRow := faintStyle.Render(cards.Pad(ansi.Truncate(label, span, "…"), span)) + "  " + render(numberColumns, rowCells(numberColumns, totals), false)
	lines = append(lines, "  "+totalsRow)
	return panel("Sources & routes", "", width, height, lines)
}

// statsCells are the table's numbers for stats; nothing forwards in inspect
// mode, so only counts and times show there.
func (m Fullscreen) statsCells(stats session.Stats) map[string]cell {
	cells := map[string]cell{"REQS": {text: strconv.Itoa(stats.Count)}, "LAST": {text: "—", style: faintStyle}}
	if stats.Last.Number != 0 {
		last := stats.Last.Received.Format(time.TimeOnly)
		cells["LAST"] = cell{text: last, style: faintStyle}
		if m.forwarding() {
			result := outcome(stats.Last)
			cells["LAST"] = cell{text: last + " " + result.text, style: result.style}
		}
	}
	failed := lipgloss.Style{}
	if stats.Failed > 0 {
		failed = errorStyle
	}
	cells["OK"] = cell{text: strconv.Itoa(stats.OK)}
	cells["FAIL"] = cell{text: strconv.Itoa(stats.Failed), style: failed}
	cells["P50"] = cell{text: "—", style: faintStyle}
	if stats.Max > 0 {
		cells["P50"] = cell{text: cards.FormatLatency(stats.P50)}
	}
	return cells
}

// sized makes columns as wide as their widest text.
func sized(columns []column, rows []map[string]cell) []column {
	for i, c := range columns {
		columns[i].width = len(c.title)
		for _, cells := range rows {
			columns[i].width = max(columns[i].width, lipgloss.Width(cells[c.title].text))
		}
	}
	return columns
}

// sizedNames fits the name columns into width. The source and route label
// are capped; the public URL and destination share what's left, and the
// public URL goes when either would get under minPath, then the destination.
func sizedNames(columns []column, width int) []column {
	for i, c := range columns {
		switch c.title {
		case "SOURCE":
			columns[i].width = min(c.width, maxSourceWidth)
		case "ROUTE":
			columns[i].width = min(c.width, 16)
		}
	}
	// room is what the URL columns get.
	room := func() int {
		used := 2 * (len(columns) - 1)
		for _, c := range columns {
			if !urlColumn(c) {
				used += c.width
			}
		}
		return width - used
	}
	if room()/2 < minPath {
		columns = without(columns, "PUBLIC URL")
		if room() < minPath {
			columns = without(columns, "DESTINATION")
		}
	}
	url, destination := titled(columns, "PUBLIC URL"), titled(columns, "DESTINATION")
	switch {
	case url >= 0:
		columns[destination].width = min(columns[destination].width, max(room()/2, room()-columns[url].width))
		columns[url].width = min(columns[url].width, room()-columns[destination].width)
	case destination >= 0:
		columns[destination].width = min(columns[destination].width, room())
	}
	return columns
}

func urlColumn(c column) bool {
	return c.title == "PUBLIC URL" || c.title == "DESTINATION"
}

func titled(columns []column, title string) int {
	return slices.IndexFunc(columns, func(c column) bool { return c.title == title })
}

func without(columns []column, title string) []column {
	return slices.DeleteFunc(columns, func(c column) bool { return c.title == title })
}

func rowWidth(columns []column) int {
	width := 2 * max(0, len(columns)-1)
	for _, c := range columns {
		width += c.width
	}
	return width
}

func rowCells(columns []column, cells map[string]cell) []cell {
	row := make([]cell, len(columns))
	for i, c := range columns {
		row[i] = cells[c.title]
	}
	return row
}

// routeDetail numbers the route's fields; after c, the numbers stand out.
func (m Fullscreen) routeDetail(route cards.BannerRoute) []string {
	var lines []string
	for i, f := range m.routeFields(route) {
		number := faintStyle.Render(" " + strconv.Itoa(i+1) + " ")
		if m.sources.copying {
			number = cards.Badge(strconv.Itoa(i + 1))
		}
		lines = append(lines, number+"  "+faintStyle.Render(cards.Pad(f.label, 13))+cards.Line(f.value))
	}
	return lines
}

// activity sums up the route's requests: counts, latency, a per-minute
// sparkline, the newest request and how forwarded ones ended. Inspect
// mode forwards nothing, so it shows only counts and times.
func (m Fullscreen) activity(route cards.BannerRoute) []string {
	stats := m.routes[route.RouteUID]
	requests := strconv.Itoa(stats.Count)
	if m.forwarding() {
		failed := strconv.Itoa(stats.Failed) + " failed"
		if stats.Failed > 0 {
			failed = errorStyle.Render(failed)
		}
		requests += faintStyle.Render(" · ") + strconv.Itoa(stats.OK) + " ok" + faintStyle.Render(" · ") + failed
	}
	lines := []string{activityLine("requests", requests)}
	if m.forwarding() {
		latency := faintStyle.Render("—")
		if stats.Max > 0 {
			latency = latencies(stats)
		}
		lines = append(lines, activityLine("latency", latency))
	}
	lines = append(lines, activityLine("per minute", sparkline(stats.PerMinuteAt(m.clock()))+faintStyle.Render("  last "+strconv.Itoa(session.StatsMinutes)+" min")))
	last := faintStyle.Render("—")
	if e := stats.Last; e.Number != 0 {
		last = "#" + strconv.Itoa(e.Number) + "  " + faintStyle.Render(e.Received.Format(time.TimeOnly))
		if m.forwarding() {
			result := outcome(e)
			last += "  " + result.style.Render(result.text)
		}
	}
	lines = append(lines, activityLine("last", last))
	if !m.forwarding() {
		return lines
	}
	return append(lines, statusBars(stats.Outcomes)...)
}

func activityLine(label, value string) string {
	return faintStyle.Render(cards.Pad(label, activityLabelWidth)) + value
}

// statusBars break forwarded requests down by outcome, most common first,
// with bars scaled to the most common.
func statusBars(outcomes map[session.Outcome]int) []string {
	type bar struct {
		outcome cell
		count   int
	}
	var bars []bar
	labelWidth, countWidth, most := 0, 0, 0
	for o, count := range outcomes {
		e := session.Entry{Response: ws.Response{Status: o.Status}}
		if o.Failure != "" {
			e.Failure = &proxy.TransportFailure{Kind: o.Failure}
		}
		b := bar{outcome(e), count}
		bars = append(bars, b)
		labelWidth, countWidth, most = max(labelWidth, len(b.outcome.text)), max(countWidth, len(strconv.Itoa(count))), max(most, count)
	}
	slices.SortFunc(bars, func(a, b bar) int { return cmp.Or(b.count-a.count, strings.Compare(a.outcome.text, b.outcome.text)) })
	// The label, the gaps and the count leave the rest.
	longest := max(1, activityWidth-panelFrame-activityLabelWidth-labelWidth-2-1-countWidth)
	var lines []string
	for i, b := range bars {
		label := ""
		if i == 0 {
			label = "status"
		}
		lines = append(lines, activityLine(label, cards.Pad(b.outcome.text, labelWidth)+"  "+b.outcome.style.Render(strings.Repeat("█", max(1, b.count*longest/most)))+" "+strconv.Itoa(b.count)))
	}
	return lines
}

var sparks = []rune("▁▂▃▄▅▆▇█")

// sparkline draws counts as bars scaled to the largest; any request at all
// rises above the floor.
func sparkline(counts [session.StatsMinutes]int) string {
	most := slices.Max(counts[:])
	var line strings.Builder
	for _, count := range counts {
		level := 0
		if count > 0 {
			level = (count*(len(sparks)-1) + most - 1) / most
		}
		line.WriteRune(sparks[level])
	}
	return line.String()
}

// sourcesFooter lists the page's keys, or all of them in columns after ?.
func (m Fullscreen) sourcesFooter(width int) []string {
	return helpView(sourcesKeyMap{copying: m.sources.copying}, m.help && !m.sources.copying, width)
}

// sourcesKeyMap lists the page's keys; after c, only the field numbers.
type sourcesKeyMap struct{ copying bool }

func (k sourcesKeyMap) ShortHelp() []key.Binding {
	if k.copying {
		return []key.Binding{sourcesKeys.field, sourcesKeys.cancel}
	}
	return []key.Binding{keys.up, sourcesKeys.copy, keys.test, sourcesKeys.back, keys.help, keys.quit}
}

func (k sourcesKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{described(keys.up, "select a route"), described(sourcesKeys.copy, "copy a field by its number"), described(keys.test, "send a test event")},
		{described(sourcesKeys.back, "back to requests"), described(keys.help, "close help"), described(keys.quit, "stop listening")},
	}
}
