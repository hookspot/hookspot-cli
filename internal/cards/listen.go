package cards

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"sort"
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
	inner := max(1, width-4)
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
			leads[i] = pad(sourceStyle(route.SourceUID).Render(Line(route.Source)), nameWidth) + "  " + faintStyle.Render(Line(route.PublicURL))
		}
		destination := "terminal only"
		if route.Destination != "" {
			destination = Line(route.Destination)
		}
		targets[i] = faintStyle.Render("→") + " " + destination
		// An unnamed route's label is its path, which the destination already ends with.
		if label := Line(route.Label); label != "" && !strings.HasSuffix(destination, label) {
			targets[i] += "  " + faintStyle.Render(label)
		}
		fits = fits && nameWidth+2+urlWidth+2+lipgloss.Width(targets[i]) <= inner
	}

	var lines []string
	for i := range routes {
		switch {
		case fits:
			lines = append(lines, pad(leads[i], nameWidth+2+urlWidth)+"  "+targets[i])
		case leads[i] != "":
			lines = append(lines, leads[i], "  "+targets[i])
		default:
			lines = append(lines, "  "+targets[i])
		}
	}

	title := "Listening in " + boldStyle.Render(Line(project))
	label := faintStyle.Render(count(len(sources), "source") + " • " + count(len(routes), "route"))
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
	return warnStyle.Render("connection lost:") + " " + Line(err.Error()) + faintStyle.Render(fmt.Sprintf("; reconnecting in %s...", retryIn))
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
}

// Line renders the status at width: state, project, counts and p50, then the
// hints while they fit.
func (s Status) Line(width int) string {
	parts := []string{s.state(), boldStyle.Render(Line(s.Project)), count(s.Totals.Count, "request")}
	if t := s.Totals; t.OK+t.Failed > 0 {
		failed := strconv.Itoa(t.Failed) + " failed"
		if t.Failed > 0 {
			failed = errorStyle.Render(failed)
		}
		parts = append(parts, okStyle.Render(strconv.Itoa(t.OK)+" ok"), failed)
	}
	// Max is zero until a request got a response or timed out.
	if s.Totals.Max > 0 {
		parts = append(parts, "p50 "+FormatLatency(s.Totals.P50))
	}
	if s.State == StateOffline && s.Err != nil {
		parts = append(parts, faintStyle.Render(Line(s.Err.Error())))
	}
	return withHints(strings.Join(parts, faintStyle.Render(" · ")), s.Hints, width)
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

var cursorStyle = lipgloss.NewStyle().Reverse(true)

// Prompt is the › prompt with the typed input and a block cursor, then hints
// at the right end while they fit.
func Prompt(input string, hints []string, width int) string {
	return withHints(faintStyle.Render("›")+" "+Line(input)+cursorStyle.Render(" "), hints, width)
}

// withHints puts hints at the right end of line while they fit, then cuts the
// line to width.
func withHints(line string, hints []string, width int) string {
	if len(hints) > 0 {
		joined := faintStyle.Render(strings.Join(hints, " · "))
		if gap := width - lipgloss.Width(line) - lipgloss.Width(joined); gap >= 2 {
			line += strings.Repeat(" ", gap) + joined
		}
	}
	return truncate(line, width)
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
			lines = append(lines, "  "+pad(keys[i], keyWidth)+"  send a test event to "+boldStyle.Render(Line(source.Name)))
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
func PathWorks(r Request, width int) string {
	path := "hookspot → this terminal"
	if r.Target != "" && r.Failure == nil {
		path += " → " + Line(r.Target)
	}
	return truncate(okStyle.Render("✓")+" path works: "+path, width)
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

// Request is one delivery and what forwarding it did.
type Request struct {
	Number   int
	Delivery ws.Delivery
	Received time.Time
	// Target is the URL the delivery was forwarded to; "" in inspect mode.
	Target   string
	Response ws.Response
	Latency  time.Duration
	Failure  *proxy.TransportFailure
	// Replay compares a replay with the request it replays; nil otherwise.
	Replay *session.Comparison
	Test   bool
}

// Request renders a replay as its summary and response diff, r as a one-line
// row when forwarding answered 2xx, and as a card otherwise and in inspect
// mode.
func (l Listen) Request(r Request, width int) string {
	inner := max(1, width-4)
	switch {
	case r.Replay != nil:
		return l.replay(r, width)
	case r.Target == "":
		return l.inspect(r, inner)
	case r.Failure != nil:
		return l.transportFailure(r, inner)
	case r.Response.Status >= 200 && r.Response.Status < 300:
		return l.row(r, width)
	default:
		return l.httpFailure(r, inner)
	}
}

func (l Listen) row(r Request, width int) string {
	d := r.Delivery
	// Badges are padded, so one space sets them apart. Whoever posts to a
	// source picks the method, so one longer than OPTIONS is cut.
	const methodWidth, maxMethod = 6, len("OPTIONS")
	left := pad(Badge(truncate(Line(method(d)), maxMethod)), methodWidth) + " "
	if r.Number > 0 {
		left = faintStyle.Render(fmt.Sprintf("#%-3d", r.Number)) + left
	}
	right := ColorBadge(StatusColor(r.Response.Status), strconv.Itoa(r.Response.Status)) + " " +
		faintStyle.Render(fmt.Sprintf("%5s", FormatLatency(r.Latency))+"  "+timestamp(r))
	if marks := marks(r); marks != "" {
		right = marks + " " + right
	}

	// The path keeps a few columns even when that overflows width; a long
	// source name gives way first.
	const minPath, minSummary = 12, 8
	sourceWidth := min(l.sourceWidth(), max(lipgloss.Width("unknown"), width-lipgloss.Width(left)-lipgloss.Width(right)-minPath-4))
	left += pad(truncate(l.source(d.SourceUID), sourceWidth), sourceWidth) + "  "
	room := max(minPath, width-lipgloss.Width(left)-lipgloss.Width(right)-2)
	path, summary := Line(d.Path), l.Summary(d)
	if summaryRoom := room - lipgloss.Width(path) - 2; lipgloss.Width(summary) <= summaryRoom || summaryRoom >= minSummary {
		left += path + "  " + faintStyle.Render(truncate(summary, summaryRoom))
	} else {
		left += truncate(path, room)
	}
	return left + strings.Repeat(" ", max(2, width-lipgloss.Width(left)-lipgloss.Width(right))) + right
}

func (l Listen) httpFailure(r Request, inner int) string {
	d, response := r.Delivery, r.Response
	responseLines := append(RedirectHint(response), l.Body(response.Body, response.Headers)...)

	border := lipgloss.NewStyle().Foreground(StatusColor(response.Status))
	return frame(border, inner, l.title(r), cardLabel(r), "",
		section{lines: outcome(ColorBadge(StatusColor(response.Status), responseStatus(response.Status)), r, inner)},
		section{title: faintStyle.Render("request · " + BodyTitle(d.Body, d.Headers)), lines: l.Body(d.Body, d.Headers)},
		section{title: faintStyle.Render("response · " + BodyTitle(response.Body, response.Headers)), lines: responseLines},
	)
}

func (l Listen) transportFailure(r Request, inner int) string {
	lines := append(outcome(ColorBadge(StatusColor(0), "✗ "+TransportLabel(r.Failure.Kind)), r, inner), "")
	lines = append(lines, l.TransportHints(r.Target, r.Failure)...)
	return frame(errorStyle, inner, l.title(r), cardLabel(r), "", section{lines: lines})
}

// RedirectHint follows a 3xx response that names a Location: webhook senders
// stop there.
func RedirectHint(response ws.Response) []string {
	location := headerGet(response.Headers, "Location")
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

func (l Listen) inspect(r Request, inner int) string {
	d := r.Delivery
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
	return frame(faintStyle, inner, l.title(r), cardLabel(r), "",
		section{lines: first},
		section{title: faintStyle.Render("headers · " + headers), lines: l.Headers(d.Headers)},
		section{title: faintStyle.Render("body · " + BodyTitle(d.Body, d.Headers)), lines: l.Body(d.Body, d.Headers)},
	)
}

// replay sums up a replay as "#46 ↻ #45  422 → 200  9ms → 41ms", then shows
// how its response differs from the original's.
func (l Listen) replay(r Request, width int) string {
	c := r.Replay
	summary := faintStyle.Render("#"+strconv.Itoa(r.Number)) + " " + warnStyle.Render("↻") + " " + faintStyle.Render("#"+strconv.Itoa(c.Original)) + "  " +
		result(c.Status, c.Failure) + faintStyle.Render(" → ") + result(r.Response.Status, r.Failure) + "  " +
		faintStyle.Render(FormatLatency(c.Latency)+" → "+FormatLatency(r.Latency))
	right := faintStyle.Render(timestamp(r))
	const minTitle = 12
	room := max(minTitle, width-lipgloss.Width(summary)-lipgloss.Width(right)-4)
	// The title leaves out the number the summary opens with.
	left := summary + "  " + truncate(l.title(Request{Delivery: r.Delivery}), room)
	lines := []string{left + strings.Repeat(" ", max(2, width-lipgloss.Width(left)-lipgloss.Width(right))) + right}

	for _, line := range c.Removed {
		lines = append(lines, truncate("  "+errorStyle.Render("- "+l.bodyLine(line)), width))
	}
	for _, line := range c.Added {
		lines = append(lines, truncate("  "+okStyle.Render("+ "+l.bodyLine(line)), width))
	}
	if c.More > 0 {
		lines = append(lines, "  "+faintStyle.Render("… "+count(c.More, "more changed line")))
	}
	if c.Binary {
		lines = append(lines, "  "+faintStyle.Render("binary body "+formatBytes(c.Size)+" → "+formatBytes(len(r.Response.Body))))
	}
	if r.Failure != nil {
		for _, line := range strings.Split(lipgloss.Wrap(l.transportHint(r.Target, r.Failure), max(1, width-2), ""), "\n") {
			lines = append(lines, "  "+line)
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
func (l Listen) title(r Request) string {
	title := l.source(r.Delivery.SourceUID) + faintStyle.Render(" · ") + Line(method(r.Delivery)) + " " + Line(r.Delivery.Path)
	if r.Number > 0 {
		title = faintStyle.Render("#"+strconv.Itoa(r.Number)) + " " + title
	}
	return title
}

// outcome opens a forwarding card: what forwarding did, how long it took and
// where it went, then the request ID.
func outcome(status string, r Request, inner int) []string {
	return spread(status+" "+faintStyle.Render(FormatLatency(r.Latency)+"  → "+Line(r.Target)), faintStyle.Render(Line(r.Delivery.RequestUID)), inner)
}

// cardLabel ends a card's top border: its marks and the time it arrived.
func cardLabel(r Request) string {
	label := faintStyle.Render(timestamp(r))
	if marks := marks(r); marks != "" {
		label = marks + "  " + label
	}
	return label
}

func marks(r Request) string {
	if r.Test {
		return Badge("test")
	}
	return ""
}

func (l Listen) source(uid string) string {
	return sourceStyle(uid).Render(Line(l.sourceName(uid)))
}

func (l Listen) sourceName(uid string) string {
	if name := l.Sources[uid]; name != "" {
		return name
	}
	return "unknown"
}

// sourceWidth aligns the rows' source column.
func (l Listen) sourceWidth() int {
	width := lipgloss.Width("unknown")
	for _, name := range l.Sources {
		width = max(width, lipgloss.Width(Line(name)))
	}
	return width
}

func sourceStyle(uid string) lipgloss.Style {
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
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := strings.ToLower(keys[i]), strings.ToLower(keys[j])
		if left == right {
			return keys[i] < keys[j]
		}
		return left < right
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
		lines = append(lines, pad(keyStyle.Render(strings.ToLower(Line(key))), nameWidth)+"  "+value)
	}
	if omitted := len(headers) - len(keys); omitted > 0 {
		lines = append(lines, faintStyle.Render("… "+count(omitted, "more header")))
	}
	return lines
}

// Body lists a body's lines, pretty-printing and highlighting JSON; a binary
// body has none.
func (l Listen) Body(body []byte, headers http.Header) []string {
	lines, isJSON := bodyLines(body, bodyMIME(headers, body))
	visible := limit(len(lines), l.Limits.MaxBodyLines)
	out := make([]string, 0, visible+1)
	for _, line := range lines[:visible] {
		line = l.bodyLine(line)
		if isJSON {
			line = highlightJSON(line)
		}
		out = append(out, line)
	}
	if omitted := len(lines) - visible; omitted > 0 {
		out = append(out, faintStyle.Render("… "+count(omitted, "more line")))
	}
	return out
}

// bodyLine escapes one body line, applies --max-value-chars and expands tabs.
func (l Listen) bodyLine(line string) string {
	return strings.ReplaceAll(l.value(Sanitize(line)), "\t", "    ")
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
		return target + " timed out — check that your server responds within 30s"
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
	if most := l.Limits.MaxValueChars; most > 0 && utf8.RuneCountInString(value) > most {
		return string([]rune(value)[:most]) + "…"
	}
	return value
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

// pad fills s, which may be styled, to width columns.
func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(0, width-lipgloss.Width(s)))
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func timestamp(r Request) string {
	return r.Received.Format("15:04:05.000")
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
	contentType := headerGet(headers, "Content-Type")
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

func headerGet(headers http.Header, wanted string) string {
	for key, values := range headers {
		if strings.EqualFold(key, wanted) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

// bodyLines splits a body for display: indented JSON, or text when the media
// type is textual and the bytes are UTF-8. Anything else has no lines.
func bodyLines(body []byte, mimeType string) (lines []string, isJSON bool) {
	trimmed := bytes.TrimSpace(body)
	var pretty bytes.Buffer
	if len(trimmed) > 0 && json.Indent(&pretty, trimmed, "", "  ") == nil {
		return strings.Split(pretty.String(), "\n"), true
	}
	if len(body) == 0 || !textualMIME(mimeType) || !utf8.Valid(body) {
		return nil, false
	}
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n"), "\n"), false
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

func method(d ws.Delivery) string {
	if d.Method == "" {
		return http.MethodPost
	}
	return d.Method
}

func responseStatus(status int) string {
	if text := http.StatusText(status); text != "" {
		return strconv.Itoa(status) + " " + text
	}
	return strconv.Itoa(status)
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
