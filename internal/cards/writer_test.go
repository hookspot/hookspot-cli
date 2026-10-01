package cards

import (
	"bytes"
	"errors"
	"io"
	"net/http"
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
	entry := session.Entry{Number: 2, Delivery: testDelivery(), Received: received, Target: "http://localhost:3000/api/webhooks", Response: ws.Response{Status: http.StatusBadGateway}, ReplayOf: 1}
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

	card := noColor(t, testListen().Request(request(entry), DefaultWidth))
	if want := "Connecting…\nReady. Waiting for requests (Ctrl-C to quit)\n" + card; out.String() != want {
		t.Errorf("out:\n%s\nwant:\n%s", out.String(), want)
	}
	if !strings.Contains(card, "#2") || !strings.Contains(card, "↻ replay") {
		t.Errorf("recorded entry lost its number or replay mark:\n%s", card)
	}
	wantErr := "⚠ github is disabled: requests to it are rejected. Enable it in the dashboard.\n" +
		"⚠ shopify has no route and is skipped. Add one in the dashboard.\n" +
		"http://localhost:3000/ returned 404. If your webhook route is elsewhere, include it in --forward-to, e.g. --forward-to http://localhost:3000/webhooks\n" +
		"connection lost: dropped; reconnecting in 2s...\n" +
		"Reconnected after 3s offline. Requests that arrived meanwhile were not delivered; retry them from https://hookspot.test/acme/payments/requests\n" +
		`#9: no such request\x1b` + "\n"
	if errOut.String() != wantErr {
		t.Errorf("errOut:\n%s\nwant:\n%s", errOut.String(), wantErr)
	}
	if out.writes != 3 || errOut.writes != 6 {
		t.Errorf("writes = %d and %d, want one per event", out.writes, errOut.writes)
	}
}

func TestWriterColorsOnlyTerminals(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("CLICOLOR_FORCE", "")
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
	if !strings.Contains(terminal.String(), "\x1b[") {
		t.Errorf("terminal Ready = %q, want color", terminal.String())
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
