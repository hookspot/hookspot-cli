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
	case session.Recorded:
		return w.write(w.out, w.listen.Request(request(e.Entry), Width(w.out.w)))
	}
	return nil
}

// Reply answers a line command.
func (w *Writer) Reply(text string) error {
	return w.write(w.errOut, Line(text))
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

func request(e session.Entry) Request {
	return Request{
		Number:   e.Number,
		Delivery: e.Delivery,
		Received: e.Received,
		Target:   e.Target,
		Response: e.Response,
		Latency:  e.Latency,
		Failure:  e.Failure,
		Replay:   e.ReplayOf > 0,
	}
}
