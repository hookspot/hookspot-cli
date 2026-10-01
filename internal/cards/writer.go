package cards

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/colorprofile"

	"hookspot/internal/session"
)

// Writer prints a listen run as a plain stream: the banner, connection
// states and requests on out; notices, connection trouble and command replies
// on errOut. Each stream is colored only when it is a terminal.
type Writer struct {
	// Commands is set when stdin takes line commands, so the test hint names t.
	Commands bool

	listen      Listen
	requestsURL string
	out, errOut stream
	// mu keeps replies, which don't come through the session, whole.
	mu sync.Mutex
}

type stream struct {
	w       io.Writer
	profile colorprofile.Profile
}

// NewWriter prints listen's requests with l; Reconnected names requestsURL as
// the place to retry requests missed while offline.
func NewWriter(out, errOut io.Writer, l Listen, requestsURL string) *Writer {
	environ := os.Environ()
	return &Writer{
		listen:      l,
		requestsURL: requestsURL,
		out:         stream{out, colorprofile.Detect(out, environ)},
		errOut:      stream{errOut, colorprofile.Detect(errOut, environ)},
	}
}

// Banner opens the stream.
func (w *Writer) Banner(project string, routes []BannerRoute, hints []string) error {
	return w.write(w.out, Banner(project, routes, hints, Width(w.out.w)))
}

// Emit writes each event in one write, so a terminal never shows part of a
// card.
func (w *Writer) Emit(event session.Event) error {
	switch e := event.(type) {
	case session.Connecting:
		return w.write(w.out, Connecting())
	case session.Ready:
		return w.write(w.out, Ready())
	case session.ConnectionLost:
		return w.write(w.errOut, ConnectionLost(e.Err, e.RetryIn))
	case session.Reconnected:
		return w.write(w.errOut, Reconnected(e.Offline, w.requestsURL))
	case session.DisabledSource:
		return w.write(w.errOut, DisabledSource(e.Name))
	case session.SkippedSource:
		return w.write(w.errOut, SkippedSource(e.Name))
	case session.RootNotFound:
		return w.write(w.errOut, RootNotFound(e.Root, e.Status))
	case session.TestHint:
		return w.write(w.errOut, TestHint(e, w.Commands))
	case session.Recorded:
		return w.write(w.out, w.listen.Entry(e.Entry, Width(w.out.w)))
	}
	return nil
}

// Reply answers a line command.
func (w *Writer) Reply(text string) error {
	return w.write(w.errOut, Line(text))
}

// Print answers a line command with text that keeps its lines, such as a
// command to paste.
func (w *Writer) Print(text string) error {
	return w.write(w.errOut, Sanitize(text))
}

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
		notes = append(notes, "copying needs a terminal with OSC 52 (Terminal.app has none)")
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

func (w *Writer) write(s stream, text string) error {
	var styled bytes.Buffer
	_, _ = (&colorprofile.Writer{Forward: &styled, Profile: s.profile}).WriteString(text + "\n")
	w.mu.Lock()
	defer w.mu.Unlock()
	written, err := s.w.Write(styled.Bytes())
	if err == nil && written < styled.Len() {
		err = io.ErrShortWrite
	}
	return err
}
