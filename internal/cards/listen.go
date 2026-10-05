package cards

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"hookspot/internal/proxy"
	"hookspot/internal/session"
	"hookspot/internal/ws"
)

// BannerRoute is one route listen delivers through.
type BannerRoute struct {
	SourceUID string
	Source    string
	PublicURL string
	RouteUID  string
	// Path is the route's destination path.
	Path string
	// Destination is the URL the route forwards to; "" in inspect mode.
	Destination string
	Label       string
}

// Banner opens listen: the project, then one line per route, with key hints
// in the bottom border. Routes of one source must be adjacent. When a route
// doesn't fit on one line, every route moves below its source. Connection
// state follows the box rather than living in it.
func Banner(project string, routes []BannerRoute, hints []string, width int) string {
	inner := innerWidth(width)
	sources := map[string]bool{}
	nameWidth, urlWidth := 0, 0
	for _, route := range routes {
		sources[route.SourceUID] = true
		nameWidth = max(nameWidth, lipgloss.Width(Line(route.Source)))
		urlWidth = max(urlWidth, lipgloss.Width(Line(route.PublicURL)))
	}

	leads := make([]string, len(routes))
	targets := make([]string, len(routes))
	fits := true
	for i, route := range routes {
		if i == 0 || routes[i-1].SourceUID != route.SourceUID {
			leads[i] = Pad(SourceStyle(route.SourceUID).Render(Line(route.Source)), nameWidth) + "  " + faintStyle.Render(Line(route.PublicURL))
		}
		destination := "terminal only"
		if route.Destination != "" {
			destination = Line(route.Destination)
		}
		targets[i] = faintStyle.Render("→") + " " + destination
		// An unnamed route's label is its path, which a forwarding destination already ends with.
		if label := Line(route.Label); label != "" && (route.Label != route.Path || route.Destination == "") {
			targets[i] += "  " + faintStyle.Render(label)
		}
		fits = fits && nameWidth+2+urlWidth+2+lipgloss.Width(targets[i]) <= inner
	}

	var lines []string
	for i := range routes {
		switch {
		case fits:
			lines = append(lines, Pad(leads[i], nameWidth+2+urlWidth)+"  "+targets[i])
		case leads[i] != "":
			lines = append(lines, leads[i], "  "+targets[i])
		default:
			lines = append(lines, "  "+targets[i])
		}
	}

	title := "Listening in " + boldStyle.Render(Line(project))
	label := faintStyle.Render(Count(len(sources), "source") + " • " + Count(len(routes), "route"))
	footer := ""
	if len(hints) > 0 {
		footer = faintStyle.Render(strings.Join(hints, " · "))
	}
	return frame(faintStyle, inner, title, label, footer, section{lines: lines})
}

// The connection states and notices keep the wording plain mode has always
// printed, so stripping their color leaves exactly that text.

// Connecting is shown until the first join.
func Connecting() string {
	return faintStyle.Render("Connecting…")
}

// Ready follows the first join. Harnesses wait for its plain text.
func Ready() string {
	return okStyle.Bold(true).Render("Ready.") + " Waiting for requests " + faintStyle.Render("(Ctrl-C to quit)")
}

// ConnectionLost reports a failed session and the coming retry.
func ConnectionLost(err error, retryIn time.Duration) string {
	return warnStyle.Render("connection lost:") + " " + Line(err.Error()) + faintStyle.Render(fmt.Sprintf("; reconnecting in %s...", retryIn.Round(100*time.Millisecond)))
}

// Reconnected follows every join after the first. Requests that arrived while
// offline are never retried, so it says where to retry them.
func Reconnected(offline time.Duration, requestsURL string) string {
	return okStyle.Render("Reconnected") + fmt.Sprintf(" after %s offline. ", offline.Round(time.Second)) +
		faintStyle.Render("Requests that arrived meanwhile were not delivered; retry them from ") + Line(requestsURL)
}

// State is what a listen run's connection is doing.
type State int

const (
	StateConnecting State = iota
	StateLive
	StateOffline
	StateStopping
	StateStopped
)

// Status is the terminal stream's status line.
type Status struct {
	State State
	// Err is why the connection dropped, while offline.
	Err     error
	Project string
	Totals  session.Stats
	Hints   []string
	// Alert takes the hints' place where it fits.
	Alert Alert
}

// Render draws the status at width: state, project, counts and p50, then the
// alert, or the hints while they fit.
func (s Status) Render(width int) string {
	separator := faintStyle.Render(" · ")
	parts := []string{s.state(), boldStyle.Render(Line(s.Project)), Count(s.Totals.Count, "request")}
	if t := s.Totals; t.OK+t.Failed > 0 {
		failed := strconv.Itoa(t.Failed) + " failed"
		if t.Failed > 0 {
			failed = errorStyle.Render(failed)
		}
		// One part, so a line that gives way never shows one without the
		// other.
		parts = append(parts, okStyle.Render(strconv.Itoa(t.OK)+" ok")+separator+failed)
	}
	// Max is zero until a request got a response or timed out.
	if s.Totals.Max > 0 {
		parts = append(parts, "p50 "+FormatLatency(s.Totals.P50))
	}
	if s.State == StateOffline && s.Err != nil {
		parts = append(parts, faintStyle.Render(Line(s.Err.Error())))
	}
	// The details after the request count, parts[3:], give way whole to the
	// alert, as the full-screen keys do; then the count, but only to its
	// short form. The state and project stay, and offline, so does the
	// reason.
	kept := 3
	if s.State == StateOffline {
		kept = len(parts)
	}
	alert := s.Alert.fit(width - lipgloss.Width(strings.Join(parts[:kept], separator)) - endGap)
	if alert == "" && s.State != StateOffline {
		kept = 2
		alert = s.Alert.shrunk(width - lipgloss.Width(strings.Join(parts[:kept], separator)) - endGap)
	}
	if alert == "" {
		return withHints(strings.Join(parts, separator), s.Hints, width)
	}
	// Then as many details as fit come back.
	for kept < len(parts) && lipgloss.Width(strings.Join(parts[:kept+1], separator))+endGap+lipgloss.Width(alert) <= width {
		kept++
	}
	return AtRightEnd(strings.Join(parts[:kept], separator), alert, width)
}

func (s Status) state() string {
	switch s.State {
	case StateLive:
		return okStyle.Render("●") + " live"
	case StateOffline:
		return warnStyle.Render("○") + " reconnecting"
	case StateStopping:
		return faintStyle.Render("◌ stopping…")
	case StateStopped:
		return faintStyle.Render("■ stopped")
	default:
		return faintStyle.Render("○ connecting…")
	}
}

// Prompt is the › prompt with the typed input and a block cursor, then hints
// at the right end while they fit.
func Prompt(input string, hints []string, width int) string {
	return withHints(faintStyle.Render("›")+" "+Line(input)+cursorStyle.Render(" "), hints, width)
}

// withHints puts hints at the right end of line while they fit, then cuts the
// line to width.
func withHints(line string, hints []string, width int) string {
	end := ""
	if len(hints) > 0 {
		end = faintStyle.Render(strings.Join(hints, " · "))
	}
	return AtRightEnd(line, end, width)
}

// endGap is the fewest columns between a line and what ends it.
const endGap = 2

// AtRightEnd puts end at the right end of line when it fits, then cuts the
// line to width.
func AtRightEnd(line, end string, width int) string {
	if gap := width - lipgloss.Width(line) - lipgloss.Width(end); end != "" && gap >= endGap {
		line += strings.Repeat(" ", gap) + end
	}
	return truncate(line, width)
}

// Alert is what listen keeps at the right end of a line, in its widest form
// that fits: the update alert. Unlike the full-screen view's alerts, esc
// never dismisses it.
type Alert struct{ whole, short string }

// UpdateAlert names the newer release and how to upgrade to it, or only that
// an update is available where that doesn't fit.
func UpdateAlert(update session.UpdateAvailable) Alert {
	return Alert{
		whole: okStyle.Render("↑ "+Line(update.Latest)) + faintStyle.Render(" · ") + Line(update.Upgrade),
		short: okStyle.Render("↑ update available"),
	}
}

// Claim fits the alert at the right end of a width-column line that keeps
// its first columns: it returns the alert, "" when none fits, and the room
// left before it.
func (a Alert) Claim(width, keep int) (string, int) {
	alert := a.fit(width - keep - endGap)
	if alert == "" {
		return "", width
	}
	return alert, width - lipgloss.Width(alert) - endGap
}

// shrunk is the alert's short form within width columns, or "".
func (a Alert) shrunk(width int) string {
	if lipgloss.Width(a.short) > width {
		return ""
	}
	return a.short
}

// fit is the alert's widest form within width columns, or "".
func (a Alert) fit(width int) string {
	for _, text := range []string{a.whole, a.short} {
		if lipgloss.Width(text) <= width {
			return text
		}
	}
	return ""
}

// DisabledSource warns that a listened source rejects its requests.
func DisabledSource(name string) string {
	return warnStyle.Render("⚠") + " " + boldStyle.Render(Line(name)) + " is disabled: requests to it are rejected. Enable it in the dashboard."
}

// SkippedSource warns that a named source has no route to listen to.
func SkippedSource(name string) string {
	return warnStyle.Render("⚠") + " " + boldStyle.Render(Line(name)) + " has no route and is skipped. Add one in the dashboard."
}

// RootNotFound hints, after a 404 or 405 at the bare --forward-to root, that
// the app's webhook route is probably elsewhere.
func RootNotFound(root string, status int) string {
	root = Line(root)
	return boldStyle.Render(root) + " returned " + errorStyle.Render(strconv.Itoa(status)) +
		". If your webhook route is elsewhere, include it in --forward-to, e.g. --forward-to " + root + "webhooks"
}

// TestHint offers a test event while no request has arrived: t when commands
// is set, and each source's curl, which works from anywhere. The curls are
// never cut, so they can be copied whole.
func TestHint(hint session.TestHint, commands bool) string {
	several := len(hint.Sources) > 1
	lines := []string{"No requests yet. Check the whole path with a test event:", ""}
	if commands {
		keys := make([]string, len(hint.Sources))
		keyWidth := 0
		for i, source := range hint.Sources {
			keys[i] = "t"
			if several {
				keys[i] += " " + Line(source.Name)
			}
			keys[i] = Badge(keys[i])
			keyWidth = max(keyWidth, lipgloss.Width(keys[i]))
		}
		for i, source := range hint.Sources {
			lines = append(lines, "  "+Pad(keys[i], keyWidth)+"  send a test event to "+boldStyle.Render(Line(source.Name)))
		}
		lines = append(lines, "", faintStyle.Render("  or from anywhere:"))
	}
	for _, source := range hint.Sources {
		curl := "  " + Line(session.TestCurl(source.URL))
		if several {
			curl += faintStyle.Render("  # " + Line(source.Name))
		}
		lines = append(lines, curl)
	}
	return strings.Join(lines, "\n")
}

// PathWorks follows a test event's delivery with the path it proved: through
// to the local target when that answered.
func PathWorks(target string, failure *proxy.TransportFailure, width int) string {
	path := "hookspot → this terminal"
	if target != "" && failure == nil {
		path += " → " + Line(target)
	}
	return truncate(okStyle.Render("✓")+" path works: "+path, width)
}

// ClipboardNote says where copying to the clipboard works.
const ClipboardNote = "copying needs a terminal with OSC 52 (Terminal.app has none)"

// CurlNotes says what request n's cURL command does and what it leaves out;
// copied is set when the full command went to the clipboard.
func CurlNotes(n int, c session.Curl, copied bool) string {
	note := "#" + strconv.Itoa(n) + " as cURL"
	if copied {
		note = "copied " + note
	}
	if c.Resend {
		note += ", which resends it through Hookspot"
	}
	notes := []string{note}
	if copied {
		notes = append(notes, ClipboardNote)
	}
	if c.Redacted {
		notes = append(notes, "sensitive headers are hidden; --show-sensitive-headers shows the full command")
	}
	if c.HeadersFile != "" {
		notes = append(notes, Line(c.HeadersFile)+" holds unredacted headers, which may contain credentials")
	}
	return strings.Join(notes, "\n")
}

// Exported confirms request n's export as a fixture.
func Exported(n int, f session.Fixture) string {
	exported := "exported #" + strconv.Itoa(n) + " to " + Line(f.JSON) + " and " + Line(f.Body)
	if f.Redacted {
		exported += " · sensitive headers redacted"
	}
	return exported
}

// Limits caps what request cards print; zero means unlimited.
type Limits struct {
	MaxBodyLines  int
	MaxHeaders    int
	MaxValueChars int
}

// Listen renders one listen run's requests.
type Listen struct {
	// Sources names sources by UID.
	Sources              map[string]string
	Limits               Limits
	ShowSensitiveHeaders bool
	// Container is set inside a container, where localhost is the container
	// itself.
	Container bool
}

// Entry renders a replay as its summary and response diff, e as a one-line
// row when forwarding answered 2xx, and as a card otherwise and in inspect
// mode. A test event's delivery is followed by the path it proved.
func (l Listen) Entry(e session.Entry, width int) string {
	text := l.render(e, width)
	if e.Test {
		text += "\n" + PathWorks(e.Target, e.Failure, width)
	}
	return text
}

func (l Listen) render(e session.Entry, width int) string {
	inner := innerWidth(width)
	switch {
	case e.Replay != nil:
		return l.replay(e, width)
	case e.Target == "":
		return l.inspect(e, inner)
	case e.Failure != nil:
		return l.transportFailure(e, inner)
	case !e.Failed():
		return l.row(e, width)
	default:
		return l.httpFailure(e, inner)
	}
}

func (l Listen) row(e session.Entry, width int) string {
	d := e.Delivery
	// Badges are padded, so one space sets them apart. Whoever posts to a
	// source picks the method, so one longer than OPTIONS is cut.
	const methodWidth, maxMethod = 6, len("OPTIONS")
	left := Pad(Badge(truncate(Line(session.Method(d)), maxMethod)), methodWidth) + " "
	if e.Number > 0 {
		left = faintStyle.Render(fmt.Sprintf("#%-3d", e.Number)) + left
	}
	right := ColorBadge(StatusColor(e.Response.Status), strconv.Itoa(e.Response.Status)) + " " +
		faintStyle.Render(fmt.Sprintf("%5s", FormatLatency(e.Latency))+"  "+Timestamp(e))
	if badge := marks(e); badge != "" {
		right = badge + " " + right
	}

	// The path keeps a few columns even when that overflows width; a long
	// source name gives way first.
	const minPath, minSummary = 12, 8
	sourceWidth := min(l.sourceWidth(), max(lipgloss.Width(unknownSource), width-lipgloss.Width(left)-lipgloss.Width(right)-minPath-4))
	left += Pad(truncate(l.Source(d.SourceUID), sourceWidth), sourceWidth) + "  "
	room := max(minPath, width-lipgloss.Width(left)-lipgloss.Width(right)-2)
	path, summary := Line(d.Path), l.Summary(d)
	if summaryRoom := room - lipgloss.Width(path) - 2; lipgloss.Width(summary) <= summaryRoom || summaryRoom >= minSummary {
		left += path + "  " + faintStyle.Render(truncate(summary, summaryRoom))
	} else {
		left += truncate(path, room)
	}
	return left + strings.Repeat(" ", max(2, width-lipgloss.Width(left)-lipgloss.Width(right))) + right
}

func (l Listen) httpFailure(e session.Entry, inner int) string {
	d, response := e.Delivery, e.Response
	responseLines := append(RedirectHint(response), l.Body(response.Body, response.Headers)...)

	border := lipgloss.NewStyle().Foreground(StatusColor(response.Status))
	return frame(border, inner, l.title(e), cardLabel(e), "",
		section{lines: outcome(ColorBadge(StatusColor(response.Status), session.StatusText(response.Status)), e, inner)},
		section{title: faintStyle.Render("request · " + BodyTitle(d.Body, d.Headers)), lines: l.Body(d.Body, d.Headers)},
		section{title: faintStyle.Render("response · " + BodyTitle(response.Body, response.Headers)), lines: responseLines},
	)
}

func (l Listen) transportFailure(e session.Entry, inner int) string {
	lines := append(outcome(ColorBadge(StatusColor(0), "✗ "+TransportLabel(e.Failure.Kind)), e, inner), "")
	lines = append(lines, l.TransportHints(e.Target, e.Failure)...)
	return frame(errorStyle, inner, l.title(e), cardLabel(e), "", section{lines: lines})
}

// RedirectHint follows a 3xx response that names a Location: webhook senders
// stop there.
func RedirectHint(response ws.Response) []string {
	location := session.HeaderValue(response.Headers, "Location")
	if response.Status < 300 || response.Status >= 400 || location == "" {
		return nil
	}
	return []string{
		faintStyle.Render("Location:") + " " + Line(location),
		warnStyle.Render("webhook senders don't follow redirects; point --forward-to at the final URL"),
	}
}

// TransportHints say what a transport failure to target means and what to
// check; inside a container, a refused localhost is the container itself.
func (l Listen) TransportHints(target string, failure *proxy.TransportFailure) []string {
	hints := []string{l.transportHint(target, failure)}
	if failure.Kind == proxy.TransportConnectionRefused && l.Container && localhostTarget(target) {
		hints = append(hints, warnStyle.Render("inside a container, localhost is the container itself; use the service name (http://app:3000) or host.docker.internal"))
	}
	return hints
}

func (l Listen) inspect(e session.Entry, inner int) string {
	d := e.Delivery
	var first []string
	query := ""
	if d.Query != "" {
		query = faintStyle.Render("query") + "  " + l.query(d.Query)
	}
	if query != "" || d.RequestUID != "" {
		first = spread(query, faintStyle.Render(Line(d.RequestUID)), inner)
	}
	headers := "none"
	if len(d.Headers) > 0 {
		headers = strconv.Itoa(len(d.Headers))
	}
	return frame(faintStyle, inner, l.title(e), cardLabel(e), "",
		section{lines: first},
		section{title: faintStyle.Render("headers · " + headers), lines: l.Headers(d.Headers)},
		section{title: faintStyle.Render("body · " + BodyTitle(d.Body, d.Headers)), lines: l.Body(d.Body, d.Headers)},
	)
}

// replay sums up a replay as "#46 ↻ #45  422 → 200  9ms → 41ms", then shows
// how its response differs from the original's.
func (l Listen) replay(e session.Entry, width int) string {
	c := e.Replay
	summary := faintStyle.Render("#"+strconv.Itoa(e.Number)) + " " + warnStyle.Render("↻") + " " + faintStyle.Render("#"+strconv.Itoa(c.Original)) + "  " +
		result(c.Status, c.Failure) + faintStyle.Render(" → ") + result(e.Response.Status, e.Failure) + "  " +
		faintStyle.Render(FormatLatency(c.Latency)+" → "+FormatLatency(e.Latency))
	right := faintStyle.Render(Timestamp(e))
	const minTitle = 12
	room := max(minTitle, width-lipgloss.Width(summary)-lipgloss.Width(right)-4)
	// The title leaves out the number the summary opens with.
	left := summary + "  " + truncate(l.title(session.Entry{Delivery: e.Delivery}), room)
	lines := []string{left + strings.Repeat(" ", max(2, width-lipgloss.Width(left)-lipgloss.Width(right))) + right}

	for _, line := range c.Removed {
		lines = append(lines, truncate("  "+errorStyle.Render("- "+l.bodyLine(line)), width))
	}
	for _, line := range c.Added {
		lines = append(lines, truncate("  "+okStyle.Render("+ "+l.bodyLine(line)), width))
	}
	if c.More > 0 {
		lines = append(lines, "  "+faintStyle.Render("… "+Count(c.More, "more changed line")))
	}
	if c.Binary {
		lines = append(lines, "  "+faintStyle.Render("binary body "+formatBytes(c.Size)+" → "+formatBytes(len(e.Response.Body))))
	}
	if e.Failure != nil {
		for _, hint := range l.TransportHints(e.Target, e.Failure) {
			for _, line := range strings.Split(lipgloss.Wrap(hint, max(1, width-2), ""), "\n") {
				lines = append(lines, "  "+line)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// result is what forwarding got: a status, or the transport failure.
func result(status int, failure *proxy.TransportFailure) string {
	if failure != nil {
		return errorStyle.Render(TransportLabel(failure.Kind))
	}
	return lipgloss.NewStyle().Foreground(StatusColor(status)).Render(strconv.Itoa(status))
}

// title names a card's request: number, source, method and path.
func (l Listen) title(e session.Entry) string {
	title := l.Source(e.Delivery.SourceUID) + faintStyle.Render(" · ") + Line(session.Method(e.Delivery)) + " " + Line(e.Delivery.Path)
	if e.Number > 0 {
		title = faintStyle.Render("#"+strconv.Itoa(e.Number)) + " " + title
	}
	return title
}

// outcome opens a forwarding card: what forwarding did, how long it took and
// where it went, then the request ID.
func outcome(status string, e session.Entry, inner int) []string {
	return spread(status+" "+faintStyle.Render(FormatLatency(e.Latency)+"  → "+Line(e.Target)), faintStyle.Render(Line(e.Delivery.RequestUID)), inner)
}

// cardLabel ends a card's top border: its marks and the time it arrived.
func cardLabel(e session.Entry) string {
	label := faintStyle.Render(Timestamp(e))
	if badge := marks(e); badge != "" {
		label = badge + "  " + label
	}
	return label
}

func marks(e session.Entry) string {
	if e.Test {
		return Badge("test")
	}
	return ""
}

// unknownSource names a source this run doesn't listen to.
const unknownSource = "unknown"

// Source is a source's name in its color.
func (l Listen) Source(uid string) string {
	return SourceStyle(uid).Render(Line(l.SourceName(uid)))
}

// SourceName names source uid; callers escape it with Line.
func (l Listen) SourceName(uid string) string {
	if name := l.Sources[uid]; name != "" {
		return name
	}
	return unknownSource
}

// sourceWidth aligns the rows' source column.
func (l Listen) sourceWidth() int {
	width := lipgloss.Width(unknownSource)
	for _, name := range l.Sources {
		width = max(width, lipgloss.Width(Line(name)))
	}
	return width
}

// SourceStyle colors text in uid's SourceColor.
func SourceStyle(uid string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(SourceColor(uid))
}

// Summary names a delivery in its row: its event type when the body has one,
// else its media type and size.
func (l Listen) Summary(d ws.Delivery) string {
	var object map[string]json.RawMessage
	if json.Unmarshal(d.Body, &object) == nil {
		for _, key := range []string{"type", "event", "event_type", "action"} {
			var value string
			if raw, ok := object[key]; ok && json.Unmarshal(raw, &value) == nil && value != "" {
				return l.value(Line(value))
			}
		}
	}
	return BodyTitle(d.Body, d.Headers)
}

// Headers lists headers sorted by name, redacting sensitive values unless
// ShowSensitiveHeaders and applying --max-headers.
func (l Listen) Headers(headers http.Header) []string {
	keys := slices.SortedFunc(maps.Keys(headers), func(a, b string) int {
		return cmp.Or(strings.Compare(strings.ToLower(a), strings.ToLower(b)), strings.Compare(a, b))
	})
	keys = keys[:limit(len(keys), l.Limits.MaxHeaders)]

	nameWidth := 0
	for _, key := range keys {
		nameWidth = max(nameWidth, lipgloss.Width(strings.ToLower(Line(key))))
	}
	lines := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		value := Badge("hidden") + " " + faintStyle.Render("--show-sensitive-headers to reveal")
		if !session.SensitiveHeader(key) || l.ShowSensitiveHeaders {
			values := make([]string, len(headers[key]))
			for i, v := range headers[key] {
				values[i] = l.value(Line(v))
			}
			value = strings.Join(values, ", ")
		}
		lines = append(lines, Pad(keyStyle.Render(strings.ToLower(Line(key))), nameWidth)+"  "+value)
	}
	if omitted := len(headers) - len(keys); omitted > 0 {
		lines = append(lines, faintStyle.Render("… "+Count(omitted, "more header")))
	}
	return lines
}

// Body lists a body's lines, pretty-printing and highlighting JSON; a binary
// body has none.
func (l Listen) Body(body []byte, headers http.Header) []string {
	lines, omitted, isJSON := bodyLines(body, bodyMIME(headers, body), l.Limits.MaxBodyLines)
	out := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		line = l.bodyLine(line)
		if isJSON {
			line = highlightJSON(line)
		}
		out = append(out, line)
	}
	if omitted > 0 {
		out = append(out, faintStyle.Render("… "+Count(omitted, "more line")))
	}
	return out
}

// bodyLine escapes one body line, applies --max-value-chars and expands tabs.
// Escaping only lengthens a line, so it's cut a rune past the limit first,
// which still earns the "…".
func (l Listen) bodyLine(line string) string {
	if most := l.Limits.MaxValueChars; most > 0 {
		line, _ = runePrefix(line, most+1)
	}
	return expandTabs(l.value(Sanitize(line)))
}

func (l Listen) query(raw string) string {
	parts := strings.Split(raw, "&")
	for i, part := range parts {
		key, value, found := strings.Cut(part, "=")
		parts[i] = Line(key)
		if found {
			parts[i] += "=" + l.value(Line(value))
		}
	}
	return strings.Join(parts, "&")
}

func (l Listen) transportHint(target string, failure *proxy.TransportFailure) string {
	target = Line(target)
	switch failure.Kind {
	case proxy.TransportConnectionRefused:
		return target + " is not reachable — is your server running?"
	case proxy.TransportTimeout:
		return target + " timed out — check that your server responds within " + proxy.ForwardTimeout.String()
	case proxy.TransportDNS:
		host := "the hostname"
		if parsed, err := url.Parse(target); err == nil && parsed.Hostname() != "" {
			host = parsed.Hostname()
		}
		return target + " could not be resolved (" + host + ") — check the hostname"
	case proxy.TransportTLS:
		return "TLS connection to " + target + " failed — check its certificate and HTTPS configuration"
	default:
		detail := "transport error"
		if failure.Err != nil {
			detail = l.value(Line(failure.Err.Error()))
		}
		return target + " failed: " + detail + " — check the target URL and server logs"
	}
}

// value applies --max-value-chars.
func (l Listen) value(value string) string {
	if prefix, cut := runePrefix(value, l.Limits.MaxValueChars); cut {
		return prefix + "…"
	}
	return value
}

// runePrefix is text's first n runes, and whether that left any out; n <= 0
// keeps all of it.
func runePrefix(text string, n int) (string, bool) {
	if n <= 0 {
		return text, false
	}
	runes := 0
	for i := range text {
		if runes == n {
			return text[:i], true
		}
		runes++
	}
	return text, false
}

// limit applies a --max-* limit to n items.
func limit(n, most int) int {
	if most > 0 && n > most {
		return most
	}
	return n
}

// highlightJSON colors one line of indented JSON, which may be cut short.
func highlightJSON(line string) string {
	var out strings.Builder
	for i := 0; i < len(line); {
		switch c := line[i]; {
		case c == ' ':
			out.WriteByte(c)
			i++
		case strings.IndexByte("{}[],:", c) >= 0:
			out.WriteString(faintStyle.Render(string(c)))
			i++
		case c == '"':
			end := i + 1
			for end < len(line) && line[end] != '"' {
				if line[end] == '\\' {
					end++
				}
				end++
			}
			end = min(end+1, len(line))
			style := stringStyle
			if strings.HasPrefix(strings.TrimLeft(line[end:], " "), ":") {
				style = keyStyle
			}
			out.WriteString(style.Render(line[i:end]))
			i = end
		default:
			end := i + 1
			for end < len(line) && !strings.ContainsRune(` ,:{}[]"`, rune(line[end])) {
				end++
			}
			out.WriteString(literalStyle.Render(line[i:end]))
			i = end
		}
	}
	return out.String()
}

// spread puts left and right at the ends of a width-column line, or on lines
// of their own when they don't fit.
func spread(left, right string, width int) []string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	switch {
	case right == "":
		return []string{left}
	case gap < 2:
		return []string{left, right}
	default:
		return []string{left + strings.Repeat(" ", gap) + right}
	}
}

// Pad fills s, which may be styled, to width columns.
func Pad(s string, width int) string {
	return s + strings.Repeat(" ", max(0, width-lipgloss.Width(s)))
}

// expandTabs turns tabs into spaces, since a tab would break a box's width.
func expandTabs(s string) string {
	return strings.ReplaceAll(s, "\t", "    ")
}

// Count is n of noun, plural unless n is 1.
func Count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// Timestamp is when e arrived, to the millisecond.
func Timestamp(e session.Entry) string {
	return e.Received.Format("15:04:05.000")
}

func localhostTarget(target string) bool {
	parsed, err := url.Parse(target)
	return err == nil && slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, parsed.Hostname())
}

// BodyTitle describes a body by media type and size.
func BodyTitle(body []byte, headers http.Header) string {
	if len(body) == 0 {
		return "empty"
	}
	return Line(bodyMIME(headers, body)) + " · " + formatBytes(len(body))
}

func bodyMIME(headers http.Header, body []byte) string {
	contentType := session.HeaderValue(headers, "Content-Type")
	if contentType != "" {
		if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
			return strings.ToLower(mediaType)
		}
		before, _, _ := strings.Cut(contentType, ";")
		if contentType = strings.TrimSpace(before); contentType != "" {
			return strings.ToLower(contentType)
		}
	}
	if len(body) > 0 {
		if mediaType, _, err := mime.ParseMediaType(http.DetectContentType(body)); err == nil {
			return strings.ToLower(mediaType)
		}
	}
	return "application/octet-stream"
}

// bodyLines splits a body for display, up to most lines (0 for all) and a
// count of the rest: indented JSON, or text when the media type is textual
// and the bytes are UTF-8. Anything else has no lines.
func bodyLines(body []byte, mimeType string, most int) (lines []string, omitted int, isJSON bool) {
	if len(body) == 0 {
		return nil, 0, false
	}
	text, isJSON := session.BodyText(body)
	if !isJSON && (!textualMIME(mimeType) || !utf8.Valid(body)) {
		return nil, 0, false
	}
	if most <= 0 {
		return strings.Split(text, "\n"), 0, isJSON
	}
	lines = strings.SplitN(text, "\n", most+1)
	if len(lines) > most {
		omitted = strings.Count(lines[most], "\n") + 1
		lines = lines[:most]
	}
	return lines, omitted, isJSON
}

func textualMIME(mimeType string) bool {
	return strings.HasPrefix(mimeType, "text/") ||
		strings.Contains(mimeType, "json") ||
		strings.Contains(mimeType, "xml") ||
		strings.Contains(mimeType, "javascript") ||
		strings.Contains(mimeType, "yaml") ||
		mimeType == "application/x-www-form-urlencoded" ||
		mimeType == "application/graphql"
}

// TransportLabel names a transport failure.
func TransportLabel(kind proxy.TransportErrorKind) string {
	switch kind {
	case proxy.TransportConnectionRefused:
		return "connection refused"
	case proxy.TransportTimeout:
		return "timeout"
	case proxy.TransportDNS:
		return "DNS lookup failed"
	case proxy.TransportTLS:
		return "TLS error"
	default:
		return "transport error"
	}
}

// FormatLatency shows a latency in ms below a second, else in seconds.
func FormatLatency(duration time.Duration) string {
	switch {
	case duration < time.Second:
		return strconv.FormatInt(max(time.Millisecond, duration.Round(time.Millisecond)).Milliseconds(), 10) + "ms"
	case duration < 10*time.Second:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", duration.Seconds()), ".0") + "s"
	default:
		return duration.Round(time.Second).String()
	}
}

func formatBytes(size int) string {
	if size < 1000 {
		return strconv.Itoa(size) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value, unit := float64(size)/1000, 0
	for value >= 1000 && unit < len(units)-1 {
		value /= 1000
		unit++
	}
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", value), "0"), ".") + " " + units[unit]
}
