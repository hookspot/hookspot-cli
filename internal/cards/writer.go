package cards

import (
	"bytes"
	"io"
	"os"
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
	ready       bool
	updateAlert string
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
// card. The update alert follows Ready.
func (w *Writer) Emit(event session.Event) error {
	switch e := event.(type) {
	case session.Connecting:
		return w.write(w.out, Connecting())
	case session.Ready:
		if err := w.write(w.out, Ready()); err != nil {
			return err
		}
		w.ready = true
		// Like the update check's failures, the alert's never stop listening.
		_ = w.writeUpdateAlert()
		return nil
	case session.UpdateAvailable:
		w.updateAlert = UpdateAlert(e).whole
		return w.writeUpdateAlert()
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

// writeUpdateAlert writes the update alert once both it and Ready are in, on
// out: the terminal the update check asked about.
func (w *Writer) writeUpdateAlert() error {
	if !w.ready || w.updateAlert == "" {
		return nil
	}
	return w.write(w.out, w.updateAlert)
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
