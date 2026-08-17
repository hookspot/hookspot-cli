package printer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"

	"hookspot/internal/proxy"
	"hookspot/internal/ws"
)

// Mode controls the level and shape of delivery output.
type Mode uint8

const (
	ModeInspect Mode = iota
	ModeForward

	// These aliases read naturally at call sites and preserve both common
	// naming conventions for consumers of the package.
	InspectMode = ModeInspect
	ForwardMode = ModeForward
)

// Limits controls smart truncation. A zero value disables that limit.
type Limits struct {
	MaxBodyLines  int
	MaxHeaders    int
	MaxValueChars int
}

// Options configures a Printer.
type Options struct {
	Mode                 Mode
	Sources              map[string]string
	Limits               Limits
	ShowSensitiveHeaders bool
	Color                bool
}

// ForwardOutcome is the complete result of one local forwarding operation.
// A transport failure has no HTTP response; callers may still synthesize an
// upstream response after printing it.
type ForwardOutcome struct {
	Response  ws.Response
	Latency   time.Duration
	Failure   *proxy.TransportFailure
	TargetURL string
	Replay    bool
}

// Printer renders request inspections and local forwarding outcomes. Each
// output block is serialized so a replay cannot interleave with a live event.
type Printer struct {
	out       io.Writer
	options   Options
	now       func() time.Time
	sourceLen int
	mu        sync.Mutex
}

// New returns a configured Printer writing to out.
func New(out io.Writer, options Options) *Printer {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled {
		options.Color = false
	}

	sourceLen := 0
	for _, name := range options.Sources {
		if n := utf8.RuneCountInString(singleLine(name)); n > sourceLen {
			sourceLen = n
		}
	}
	if sourceLen == 0 {
		sourceLen = len("unknown")
	}

	return &Printer{
		out:       out,
		options:   options,
		now:       time.Now,
		sourceLen: sourceLen,
	}
}

// SupportsColor reports whether out is an interactive terminal and color has
// not been disabled with NO_COLOR.
func SupportsColor(out io.Writer) bool {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled {
		return false
	}
	fd, ok := out.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(fd.Fd()))
}

// Handle prints a delivery in inspect mode and acknowledges it with 200.
func (p *Printer) Handle(d ws.Delivery) (ws.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.renderInspect(d)
	return ws.Response{Status: http.StatusOK}, nil
}

// PrintForward prints one local forwarding result atomically.
func (p *Printer) PrintForward(d ws.Delivery, outcome ForwardOutcome) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.renderForward(d, outcome)
}

func (p *Printer) renderInspect(d ws.Delivery) {
	var output strings.Builder
	fmt.Fprintf(
		&output,
		"%s  %s  %s  %s  id %s\n",
		p.timestamp(),
		p.sourceToken(d.SourceUID),
		singleLine(method(d)),
		singleLine(d.Path),
		singleLine(requestUID(d)),
	)

	if d.Query != "" {
		fmt.Fprintf(&output, "├─ query       %s\n│\n", p.query(d.Query))
	}

	p.renderHeaders(&output, d.Headers)
	output.WriteString("│\n")
	p.renderInspectBody(&output, d.Body, d.Headers)
	output.WriteByte('\n')

	_, _ = io.WriteString(p.out, output.String())
}

func (p *Printer) renderForward(d ws.Delivery, outcome ForwardOutcome) {
	var output strings.Builder
	status := responseStatus(outcome.Response.Status)
	statusColor := statusANSIColor(outcome.Response.Status)
	if outcome.Failure != nil {
		status = "✗ " + transportLabel(outcome.Failure.Kind)
		statusColor = 31
	}
	if p.options.Color && statusColor != 0 {
		status = ansiColor(statusColor, status)
	}

	fmt.Fprintf(
		&output,
		"%s  %s  %s  %s  →  %s  %s  id %s",
		p.timestamp(),
		p.sourceToken(d.SourceUID),
		singleLine(method(d)),
		singleLine(d.Path),
		status,
		formatLatency(outcome.Latency),
		singleLine(requestUID(d)),
	)
	if outcome.Replay {
		output.WriteString("  replay")
	}

	if outcome.Failure != nil {
		output.WriteByte('\n')
		fmt.Fprintf(
			&output,
			"└─ target      %s\n\n",
			p.transportHint(outcome.TargetURL, outcome.Failure),
		)
		_, _ = io.WriteString(p.out, output.String())
		return
	}

	if outcome.Response.Status >= 200 && outcome.Response.Status < 300 {
		fmt.Fprintf(&output, "  %s\n", p.summary(d.Body, d.Headers))
		_, _ = io.WriteString(p.out, output.String())
		return
	}

	output.WriteByte('\n')
	p.renderForwardBody(&output, "request", false, d.Body, d.Headers)
	output.WriteString("│\n")
	p.renderForwardBody(&output, "response", true, outcome.Response.Body, outcome.Response.Headers)
	output.WriteByte('\n')
	_, _ = io.WriteString(p.out, output.String())
}

func (p *Printer) renderHeaders(output *strings.Builder, headers http.Header) {
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

	if len(keys) == 0 {
		output.WriteString("├─ headers     (empty)\n")
		return
	}

	visible := len(keys)
	if max := p.options.Limits.MaxHeaders; max > 0 && visible > max {
		visible = max
	}

	for i, key := range keys[:visible] {
		prefix := "│              "
		if i == 0 {
			prefix = "├─ headers     "
		}
		value := p.headerValue(key, headers[key])
		fmt.Fprintf(output, "%s%s: %s\n", prefix, strings.ToLower(singleLine(key)), value)
	}
	if omitted := len(keys) - visible; omitted > 0 {
		fmt.Fprintf(output, "│              … (%d headers omitted)\n", omitted)
	}
}

func (p *Printer) headerValue(key string, values []string) string {
	if sensitiveHeader(key) && !p.options.ShowSensitiveHeaders {
		return "(redacted)"
	}
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = p.value(singleLine(value))
	}
	return strings.Join(parts, ", ")
}

func (p *Printer) renderInspectBody(output *strings.Builder, body []byte, headers http.Header) {
	if len(body) == 0 {
		output.WriteString("└─ body        (empty)\n")
		return
	}

	mimeType := bodyMIME(headers, body)
	fmt.Fprintf(output, "└─ body        %s · %s\n", singleLine(mimeType), formatBytes(len(body)))
	lines, textual := bodyLines(body, mimeType)
	if !textual {
		return
	}
	p.renderBodyLines(output, lines, "    ")
}

func (p *Printer) renderForwardBody(output *strings.Builder, label string, last bool, body []byte, headers http.Header) {
	branch := "├─"
	contentPrefix := "│              "
	if last {
		branch = "└─"
		contentPrefix = "               "
	}

	if len(body) == 0 {
		fmt.Fprintf(output, "%s %-11s (empty)\n", branch, label)
		return
	}

	mimeType := bodyMIME(headers, body)
	fmt.Fprintf(output, "%s %-11s %s · %s\n", branch, label, singleLine(mimeType), formatBytes(len(body)))
	lines, textual := bodyLines(body, mimeType)
	if !textual {
		return
	}
	p.renderBodyLines(output, lines, contentPrefix)
}

func (p *Printer) renderBodyLines(output *strings.Builder, lines []string, prefix string) {
	visible := len(lines)
	if max := p.options.Limits.MaxBodyLines; max > 0 && visible > max {
		visible = max
	}

	for _, line := range lines[:visible] {
		fmt.Fprintf(output, "%s%s\n", prefix, p.value(escapeText(line)))
	}
	if omitted := len(lines) - visible; omitted > 0 {
		fmt.Fprintf(output, "%s… (%d lines omitted)\n", prefix, omitted)
	}
}

func (p *Printer) query(raw string) string {
	parts := strings.Split(raw, "&")
	for i, part := range parts {
		key, value, found := strings.Cut(part, "=")
		key = singleLine(key)
		if !found {
			parts[i] = key
			continue
		}
		parts[i] = key + "=" + p.value(singleLine(value))
	}
	return strings.Join(parts, "&")
}

func (p *Printer) summary(body []byte, headers http.Header) string {
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) == nil {
		for _, key := range []string{"type", "event", "event_type", "action"} {
			var value string
			if raw, ok := object[key]; ok && json.Unmarshal(raw, &value) == nil && value != "" {
				return p.value(singleLine(value)) + " · " + formatBytes(len(body))
			}
		}
	}

	if len(body) == 0 {
		return "(empty) · 0 B"
	}
	return singleLine(bodyMIME(headers, body)) + " · " + formatBytes(len(body))
}

func (p *Printer) transportHint(target string, failure *proxy.TransportFailure) string {
	target = singleLine(target)
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
			detail = p.value(singleLine(failure.Err.Error()))
		}
		return target + " failed: " + detail + " — check the target URL and server logs"
	}
}

func (p *Printer) value(value string) string {
	max := p.options.Limits.MaxValueChars
	if max == 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "… (truncated)"
}

func (p *Printer) timestamp() string {
	return p.now().Format("15:04:05.000")
}

func (p *Printer) sourceToken(uid string) string {
	name := p.options.Sources[uid]
	if name == "" {
		name = "unknown"
	}
	displayName := singleLine(name)
	padding := p.sourceLen - utf8.RuneCountInString(displayName)
	if padding < 0 {
		padding = 0
	}
	token := "● " + displayName + strings.Repeat(" ", padding)
	if !p.options.Color {
		return token
	}
	return ansiColor(sourceColor(uid), token)
}

var sourceColorCodes = [...]int{32, 33, 34, 35, 36, 92, 93, 94, 95, 96}

func sourceColor(uid string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(uid))
	return sourceColorCodes[int(hash.Sum32()%uint32(len(sourceColorCodes)))]
}

func statusANSIColor(status int) int {
	switch {
	case status >= 200 && status < 300:
		return 32
	case status >= 300 && status < 400:
		return 33
	case status >= 400:
		return 31
	default:
		return 0
	}
}

func ansiColor(code int, value string) string {
	return fmt.Sprintf("\x1b[%dm%s\x1b[0m", code, value)
}

func sensitiveHeader(key string) bool {
	switch strings.ToLower(key) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie":
		return true
	default:
		return false
	}
}

func bodyMIME(headers http.Header, body []byte) string {
	contentType := headerGet(headers, "Content-Type")
	if contentType != "" {
		if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
			return strings.ToLower(mediaType)
		}
		if before, _, found := strings.Cut(contentType, ";"); found {
			contentType = before
		}
		if contentType = strings.TrimSpace(contentType); contentType != "" {
			return strings.ToLower(contentType)
		}
	}
	if len(body) == 0 {
		return "application/octet-stream"
	}
	mediaType, _, err := mime.ParseMediaType(http.DetectContentType(body))
	if err == nil {
		return strings.ToLower(mediaType)
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

func bodyLines(body []byte, mimeType string) ([]string, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && json.Valid(trimmed) {
		var pretty bytes.Buffer
		if json.Indent(&pretty, trimmed, "", "  ") == nil {
			return strings.Split(pretty.String(), "\n"), true
		}
	}
	if !textualMIME(mimeType) || !utf8.Valid(body) {
		return nil, false
	}
	return strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n"), true
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

func escapeText(value string) string {
	var escaped strings.Builder
	for _, r := range value {
		switch {
		case r == '\n' || r == '\t':
			escaped.WriteRune(r)
		case r == '\r':
			escaped.WriteString("\\r")
		case unicode.IsControl(r):
			if r <= 0xff {
				fmt.Fprintf(&escaped, "\\x%02x", r)
			} else {
				fmt.Fprintf(&escaped, "\\u%04x", r)
			}
		default:
			escaped.WriteRune(r)
		}
	}
	return escaped.String()
}

func singleLine(value string) string {
	value = escapeText(value)
	value = strings.ReplaceAll(value, "\n", "\\n")
	return value
}

func method(d ws.Delivery) string {
	if d.Method == "" {
		return http.MethodPost
	}
	return d.Method
}

func requestUID(d ws.Delivery) string {
	if d.RequestUID == "" {
		return "(unknown)"
	}
	return d.RequestUID
}

func responseStatus(status int) string {
	text := http.StatusText(status)
	if text == "" {
		return strconv.Itoa(status)
	}
	return strconv.Itoa(status) + " " + text
}

func transportLabel(kind proxy.TransportErrorKind) string {
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

func formatLatency(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	if duration < time.Second {
		milliseconds := duration.Round(time.Millisecond)
		if milliseconds < time.Millisecond {
			milliseconds = time.Millisecond
		}
		return strconv.FormatInt(milliseconds.Milliseconds(), 10) + "ms"
	}
	if duration < 10*time.Second {
		formatted := strings.TrimSuffix(fmt.Sprintf("%.1f", duration.Seconds()), ".0")
		return formatted + "s"
	}
	return duration.Round(time.Second).String()
}

func formatBytes(size int) string {
	if size < 1000 {
		return strconv.Itoa(size) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(size)
	for _, unit := range units {
		value /= 1000
		if value < 1000 || unit == units[len(units)-1] {
			formatted := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", value), "0"), ".")
			return formatted + " " + unit
		}
	}
	return strconv.Itoa(size) + " B"
}
