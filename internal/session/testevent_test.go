package session

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"hookspot/internal/api"
	"hookspot/internal/ws"
)

// sent is a request the fake Hookspot ingest endpoint received.
type sent struct {
	method, path string
	header       http.Header
	body         string
}

// hookspot is a fake ingest endpoint that keeps each request and answers 202,
// 404 under /missing, or 302 to /in/src_stripe under /moved.
func hookspot(t *testing.T) (string, <-chan sent) {
	t.Helper()
	requests := make(chan sent, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- sent{method: r.Method, path: r.URL.Path, header: r.Header, body: string(body)}
		switch r.URL.Path {
		case "/missing":
			http.NotFound(w, r)
		case "/moved":
			http.Redirect(w, r, "/in/src_stripe", http.StatusFound)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL, requests
}

// newTestEventSession forwards for stripe, whose two routes testSources sets,
// and github, both public at base.
func newTestEventSession(base string) (*Session, *recorder) {
	s, _, sink := newTestSession()
	s.sources = []api.Source{
		{UID: "src_stripe", Name: "stripe", URL: base + "/in/src_stripe", Routes: testSources[0].Routes},
		{UID: "src_github", Name: "github", URL: base + "/missing", Routes: []api.Route{{UID: "rte_github", Destination: api.Destination{Path: "/github"}}}},
	}
	return s, sink
}

func noRequest(t *testing.T, requests <-chan sent) {
	t.Helper()
	select {
	case r := <-requests:
		t.Fatalf("sent %#v, want nothing", r)
	default:
	}
}

func TestSendTest(t *testing.T) {
	base, requests := hookspot(t)
	s, sink := newTestEventSession(base)

	if name, err := s.SendTest("stripe"); err != nil || name != "stripe" {
		t.Fatalf("SendTest(stripe) = %q, %v", name, err)
	}
	r := <-requests
	id := r.header.Get("X-Hookspot-Test")
	if r.method != http.MethodPost || r.path != "/in/src_stripe" || r.header.Get("Content-Type") != "application/json" || id == "" ||
		r.body != `{"type":"hookspot.test","sent_at":"2026-10-01T12:00:00Z"}` {
		t.Fatalf("test event = %#v", r)
	}

	t.Run("marks every delivery carrying its id", func(t *testing.T) {
		orders, refunds, other, plain := delivery("rte_orders", "/orders"), delivery("rte_refunds", "/refunds"), delivery("rte_orders", "/orders"), delivery("rte_orders", "/orders")
		orders.Headers = http.Header{"x-hookspot-test": {id}}
		refunds.Headers = http.Header{"X-HOOKSPOT-TEST": {id}}
		other.Headers = http.Header{"X-Hookspot-Test": {"someone-else"}}
		for _, d := range []ws.Delivery{orders, refunds, other, plain} {
			if _, err := s.Handle(d); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Replay(1); err != nil {
			t.Fatal(err)
		}
		var tests []bool
		for _, e := range sink.recorded() {
			tests = append(tests, e.Entry.Test)
		}
		// A replay is local, so it proves nothing about the path.
		if want := []bool{true, true, false, false, false}; !reflect.DeepEqual(tests, want) {
			t.Fatalf("test marks = %v, want %v", tests, want)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		for _, test := range []struct {
			name, source, want string
		}{
			{name: "several sources, none named", want: "test which source? t stripe · t github"},
			{name: "not listened to", source: "shopify", want: "shopify: not a source this run listens to"},
		} {
			t.Run(test.name, func(t *testing.T) {
				if _, err := s.SendTest(test.source); err == nil || err.Error() != test.want {
					t.Fatalf("SendTest(%q) = %v, want %q", test.source, err, test.want)
				}
				noRequest(t, requests)
			})
		}
	})

	t.Run("Hookspot refusing it is reported", func(t *testing.T) {
		if _, err := s.SendTest("github"); err == nil || err.Error() != "test event to github: Hookspot answered 404 Not Found" {
			t.Fatalf("SendTest(github) = %v", err)
		}
		<-requests
	})

	t.Run("a redirect or an unreachable Hookspot is reported", func(t *testing.T) {
		closed := httptest.NewServer(http.NotFoundHandler())
		closed.Close()
		failing, _ := newTestEventSession(base)
		failing.sources = []api.Source{{Name: "moved", URL: base + "/moved"}, {Name: "down", URL: closed.URL}}
		// Following it would resend the event as a GET that Hookspot accepts.
		if _, err := failing.SendTest("moved"); err == nil || err.Error() != "test event to moved: Hookspot answered 302 Found" {
			t.Fatalf("SendTest(moved) = %v", err)
		}
		<-requests
		noRequest(t, requests)
		if _, err := failing.SendTest("down"); err == nil || !strings.HasPrefix(err.Error(), "test event to down: ") {
			t.Fatalf("SendTest(down) = %v", err)
		}
	})

	t.Run("the only source needs no name", func(t *testing.T) {
		single, _ := newTestEventSession(base)
		single.sources = single.sources[:1]
		if name, err := single.SendTest(""); err != nil || name != "stripe" {
			t.Fatalf("SendTest() = %q, %v", name, err)
		}
		if r := <-requests; r.path != "/in/src_stripe" {
			t.Fatalf("test event went to %s", r.path)
		}
	})
}

func TestTestHintFollowsReadyBeforeAnyRequest(t *testing.T) {
	s, _, sink := newTestSession()
	for _, event := range []Event{Connecting{}, Ready{}} {
		if err := s.Emit(event); err != nil {
			t.Fatal(err)
		}
	}
	if want := []Event{Connecting{}, Ready{}, TestHint{Sources: testSources}}; !reflect.DeepEqual(sink.events, want) {
		t.Fatalf("events = %#v, want %#v", sink.events, want)
	}

	if _, err := s.Handle(delivery("rte_orders", "/orders")); err != nil {
		t.Fatal(err)
	}
	if err := s.Emit(Ready{}); err != nil {
		t.Fatal(err)
	}
	if last := sink.events[len(sink.events)-1]; last != (Ready{}) {
		t.Fatalf("last event = %#v, want Ready without a hint once a request arrived", last)
	}
}

func TestTestCurlSendsATestEvent(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	base, requests := hookspot(t)
	// The quote must survive the shell.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "sh", "-c", TestCurl(base+"/in/it's")+" --silent --show-error").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	r := <-requests
	if r.method != http.MethodPost || r.path != "/in/it's" || r.header.Get("Content-Type") != "application/json" || r.body != `{"type":"hookspot.test"}` {
		t.Fatalf("test curl sent %#v", r)
	}
}
