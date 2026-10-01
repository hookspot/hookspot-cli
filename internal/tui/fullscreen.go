package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"hookspot/internal/cards"
	"hookspot/internal/proxy"
	"hookspot/internal/session"
)

const (
	// splitWidth is the narrowest screen that puts the detail beside the
	// list; narrower ones stack them.
	splitWidth = 140
	listWidth  = 72
	// minPath is the path column's width below which the list drops its time
	// column, then its method column.
	minPath = 12
)

var (
	faintStyle    = lipgloss.NewStyle().Faint(true)
	boldStyle     = lipgloss.NewStyle().Bold(true)
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Red)
	markStyle     = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	selectedStyle = lipgloss.NewStyle().Reverse(true)
)

// Fullscreen is listen's full-screen view: a header, the request list and the
// selected request's detail, side by side or stacked when narrow, then the
// keys. It keeps the history's entries, dropping each one an event says was
// evicted.
type Fullscreen struct {
	Requests Requests
	// Listen renders requests: source names, --max-* limits and redaction.
	Listen  cards.Listen
	Project string
	// Routes are the routes listened to; one source's routes are adjacent.
	Routes []cards.BannerRoute
	// Target is the --forward-to URL; "" in inspect mode, where nothing
	// replays.
	Target string
	// RequestsURL is where to retry requests missed while offline.
	RequestsURL string
	// ShowSensitiveHeaders keeps sensitive header values in copied commands
	// and fixtures.
	ShowSensitiveHeaders bool

	// now and toastFor are time.Now and 4s unless a test fixes them.
	now      func() time.Time
	toastFor time.Duration

	connection
	width, height int
	totals        session.Stats
	// routes are the route stats by route UID.
	routes map[string]session.Stats
	// entries are oldest first.
	entries []session.Entry
	// paused holds the selection on request selected; otherwise it follows
	// the newest.
	paused   bool
	selected int
	// offset is the index of the list's first row.
	offset int
	tab    tab
	scroll detailScroll
	// filter narrows the list. wait is w's: it dials the target every
	// dialEvery, 1s unless a test sets it, until it answers, then replays.
	filter    filter
	wait      wait
	dialEvery time.Duration
	// notices are the source warnings, shown until a request arrives.
	notices []string
	hint    *session.TestHint
	sources sourcesPage
	toast   string
	toastID int
	help    bool
}

type (
	// clockMsg redraws the header's clock.
	clockMsg struct{}
	// toastExpiredMsg ends the toast it numbers.
	toastExpiredMsg int
)

func (m Fullscreen) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Every(time.Second, func(time.Time) tea.Msg { return clockMsg{} })
}

func (m Fullscreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := m.update(msg)
	m.offset = m.firstRow()
	return m, cmd
}

func (m Fullscreen) update(msg tea.Msg) (Fullscreen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case clockMsg:
		return m, tick()
	case session.Ready:
		m.connection = m.follow(cards.StateLive, nil)
	case session.Reconnected:
		m.connection = m.follow(cards.StateLive, nil)
		return m.show(cards.Reconnected(msg.Offline, m.RequestsURL))
	case session.ConnectionLost:
		m.connection = m.follow(cards.StateOffline, msg.Err)
	case session.DisabledSource:
		m.notices = append(m.notices, cards.DisabledSource(msg.Name))
	case session.SkippedSource:
		m.notices = append(m.notices, cards.SkippedSource(msg.Name))
	case session.TestHint:
		m.hint = &msg
	case session.RootNotFound:
		return m.show(cards.RootNotFound(msg.Root, msg.Status))
	case session.Recorded:
		return m.record(msg)
	case stoppingMsg:
		m.state = cards.StateStopping
	case replyMsg:
		return m.show(string(msg))
	case copiedMsg:
		m, expire := m.show(msg.notes)
		return m, tea.Batch(tea.SetClipboard(msg.command), expire)
	case dialedMsg:
		return m.dialed(msg)
	case waitReplayedMsg:
		return m.waitReplayed(msg), nil
	case toastExpiredMsg:
		if int(msg) == m.toastID {
			m.toast = ""
		}
	case tea.PasteMsg:
		if m.filter.editing {
			m.filter.input += pasted(msg.Content)
		}
	case tea.KeyPressMsg:
		if m.filter.editing {
			return m.editFilter(msg), nil
		}
		return m.key(msg)
	}
	return m, nil
}

// record lists a new entry after dropping the evicted ones, always the
// oldest. A replay is announced, since the selection may not move to it.
func (m Fullscreen) record(r session.Recorded) (Fullscreen, tea.Cmd) {
	if len(r.Evicted) > 0 {
		last := r.Evicted[len(r.Evicted)-1]
		dropped := 0
		for dropped < len(m.entries) && m.entries[dropped].Number <= last {
			// offset counts the rows the filter shows.
			if m.filter.shows(m.entries[dropped].Number) {
				m.offset--
			}
			dropped++
		}
		clear(m.entries[:dropped]) // releases their bodies
		m.entries = m.entries[dropped:]
	}
	m.entries = append(m.entries, r.Entry)
	m.filter = m.filter.recorded(r, m.Listen)
	m.totals = r.Totals
	if r.Entry.RouteUID != "" {
		if m.routes == nil {
			m.routes = map[string]session.Stats{}
		}
		m.routes[r.Entry.RouteUID] = r.Route
	}
	if r.Entry.ReplayOf != 0 {
		return m.show(fmt.Sprintf("replayed #%d as #%d", r.Entry.ReplayOf, r.Entry.Number))
	}
	return m, nil
}

// show puts text above the keys until it expires or another replaces it.
func (m Fullscreen) show(text string) (Fullscreen, tea.Cmd) {
	m.toastID++
	m.toast = text
	id, duration := m.toastID, m.toastFor
	if duration == 0 {
		duration = 4 * time.Second
	}
	return m, tea.Tick(duration, func(time.Time) tea.Msg { return toastExpiredMsg(id) })
}

// keys are the requests view's keys; up, left and pageUp name their pairs in
// help.
var keys = struct {
	up, down, left, right, pageUp, pageDown, follow, replay, copy, export, test, help, quit key.Binding
}{
	up:       key.NewBinding(key.WithKeys("up"), key.WithHelp("↑↓", "select")),
	down:     key.NewBinding(key.WithKeys("down")),
	left:     key.NewBinding(key.WithKeys("left"), key.WithHelp("←→", "tabs")),
	right:    key.NewBinding(key.WithKeys("right")),
	pageUp:   key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup pgdn", "scroll the detail")),
	pageDown: key.NewBinding(key.WithKeys("pgdown")),
	follow:   key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "follow")),
	replay:   key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "replay")),
	copy:     key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "curl")),
	export:   key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "export")),
	test:     key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "test")),
	help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	quit:     key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
}

func (m Fullscreen) key(msg tea.KeyPressMsg) (Fullscreen, tea.Cmd) {
	if m.sources.open {
		return m.sourcesKey(msg)
	}
	i := m.selectedIndex()
	switch {
	case key.Matches(msg, keys.up):
		m = m.move(-1)
	case key.Matches(msg, keys.down):
		m = m.move(1)
	case key.Matches(msg, keys.left):
		m.tab = (m.tab + tabCount - 1) % tabCount
	case key.Matches(msg, keys.right):
		m.tab = (m.tab + 1) % tabCount
	case key.Matches(msg, keys.pageUp):
		m = m.scrollDetail(-1)
	case key.Matches(msg, keys.pageDown):
		m = m.scrollDetail(1)
	case key.Matches(msg, keys.follow):
		m.paused = false
	case key.Matches(msg, keys.replay):
		if m.Target == "" {
			return m.show(session.ErrNoTarget.Error())
		}
		if i >= 0 {
			requests, n := m.Requests, m.entries[i].Number
			return m, runReplay(func() error { return requests.Replay(n) })
		}
	case key.Matches(msg, keys.copy):
		if i >= 0 {
			return m, copyCurl(m.Requests, m.entries[i].Number, !m.ShowSensitiveHeaders)
		}
	case key.Matches(msg, keys.export):
		if i >= 0 {
			return m, exportFixture(m.Requests, m.entries[i].Number, !m.ShowSensitiveHeaders)
		}
	case key.Matches(msg, keys.test):
		return m, sendTest(m.Requests, m.testSource())
	case key.Matches(msg, keys.help):
		m.help = !m.help
	case key.Matches(msg, keys.quit):
		return m, Stop
	case key.Matches(msg, filterKey):
		m.filter.editing, m.filter.input = true, m.filter.text
	case key.Matches(msg, waitKey):
		return m.startWait(i)
	case key.Matches(msg, escKey):
		m = m.escape()
	case key.Matches(msg, sourcesKeys.open):
		m.sources.open = true
	}
	return m, nil
}

// move selects the request delta rows away and stops following the newest.
func (m Fullscreen) move(delta int) Fullscreen {
	shown := m.shown()
	if row := m.selectedRow(shown); row >= 0 {
		m.paused = true
		m.selected = m.entries[shown[min(max(0, row+delta), len(shown)-1)]].Number
	}
	return m
}

// selectedIndex is the selected entry's index, -1 when the list shows none.
func (m Fullscreen) selectedIndex() int {
	shown := m.shown()
	if row := m.selectedRow(shown); row >= 0 {
		return shown[row]
	}
	return -1
}

// testSource is where t sends a test event: the selected request's source,
// else the first one listened to.
func (m Fullscreen) testSource() string {
	if i := m.selectedIndex(); i >= 0 {
		if name := m.Listen.Sources[m.entries[i].Delivery.SourceUID]; name != "" {
			return name
		}
	}
	if len(m.Routes) > 0 {
		return m.Routes[0].Source
	}
	return ""
}

// firstRow is the list's first row: where it was, moved just enough to show
// the selection.
func (m Fullscreen) firstRow() int {
	rows := m.layout().rows
	shown := m.shown()
	selected := max(0, m.selectedRow(shown))
	first := min(max(m.offset, selected-rows+1), selected)
	return max(0, min(first, len(shown)-rows))
}

// layout is how the screen's lines and columns are shared.
type layout struct {
	split bool
	// panes is the height left to the list and the detail.
	panes                     int
	listWidth, listHeight     int
	detailWidth, detailHeight int
	// rows is how many requests the list shows.
	rows int
}

func (m Fullscreen) layout() layout {
	width, height := m.size()
	l := layout{split: width >= splitWidth}
	// The header, source line and filter come first; toasts and keys last.
	l.panes = max(0, height-2-len(m.filterLines(width))-len(m.toastLines(width))-len(m.footer(width)))
	if l.split {
		l.listWidth, l.listHeight = listWidth, l.panes
		l.detailWidth, l.detailHeight = width-listWidth-1, l.panes
	} else {
		l.listWidth, l.listHeight = width, min(l.panes, max(6, l.panes*2/5))
		l.detailWidth, l.detailHeight = width, l.panes-l.listHeight
	}
	// The list's borders and column titles take three lines.
	l.rows = max(0, l.listHeight-3)
	return l
}

// size is the terminal's, or the width rule's fallback until it's known.
func (m Fullscreen) size() (int, int) {
	if m.width == 0 {
		return cards.DefaultWidth, 24
	}
	return m.width, m.height
}

func (m Fullscreen) View() tea.View {
	if m.sources.open {
		return m.sourcesView()
	}
	width, height := m.size()
	l := m.layout()
	lines := []string{m.header(width), m.sourceLine()}
	lines = append(lines, m.filterLines(width)...)
	switch {
	case len(m.entries) == 0:
		empty := m.empty(width)
		lines = append(lines, empty[:min(len(empty), l.panes)]...)
		lines = append(lines, make([]string, max(0, l.panes-len(empty)))...)
	case l.split:
		list, detail := m.list(l.listWidth, l.panes), m.detail(l.detailWidth, l.panes)
		for i := range list {
			lines = append(lines, list[i]+" "+detail[i])
		}
	default:
		lines = append(lines, m.list(l.listWidth, l.listHeight)...)
		lines = append(lines, m.detail(l.detailWidth, l.detailHeight)...)
	}
	lines = append(lines, m.toastLines(width)...)
	lines = append(lines, m.footer(width)...)
	lines = lines[:min(len(lines), height)]
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "…")
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	return view
}

// header is the status line with the target and the clock at its right end.
func (m Fullscreen) header(width int) string {
	target := "→ terminal only"
	if m.Target != "" {
		target = "→ " + cards.Line(m.Target)
	}
	return cards.Status{State: m.state, Err: m.lost, Project: m.Project, Totals: m.totals, Hints: []string{target, m.clock().Format(time.TimeOnly)}}.Line(width)
}

func (m Fullscreen) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// sourceLine keys the sources' colors and says whether the selection follows
// the newest request.
func (m Fullscreen) sourceLine() string {
	var sources []string
	for i, route := range m.Routes {
		if i == 0 || m.Routes[i-1].SourceUID != route.SourceUID {
			sources = append(sources, sourceStyle(route.SourceUID).Render("●")+" "+cards.Line(route.Source))
		}
	}
	line := strings.Join(sources, "   ")
	switch i := m.selectedIndex(); {
	case i < 0:
	case m.paused:
		line += "   " + markStyle.Render("paused on #"+strconv.Itoa(m.entries[i].Number)) + faintStyle.Render(" · f follows newest")
	default:
		line += faintStyle.Render("   following newest")
	}
	return line
}

// empty shows the routes while no request has arrived, then the source
// warnings and, once listening, the test hint.
func (m Fullscreen) empty(width int) []string {
	lines := strings.Split(cards.Banner(m.Project, m.Routes, nil, width), "\n")
	lines = append(lines, wrap(width, m.notices)...)
	if m.hint != nil {
		// The heading, a blank line, then a curl per source. A cut or wrapped
		// curl can't be pasted, so ones too wide give way to the Sources page.
		hint := strings.Split(cards.TestHint(*m.hint, false), "\n")
		if slices.ContainsFunc(hint[2:], func(curl string) bool { return lipgloss.Width(curl) > width }) {
			hint = append(hint[:2], wrap(width, []string{faintStyle.Render("  The curls are wider than this screen; on Sources (s), c then 6 copies one.")})...)
		}
		lines = append(append(lines, ""), hint...)
		if source := m.testSource(); source != "" {
			lines = append(lines, "", cards.Badge("t")+" send a test event to "+boldStyle.Render(cards.Line(source)))
		}
	}
	return lines
}

func (m Fullscreen) toastLines(width int) []string {
	if m.toast == "" {
		return nil
	}
	return wrap(width, strings.Split(m.toast, "\n"))
}

var helpStyles = help.Styles{
	Ellipsis:       faintStyle,
	ShortKey:       boldStyle,
	ShortDesc:      faintStyle,
	ShortSeparator: faintStyle,
	FullKey:        boldStyle,
	FullDesc:       faintStyle,
	FullSeparator:  faintStyle,
}

// footer lists the keys on one line, or all of them in columns after ?.
func (m Fullscreen) footer(width int) []string {
	return helpView(keyMap{forwarding: m.Target != ""}, m.help, width)
}

// helpView lists the keys on one line, or with all set, all of them in
// columns.
func helpView(bindings help.KeyMap, all bool, width int) []string {
	h := help.New()
	h.ShowAll = all
	h.ShortSeparator = "  "
	h.Styles = helpStyles
	h.SetWidth(width)
	return strings.Split(h.View(bindings), "\n")
}

// keyMap lists the keys; nothing replays without --forward-to.
type keyMap struct{ forwarding bool }

func (k keyMap) ShortHelp() []key.Binding {
	actions := []key.Binding{keys.copy, keys.export, keys.test}
	if k.forwarding {
		actions = append([]key.Binding{keys.replay}, actions...)
	}
	// bubbles/help cuts the line where it runs out of room, so help and quit
	// come first.
	return slices.Concat([]key.Binding{keys.help, keys.quit, keys.up, keys.left, keys.follow, filterKey}, actions, []key.Binding{sourcesKeys.open})
}

func (k keyMap) FullHelp() [][]key.Binding {
	actions := []key.Binding{described(keys.copy, "copy as cURL"), described(keys.export, "export a fixture"), described(keys.test, "send a test event")}
	if k.forwarding {
		actions = append([]key.Binding{described(keys.replay, "replay locally"), waitKey}, actions...)
	}
	return [][]key.Binding{
		{described(keys.up, "select a request"), described(keys.left, "switch tabs"), keys.pageUp, described(keys.follow, "follow the newest"), described(filterKey, "filter the list")},
		actions,
		{described(sourcesKeys.open, "sources and routes"), described(keys.help, "close help"), described(keys.quit, "stop listening")},
	}
}

// described is b with a longer description, for the full help.
func described(b key.Binding, description string) key.Binding {
	b.SetHelp(b.Help().Key, description)
	return b
}

// list frames the requests, oldest first, from the offset row. A › marks the
// selected one, which also shows in reverse.
func (m Fullscreen) list(width, height int) []string {
	// The marker and a space come first.
	inner := max(0, width-6)
	columns := m.columns(inner)
	titles := make([]string, len(columns))
	for i, c := range columns {
		titles[i] = c.title
	}
	lines := []string{"  " + faintStyle.Render(render(columns, titles, nil))}
	selected := m.selectedIndex()
	shown := m.shown()
	start := min(m.offset, len(shown))
	for _, i := range shown[start:min(len(shown), start+max(0, height-3))] {
		texts, styles := make([]string, len(columns)), make([]lipgloss.Style, len(columns))
		for j, c := range columns {
			texts[j], styles[j] = c.value(m.entries[i])
		}
		if i == selected {
			lines = append(lines, "› "+selectedStyle.Render(pad(render(columns, texts, nil), inner)))
		} else {
			lines = append(lines, "  "+render(columns, texts, styles))
		}
	}
	if len(shown) == 0 {
		lines = append(lines, "  "+faintStyle.Render("no request matches · esc clears the filter"))
	}
	title, count := m.listTitle(len(shown))
	return panel(title, count, width, height, lines)
}

// column is one of the request list's columns.
type column struct {
	title string
	width int
	right bool
	value func(session.Entry) (string, lipgloss.Style)
}

// columns fit the list into width: the path gets the room left, and the time
// and method columns go when it would get too little.
func (m Fullscreen) columns(width int) []column {
	number := 1
	if n := len(m.entries); n > 0 {
		number = len(strconv.Itoa(m.entries[n-1].Number))
	}
	source := len("SOURCE")
	for _, name := range m.Listen.Sources {
		source = min(12, max(source, lipgloss.Width(cards.Line(name))))
	}
	columns := []column{
		{title: "#", width: number, right: true, value: func(e session.Entry) (string, lipgloss.Style) {
			return strconv.Itoa(e.Number), faintStyle
		}},
		{title: "TIME", width: 8, value: func(e session.Entry) (string, lipgloss.Style) {
			return e.Received.Format(time.TimeOnly), faintStyle
		}},
		{title: "SOURCE", width: source, value: func(e session.Entry) (string, lipgloss.Style) {
			return cards.Line(m.sourceName(e.Delivery.SourceUID)), sourceStyle(e.Delivery.SourceUID)
		}},
		{title: "METHOD", width: 6, value: func(e session.Entry) (string, lipgloss.Style) {
			return cards.Line(method(e)), lipgloss.Style{}
		}},
		{title: "PATH", value: func(e session.Entry) (string, lipgloss.Style) {
			return cards.Line(e.Delivery.Path), lipgloss.Style{}
		}},
	}
	if m.Target != "" {
		columns = append(columns,
			column{title: "STATUS", width: 7, value: outcome},
			column{title: "LATENCY", width: 7, right: true, value: latency},
		)
	}
	columns = append(columns, column{width: 4, value: mark})

	// The path's width is still zero, so room is what it would get.
	room := func() int { return width - rowWidth(columns) }
	for _, title := range []string{"TIME", "METHOD"} {
		if room() >= minPath {
			break
		}
		columns = slices.DeleteFunc(columns, func(c column) bool { return c.title == title })
	}
	path := slices.IndexFunc(columns, func(c column) bool { return c.title == "PATH" })
	columns[path].width = max(0, room())
	return columns
}

// render lines up texts in columns, styling each by styles when given.
func render(columns []column, texts []string, styles []lipgloss.Style) string {
	cells := make([]string, len(columns))
	for i, c := range columns {
		text := ansi.Truncate(texts[i], c.width, "…")
		gap := strings.Repeat(" ", max(0, c.width-lipgloss.Width(text)))
		if c.right {
			text = gap + text
		} else {
			text += gap
		}
		if styles != nil {
			text = styles[i].Render(text)
		}
		cells[i] = text
	}
	return strings.Join(cells, "  ")
}

// outcome is a forwarded request's status, or its transport failure in a
// word.
func outcome(e session.Entry) (string, lipgloss.Style) {
	if e.Failure == nil {
		return strconv.Itoa(e.Response.Status), lipgloss.NewStyle().Foreground(cards.StatusColor(e.Response.Status))
	}
	switch e.Failure.Kind {
	case proxy.TransportConnectionRefused:
		return "refused", errorStyle
	case proxy.TransportTimeout:
		return "timeout", errorStyle
	case proxy.TransportDNS:
		return "DNS", errorStyle
	case proxy.TransportTLS:
		return "TLS", errorStyle
	default:
		return "failed", errorStyle
	}
}

// latency is how long the target took, when that's known.
func latency(e session.Entry) (string, lipgloss.Style) {
	if !e.Timed() {
		return "—", faintStyle
	}
	return cards.FormatLatency(e.Latency), faintStyle
}

func mark(e session.Entry) (string, lipgloss.Style) {
	switch {
	case e.ReplayOf != 0:
		return "↻", markStyle
	case e.Test:
		return "test", faintStyle
	}
	return "", lipgloss.Style{}
}

func (m Fullscreen) sourceName(uid string) string {
	if name := m.Listen.Sources[uid]; name != "" {
		return name
	}
	return "unknown"
}

func sourceStyle(uid string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(cards.SourceColor(uid))
}

func method(e session.Entry) string {
	if e.Delivery.Method == "" {
		return "POST"
	}
	return e.Delivery.Method
}

// panel frames lines in a box width × height, with title and label in its
// top border. Lines are cut to fit; those past its height give way to a count
// of the ones left out.
func panel(title, label string, width, height int, lines []string) []string {
	inner := width - 4
	if height < 2 || inner < 1 {
		return make([]string, max(0, height))
	}
	body := height - 2
	if len(lines) > body {
		more := faintStyle.Render(fmt.Sprintf("… %d more lines", len(lines)-body+1))
		lines = append(lines[:max(0, body-1):max(0, body-1)], more)[:body]
	}

	if label != "" {
		label = " " + label + " "
	}
	// "╭─ " title " " fill label "─╮"
	fill := width - 6 - lipgloss.Width(title) - lipgloss.Width(label)
	if fill < 1 {
		label = ""
		title = ansi.Truncate(title, max(0, width-7), "…")
		fill = width - 6 - lipgloss.Width(title)
	}
	framed := []string{faintStyle.Render("╭─ ") + title + faintStyle.Render(" "+strings.Repeat("─", max(0, fill))) + label + faintStyle.Render("─╮")}
	side := faintStyle.Render("│")
	for i := range body {
		line := ""
		if i < len(lines) {
			line = ansi.Truncate(lines[i], inner, "…")
		}
		framed = append(framed, side+" "+pad(line, inner)+" "+side)
	}
	return append(framed, faintStyle.Render("╰"+strings.Repeat("─", width-2)+"╯"))
}

// pad fills s, which may be styled, to width columns.
func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(0, width-lipgloss.Width(s)))
}

// wrap breaks prose lines at width.
func wrap(width int, lines []string) []string {
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, strings.Split(lipgloss.Wrap(line, max(1, width), ""), "\n")...)
	}
	return wrapped
}

// FullscreenSink hands every event to the full-screen model. Nothing prints:
// Println shows nothing on the alt screen.
func (p *Program) FullscreenSink() session.Sink {
	return fullscreenSink{program: p}
}

type fullscreenSink struct {
	program *Program
}

func (s fullscreenSink) Emit(event session.Event) error {
	s.program.Send(event)
	return nil
}
