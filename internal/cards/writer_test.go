package cards

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"hookspot/internal/session"
	"hookspot/internal/ws"
)

// countingWriter counts writes, so a test can see a card arrive whole.
type countingWriter struct {
	bytes.Buffer
	writes int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes++
	return w.Buffer.Write(p)
}

func TestWriterSendsEachEventToItsStreamInOneWrite(t *testing.T) {
	var out, errOut countingWriter
	w := NewWriter(&out, &errOut, testListen(), "https://hookspot.test/acme/payments/requests")
	entry := session.Entry{Number: 2, Delivery: testDelivery(), Received: received, Target: "http://localhost:3000/api/webhooks", Response: ws.Response{Status: http.StatusBadGateway}, ReplayOf: 1, Replay: &session.Comparison{Original: 1, Status: http.StatusOK}}
	events := []session.Event{
		session.DisabledSource{Name: "github"},
		session.SkippedSource{Name: "shopify"},
		session.Connecting{},
		session.Ready{},
		session.Recorded{Entry: entry},
		session.RootNotFound{Root: "http://localhost:3000/", Status: http.StatusNotFound},
		session.ConnectionLost{Err: errors.New("dropped"), RetryIn: 2 * time.Second},
		session.Reconnected{Offline: 3 * time.Second},
	}
	for _, event := range events {
		if err := w.Emit(event); err != nil {
			t.Fatalf("Emit(%T) = %v", event, err)
		}
	}
	if err := w.Reply("#9: no such request\x1b"); err != nil {
		t.Fatal(err)
	}

	if want := noColor(t, Connecting()) + noColor(t, Ready()) + noColor(t, testListen().Entry(entry, DefaultWidth)); out.String() != want {
		t.Errorf("out:\n%s\nwant:\n%s", out.String(), want)
	}
	wantErr := noColor(t, DisabledSource("github")) +
		noColor(t, SkippedSource("shopify")) +
		noColor(t, RootNotFound("http://localhost:3000/", http.StatusNotFound)) +
		noColor(t, ConnectionLost(errors.New("dropped"), 2*time.Second)) +
		noColor(t, Reconnected(3*time.Second, "https://hookspot.test/acme/payments/requests")) +
		`#9: no such request\x1b` + "\n"
	if errOut.String() != wantErr {
		t.Errorf("errOut:\n%s\nwant:\n%s", errOut.String(), wantErr)
	}
	if out.writes != 3 || errOut.writes != 6 {
		t.Errorf("writes = %d and %d, want one per event", out.writes, errOut.writes)
	}
}

func TestCurlNotesWarnAboutTheHeadersFile(t *testing.T) {
	c := session.Curl{HeadersFile: "/work/hookspot-fixtures/req_1\x1b[2J.headers"}
	want := "#3 as cURL\n" + `/work/hookspot-fixtures/req_1\x1b[2J.headers holds unredacted headers, which may contain credentials`
	if got := CurlNotes(3, c, false); got != want {
		t.Fatalf("notes = %q, want %q", got, want)
	}
}

// colorSGR is a styling sequence that sets a color.
var colorSGR = regexp.MustCompile(`\x1b\[([0-9]+;)*(3[0-9]|4[0-9]|9[0-7]|10[0-7])(;[0-9]+)*m`)

func TestWriterColorsOnlyTerminals(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("NO_COLOR", "")
	var piped bytes.Buffer
	if err := NewWriter(&piped, io.Discard, testListen(), "").Emit(session.Ready{}); err != nil {
		t.Fatal(err)
	}
	if piped.String() != "Ready. Waiting for requests (Ctrl-C to quit)\n" {
		t.Errorf("piped Ready = %q", piped.String())
	}

	t.Setenv("TTY_FORCE", "1")
	var terminal bytes.Buffer
	if err := NewWriter(&terminal, io.Discard, testListen(), "").Emit(session.Ready{}); err != nil {
		t.Fatal(err)
	}
	if !colorSGR.MatchString(terminal.String()) {
		t.Errorf("terminal Ready = %q, want color", terminal.String())
	}

	// NO_COLOR keeps bold and faint.
	t.Setenv("NO_COLOR", "1")
	var uncolored bytes.Buffer
	writer := NewWriter(&uncolored, &uncolored, testListen(), "")
	failed := session.Entry{Number: 2, Delivery: testDelivery(), Target: "http://localhost:3000/api/webhooks", Response: ws.Response{Status: http.StatusBadGateway}, Received: received}
	for _, event := range []session.Event{session.Ready{}, session.Recorded{Entry: failed}, session.DisabledSource{Name: "github"}} {
		if err := writer.Emit(event); err != nil {
			t.Fatal(err)
		}
	}
	if colorSGR.MatchString(uncolored.String()) || !strings.Contains(uncolored.String(), "\x1b[1m") {
		t.Errorf("NO_COLOR stream = %q, want bold and faint only", uncolored.String())
	}
}

func TestWriterReturnsWriteFailures(t *testing.T) {
	wantErr := errors.New("stdout unavailable")
	for _, test := range []struct {
		name   string
		writer io.Writer
		want   error
	}{
		{name: "writer error", writer: failingWriter{wantErr}, want: wantErr},
		{name: "short write", writer: shortWriter{}, want: io.ErrShortWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := NewWriter(test.writer, io.Discard, testListen(), "")
			errOut := NewWriter(io.Discard, test.writer, testListen(), "")
			for name, err := range map[string]error{
				"banner":  out.Banner("Acme | Payments", nil, nil),
				"request": out.Emit(session.Recorded{Entry: session.Entry{Number: 1, Delivery: testDelivery(), Received: received}}),
				"notice":  errOut.Emit(session.DisabledSource{Name: "github"}),
				"reply":   errOut.Reply("#9: no such request"),
				"ready":   out.Emit(session.Ready{}),
			} {
				if !errors.Is(err, test.want) {
					t.Errorf("%s error = %v, want %v", name, err, test.want)
				}
			}
		})
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return max(0, len(p)-1), nil }
