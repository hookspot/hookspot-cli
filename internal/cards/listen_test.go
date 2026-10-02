package cards

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/golden"

	"hookspot/internal/api"
	"hookspot/internal/proxy"
	"hookspot/internal/session"
	"hookspot/internal/ws"
)

var received = time.Date(2026, 7, 12, 12, 34, 56, 789_000_000, time.UTC)

func testListen() Listen {
	return Listen{Sources: map[string]string{"src_stripe": "stripe", "src_shopify": "shopify"}}
}

func testDelivery() ws.Delivery {
	return ws.Delivery{
		AttemptUID: "att-internal-never-print",
		RequestUID: "req_01JFULLREQUESTUID",
		SourceUID:  "src_stripe",
		Method:     "POST",
		Path:       "/api/webhooks",
		Query:      "page=1&x=2",
		Headers: http.Header{
			"stripe-signature": []string{"t=1690,v1=5f8e"},
			"Content-Type":     []string{"application/json; charset=utf-8"},
			"Authorization":    []string{"Bearer secret"},
		},
		Body: []byte(`{"id":"evt_1","type":"payment_intent.succeeded","data":{"amount":2000,"live":false,"note":null}}`),
	}
}

func forwarded(number int, d ws.Delivery, status int, latency time.Duration) session.Entry {
	return session.Entry{
		Number:   number,
		Delivery: d,
		Received: received,
		Target:   "http://localhost:3000" + d.Path,
		Response: ws.Response{Status: status},
		Latency:  latency,
	}
}

func joinCards(list ...string) string {
	return strings.Join(list, "\n\n")
}

func TestBanner(t *testing.T) {
	// A route's name shows even when its destination ends with it.
	ciHook := []BannerRoute{
		{SourceUID: "src_stripe", Source: "stripe", PublicURL: "https://in.hookspot.test/src_stripe", Path: "/webhooks/stripe", Destination: "http://localhost:3000/webhooks/stripe", Label: "/webhooks/stripe"},
		{SourceUID: "src_github", Source: "github", PublicURL: "https://in.hookspot.test/src_github", Path: "/webhooks/github", Destination: "http://localhost:3000/webhooks/github", Label: "/webhooks/github"},
		{SourceUID: "src_github", Source: "github", PublicURL: "https://in.hookspot.test/src_github", Path: "/ci/github", Destination: "http://localhost:3000/ci/github", Label: "github"},
	}
	hints := []string{"↵ replay last", "ctrl-c quit"}
	t.Run("forward", func(t *testing.T) {
		golden.RequireEqual(t, noColor(t, Banner("Acme Inc. | Payments", ciHook, hints, 80)))
	})
	t.Run("forward 120", func(t *testing.T) {
		golden.RequireEqual(t, noColor(t, Banner("Acme Inc. | Payments", ciHook, hints, 120)))
	})
	t.Run("inspect", func(t *testing.T) {
		routes := []BannerRoute{
			{SourceUID: "src_billing", Source: "billing", PublicURL: "https://in.hookspot.test/src_billing", Path: "/hooks/invoices", Label: "/hooks/invoices"},
			{SourceUID: "src_billing", Source: "billing", PublicURL: "https://in.hookspot.test/src_billing", Path: "/hooks/refunds", Label: "refunds"},
		}
		golden.RequireEqual(t, noColor(t, Banner("Acme Inc. | Billing", routes, []string{"ctrl-c quit"}, 80)))
	})
	t.Run("hostile", func(t *testing.T) {
		routes := []BannerRoute{{
			SourceUID:   "src_evil",
			Source:      "evil\x1b[31m\n",
			PublicURL:   "https://in.hookspot.test/path\tPrompt\x1b",
			Destination: "http://localhost:3000/\x07",
			Label:       "label\u009b2J",
		}}
		golden.RequireEqual(t, noColor(t, Banner("Acme\x1b]0;title\x07 | "+strings.Repeat("Long", 30), routes, nil, 80)))
	})
}

// cmd's tests pin the notices' wording; these pin escaping and rounding.
func TestNoticesEscapeServerTextAndRoundTheOutage(t *testing.T) {
	for _, test := range []struct {
		got, want string
	}{
		{got: DisabledSource("stripe\x1b[31m"), want: `⚠ stripe\x1b[31m is disabled`},
		{got: SkippedSource("github\n"), want: `⚠ github\n has no route`},
		{got: Reconnected(91400*time.Millisecond, "https://hookspot.test/requests"), want: "Reconnected after 1m31s offline."},
	} {
		if got := noColor(t, test.got); !strings.HasPrefix(got, test.want) {
			t.Errorf("got %q, want it to start with %q", got, test.want)
		}
	}
}

func TestTestEvent(t *testing.T) {
	t.Run("hint", func(t *testing.T) {
		stripe := api.Source{Name: "stripe", URL: "https://in.hookspot.test/src_stripe"}
		evil := api.Source{Name: "evil\x1b[31m", URL: "https://in.hookspot.test/it's\n\x1b[2J"}
		both := session.TestHint{Sources: []api.Source{stripe, evil}}
		golden.RequireEqual(t, noColor(t, joinCards(
			TestHint(session.TestHint{Sources: []api.Source{stripe}}, true),
			TestHint(both, true),
			TestHint(both, false),
		)))
	})
	t.Run("entries", func(t *testing.T) {
		d := testDelivery()
		d.Body = []byte(`{"type":"hookspot.test","sent_at":"2026-07-12T12:34:56Z"}`)
		ok := forwarded(1, d, http.StatusOK, 12*time.Millisecond)
		failed := forwarded(2, d, http.StatusInternalServerError, 3*time.Millisecond)
		failed.Response.Body = []byte("boom")
		refused := forwarded(3, d, 0, time.Millisecond)
		refused.Failure = &proxy.TransportFailure{Kind: proxy.TransportConnectionRefused}
		inspected := session.Entry{Number: 4, Delivery: d, Received: received}
		var entries []string
		for _, e := range []session.Entry{ok, failed, refused, inspected} {
			e.Test = true
			entries = append(entries, testListen().Entry(e, 80))
		}
		golden.RequireEqual(t, noColor(t, joinCards(entries...)))
	})
}

func TestStatus(t *testing.T) {
	hints := []string{"↵ replay last", "r N replay #N", "? help", "ctrl-c quit"}
	forwarded := session.Stats{Count: 12, OK: 10, Failed: 2, P50: 41 * time.Millisecond, Max: 900 * time.Millisecond}
	lines := []string{
		Status{Project: "Acme | Payments"}.Render(80),
		Prompt("", hints, 80),
		Status{State: StateLive, Project: "Acme | Payments", Totals: forwarded}.Render(80),
		Prompt("r 4\x1b", hints, 80),
		Status{State: StateLive, Project: "Acme | Payments", Totals: session.Stats{Count: 3}, Hints: []string{"ctrl-c quit"}}.Render(80),
		Status{State: StateOffline, Err: errors.New("dial tcp: connection refused\x1b[2J"), Project: "Acme | Payments", Totals: forwarded}.Render(80),
		Status{State: StateStopping, Project: "evil\x1b]0;title\x07\n", Hints: []string{"ctrl-c force quit"}}.Render(80),
		Status{State: StateStopped, Project: "Acme | Payments", Totals: forwarded}.Render(80),
		// Hints go first when a line doesn't fit, then the rest is cut.
		Status{State: StateLive, Project: "Acme | Payments", Totals: forwarded, Hints: hints}.Render(40),
		Prompt("r 4", hints, 40),
	}
	golden.RequireEqual(t, noColor(t, strings.Join(lines, "\n")))
}

func TestRequest(t *testing.T) {
	t.Run("success rows", func(t *testing.T) {
		l := testListen()
		priority := testDelivery()
		priority.Body = []byte(`{"action":"fallback","event_type":"third","event":"second","type":"first"}`)
		event, eventType, action := testDelivery(), testDelivery(), testDelivery()
		event.Body = []byte(`{"event":"second","action":"fallback"}`)
		eventType.Body = []byte(`{"event_type":"third","action":"fallback"}`)
		action.Body = []byte(`{"action":"fallback","type":""}`)
		mimeFallback := testDelivery()
		mimeFallback.Body = []byte(`{"id":"evt_1"}`)
		empty := testDelivery()
		empty.SourceUID = "src_shopify"
		empty.Method = ""
		empty.Body = nil
		unknown := testDelivery()
		unknown.SourceUID = "src_gone"
		unknown.Path = "/" + strings.Repeat("very-long-segment/", 8)

		testEvent := forwarded(7, eventType, http.StatusAccepted, 12400*time.Millisecond)
		testEvent.Test = true
		rows := []session.Entry{
			forwarded(1, priority, http.StatusOK, 38*time.Millisecond),
			forwarded(2, event, http.StatusOK, 112*time.Millisecond),
			forwarded(3, eventType, http.StatusOK, 999*time.Millisecond),
			forwarded(4, action, http.StatusOK, 9*time.Millisecond),
			forwarded(5, mimeFallback, http.StatusNoContent, 1250*time.Millisecond),
			forwarded(6, empty, http.StatusCreated, 100*time.Microsecond),
			testEvent,
			forwarded(1234, unknown, http.StatusOK, 4*time.Millisecond),
		}
		for _, width := range []int{80, 120} {
			var out []string
			for _, row := range rows {
				out = append(out, l.Entry(row, width))
			}
			limited := Listen{Sources: l.Sources, Limits: Limits{MaxValueChars: 5}}
			out = append(out, limited.Entry(forwarded(9, testDelivery(), http.StatusOK, time.Millisecond), width))
			t.Run(strconv.Itoa(width), func(t *testing.T) {
				golden.RequireEqual(t, noColor(t, strings.Join(out, "\n")))
			})
		}
	})

	t.Run("http failure", func(t *testing.T) {
		d := testDelivery()
		d.Query = ""
		d.Body = []byte(`{"id":"evt_1Q2w3E4r","object":"event","type":"invoice.payment_failed","data":{"object":{"customer":null,"lines":[1,2.5,-3e2],"paid":false}}}`)
		r := forwarded(45, d, http.StatusUnprocessableEntity, 9*time.Millisecond)
		r.Response.Headers = http.Header{"content-type": []string{"application/json"}}
		r.Response.Body = []byte(`{"error":"missing customer_id"}`)
		long := forwarded(46, d, http.StatusInternalServerError, 1500*time.Millisecond)
		long.Delivery.Path = "/" + strings.Repeat("deep/", 20)
		long.Response.Body = []byte("panic: boom\n\tat handler.go:12\n")
		golden.RequireEqual(t, noColor(t, joinCards(testListen().Entry(r, 80), testListen().Entry(long, 80))))
	})

	t.Run("redirect", func(t *testing.T) {
		r := forwarded(3, testDelivery(), http.StatusPermanentRedirect, 2*time.Millisecond)
		r.Response.Headers = http.Header{"Location": []string{"http://localhost:3000/webhooks/\x1b[31m"}, "Content-Type": []string{"text/html"}}
		r.Response.Body = []byte("<a href=\"/webhooks/\">Permanent Redirect</a>.")
		without := forwarded(4, testDelivery(), http.StatusTemporaryRedirect, 2*time.Millisecond)
		golden.RequireEqual(t, noColor(t, joinCards(testListen().Entry(r, 80), testListen().Entry(without, 80))))
	})

	t.Run("body limits", func(t *testing.T) {
		l := testListen()
		l.Limits = Limits{MaxBodyLines: 2, MaxValueChars: 4}
		d := testDelivery()
		d.Headers = http.Header{"Content-Type": []string{"text/plain"}}
		d.Body = []byte("123456\nsecond\nthird")
		r := forwarded(8, d, http.StatusBadRequest, 3*time.Millisecond)
		r.Response.Body = []byte(`{"error":"missing customer_id","code":42}`)
		golden.RequireEqual(t, noColor(t, l.Entry(r, 80)))
	})

	t.Run("transport failures", func(t *testing.T) {
		failure := func(number int, kind proxy.TransportErrorKind, target string) session.Entry {
			r := forwarded(number, testDelivery(), 0, 4*time.Millisecond)
			r.Target = target + "/api/webhooks"
			r.Failure = &proxy.TransportFailure{Kind: kind, Err: errors.New("dial tcp 10.0.0.1:443: i/o \x1b[2Jfailure")}
			return r
		}
		host, container := testListen(), testListen()
		container.Container = true
		limited := testListen()
		limited.Limits.MaxValueChars = 12
		golden.RequireEqual(t, noColor(t, joinCards(
			host.Entry(failure(1, proxy.TransportConnectionRefused, "http://localhost:3000"), 80),
			host.Entry(failure(2, proxy.TransportTimeout, "http://localhost:3000"), 80),
			host.Entry(failure(3, proxy.TransportDNS, "http://app.invalid:3000"), 80),
			host.Entry(failure(4, proxy.TransportTLS, "https://localhost:3443"), 80),
			host.Entry(failure(5, proxy.TransportOther, "http://localhost:3000"), 80),
			limited.Entry(failure(6, proxy.TransportOther, "http://localhost:3000"), 80),
			container.Entry(failure(7, proxy.TransportConnectionRefused, "http://localhost:3000"), 80),
			container.Entry(failure(8, proxy.TransportConnectionRefused, "http://[::1]:3000"), 80),
			container.Entry(failure(9, proxy.TransportConnectionRefused, "http://app:3000"), 80),
		)))
	})

	t.Run("replays", func(t *testing.T) {
		entry := func(number, status int, body string, latency time.Duration) session.Entry {
			return session.Entry{
				Number:   number,
				Delivery: testDelivery(),
				Received: received,
				Target:   "http://localhost:3000/api/webhooks",
				Response: ws.Response{Status: status, Body: []byte(body)},
				Latency:  latency,
			}
		}
		refused := func(number int) session.Entry {
			e := entry(number, 0, "", time.Millisecond)
			e.Failure = &proxy.TransportFailure{Kind: proxy.TransportConnectionRefused}
			return e
		}
		// Inside a container, a refused localhost gets its hint here too.
		container := testListen()
		container.Container = true
		replay := func(original, replay session.Entry) string {
			replay.ReplayOf = original.Number
			comparison := session.Compare(original, replay)
			replay.Replay = &comparison
			return container.Entry(replay, 80)
		}
		failed := entry(45, http.StatusUnprocessableEntity, `{"error":"missing customer_id"}`, 9*time.Millisecond)
		fixed := entry(46, http.StatusOK, `{"received":true}`, 41*time.Millisecond)
		lines := func(prefix string) string {
			var lines []string
			for i := range 10 {
				lines = append(lines, prefix+strconv.Itoa(i))
			}
			return strings.Join(lines, "\n")
		}
		hostile := entry(57, http.StatusBadRequest, "ok\tfine\x1b[31m\u009b2J\r\n"+strings.Repeat("long ", 30), time.Millisecond)
		golden.RequireEqual(t, noColor(t, strings.Join([]string{
			replay(failed, fixed),
			replay(failed, entry(47, http.StatusUnprocessableEntity, `{"error":"missing customer_id"}`, 8*time.Millisecond)),
			replay(failed, entry(48, http.StatusUnprocessableEntity, `{"error":"missing invoice_id"}`, 12*time.Millisecond)),
			replay(refused(49), entry(50, http.StatusOK, `{"received":true}`, 30*time.Millisecond)),
			replay(fixed, refused(51)),
			replay(entry(52, http.StatusOK, "\x89PNG\xff", time.Millisecond), entry(53, http.StatusOK, "\x89PNG\xfe\xff\x00", time.Millisecond)),
			replay(entry(54, http.StatusInternalServerError, lines("before "), time.Millisecond), entry(55, http.StatusInternalServerError, lines("after "), time.Millisecond)),
			replay(entry(56, http.StatusBadRequest, "ok", time.Millisecond), hostile),
		}, "\n")))
	})

	t.Run("inspect", func(t *testing.T) {
		d := testDelivery()
		d.Headers["X-Long"] = []string{strings.Repeat("unlimited ", 20)}
		r := session.Entry{Number: 12, Delivery: d, Received: received, Test: true}
		golden.RequireEqual(t, noColor(t, testListen().Entry(r, 80)))
	})

	t.Run("inspect redaction list", func(t *testing.T) {
		d := testDelivery()
		d.Query = ""
		d.Body = nil
		d.Headers = http.Header{"Content-Type": []string{"application/json"}}
		for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-CLI-Key", "X-API-Key", "Api-Key", "X-Hookspot-CLI-Key"} {
			d.Headers[name] = []string{"sentinel-" + name}
		}
		r := session.Entry{Number: 1, Delivery: d, Received: received}
		hidden := noColor(t, testListen().Entry(r, 80))
		if strings.Contains(hidden, "sentinel") {
			t.Fatalf("a sensitive header value was shown:\n%s", hidden)
		}
		shown := testListen()
		shown.ShowSensitiveHeaders = true
		golden.RequireEqual(t, joinCards(hidden, noColor(t, shown.Entry(r, 80))))
	})

	t.Run("inspect limits", func(t *testing.T) {
		l := testListen()
		l.Limits = Limits{MaxBodyLines: 2, MaxHeaders: 1, MaxValueChars: 4}
		d := testDelivery()
		d.Query = "token=abcdefgh&ok=yes&flag"
		d.Headers = http.Header{"A-First": []string{"abcdefgh", "second"}, "B-Next": []string{"second"}, "C-Last": []string{"third"}}
		d.Body = []byte("123456\nsecond\nthird")
		golden.RequireEqual(t, noColor(t, l.Entry(session.Entry{Number: 2, Delivery: d, Received: received}, 80)))
	})

	t.Run("inspect binary and empty", func(t *testing.T) {
		binary := testDelivery()
		binary.Query = ""
		binary.Headers = http.Header{"Content-Type": []string{"application/octet-stream"}}
		binary.Body = []byte{0x00, 0x1b, 0xff}
		empty := ws.Delivery{SourceUID: "src_shopify", Method: "PUT", Path: "/"}
		sniffed := testDelivery()
		sniffed.Query = ""
		sniffed.Headers = nil
		sniffed.Body = []byte("plain text body")
		golden.RequireEqual(t, noColor(t, joinCards(
			testListen().Entry(session.Entry{Number: 3, Delivery: binary, Received: received}, 80),
			testListen().Entry(session.Entry{Number: 4, Delivery: empty, Received: received}, 80),
			testListen().Entry(session.Entry{Number: 5, Delivery: sniffed, Received: received}, 80),
		)))
	})

	t.Run("hostile", func(t *testing.T) {
		l := Listen{Sources: map[string]string{"src_evil": "evil\x1b]0;title\x07\n"}}
		d := ws.Delivery{
			RequestUID: "req\x07\u009b",
			SourceUID:  "src_evil",
			Method:     "PO\x1bST",
			Path:       "/hook\x1b[2J\r\n",
			Query:      "a\x1b=b\tc",
			Headers: http.Header{
				"X-Evil\x1b[31m":  []string{"value\x00\r\n\u0085"},
				"X-Trace\tPrompt": []string{"\x1b]8;;http://evil.test\x07link"},
				"Content-Type":    []string{"text/plain"},
			},
			Body: []byte("hello\tworld\x1b[31m\r\nline\x7f\u009b2J\x00"),
		}
		jsonDelivery := d
		jsonDelivery.Headers = http.Header{"Content-Type": []string{"application/json"}}
		jsonDelivery.Body = []byte("{\"type\":\"evil\u009b31m\x7f\",\"key\xff\":\"\\u001b[2J \\\"quoted\\\"\"}")
		failed := session.Entry{Number: 2, Delivery: jsonDelivery, Received: received, Target: "http://localhost:3000/\x1b[2J", Response: ws.Response{Status: http.StatusBadRequest, Headers: d.Headers, Body: d.Body}}
		// A long method and source name can't push a row past the width.
		long := Listen{Sources: map[string]string{"src_evil": strings.Repeat("evil\x1b", 20)}}
		longMethod := jsonDelivery
		longMethod.Method = strings.Repeat("PROPFIND", 20)
		golden.RequireEqual(t, noColor(t, joinCards(
			l.Entry(session.Entry{Number: 1, Delivery: d, Received: received}, 80),
			l.Entry(failed, 80),
			l.Entry(forwarded(3, jsonDelivery, http.StatusOK, time.Millisecond), 80),
			long.Entry(forwarded(4, longMethod, http.StatusOK, time.Millisecond), 80),
		)))
	})
}

func TestNarrowWidthsKeepRendering(t *testing.T) {
	l := testListen()
	refused := forwarded(1, testDelivery(), 0, time.Millisecond)
	refused.Failure = &proxy.TransportFailure{Kind: proxy.TransportConnectionRefused}
	refused.Replay = &session.Comparison{Original: 2, Status: http.StatusOK, Removed: []string{"{"}, Added: []string{"}"}, More: 1, Binary: true}
	requests := []session.Entry{
		forwarded(1, testDelivery(), http.StatusOK, time.Millisecond),
		forwarded(2, testDelivery(), http.StatusBadGateway, time.Millisecond),
		refused,
		{Number: 3, Delivery: testDelivery(), Received: received, Test: true},
	}
	routes := []BannerRoute{{SourceUID: "src_stripe", Source: "stripe", PublicURL: "https://in.hookspot.test/src_stripe", Destination: "http://localhost:3000/"}}
	for width := range 24 {
		Banner("Acme | Payments", routes, []string{"ctrl-c quit"}, width)
		for _, r := range requests {
			l.Entry(r, width)
		}
	}
}
