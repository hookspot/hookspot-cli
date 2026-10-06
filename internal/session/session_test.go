package session

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"hookspot/internal/api"
	"hookspot/internal/proxy"
	"hookspot/internal/ws"
)

var testSources = []api.Source{{UID: "src_stripe", Routes: []api.Route{
	{UID: "rte_orders", Destination: api.Destination{Path: "/orders"}},
	{UID: "rte_refunds", Destination: api.Destination{Path: "/refunds"}},
}}}

func delivery(route, path string) ws.Delivery {
	return ws.Delivery{
		AttemptUID: "att_1",
		RequestUID: "req_1",
		SourceUID:  "src_stripe",
		RouteUID:   route,
		Method:     http.MethodPut,
		Path:       path,
		Query:      "a=1",
		Headers:    http.Header{"X-Test": []string{"original"}},
		Body:       []byte(`{"type":"created"}`),
	}
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// reply is what the local target does with one forward; bodyErr fails
// reading its body.
type reply struct {
	status  int
	header  http.Header
	body    string
	latency time.Duration
	err     error
	bodyErr error
}

// fakeForwarder plays its replies in order, then answers 200 at once. Each
// reply's latency advances the clock.
type fakeForwarder struct {
	clock   *clock
	mu      sync.Mutex
	replies []reply
	calls   []ws.Delivery
}

func (f *fakeForwarder) Forward(_ context.Context, method, path, query string, body []byte, headers http.Header) (*http.Response, error) {
	f.mu.Lock()
	r := reply{status: http.StatusOK}
	if len(f.replies) > 0 {
		r, f.replies = f.replies[0], f.replies[1:]
	}
	f.calls = append(f.calls, ws.Delivery{Method: method, Path: path, Query: query, Body: body, Headers: headers})
	f.mu.Unlock()
	runtime.Gosched()
	f.clock.Add(r.latency)
	if r.err != nil {
		return nil, r.err
	}
	reader := io.Reader(strings.NewReader(r.body))
	if r.bodyErr != nil {
		reader = iotest.ErrReader(r.bodyErr)
	}
	return &http.Response{StatusCode: r.status, Header: r.header, Body: io.NopCloser(reader)}, nil
}

func (f *fakeForwarder) DestinationURL(path, _ string) (*url.URL, error) {
	return url.Parse("http://localhost:3000" + path)
}

func (f *fakeForwarder) String() string { return "http://localhost:3000" }

type recorder struct {
	mu     sync.Mutex
	events []Event
	err    error
}

func (r *recorder) Emit(event Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return r.err
}

func (r *recorder) recorded() []Recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	var recorded []Recorded
	for _, event := range r.events {
		if e, ok := event.(Recorded); ok {
			recorded = append(recorded, e)
		}
	}
	return recorded
}

func newTestSession(replies ...reply) (*Session, *fakeForwarder, *recorder) {
	c := &clock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	forwarder := &fakeForwarder{clock: c, replies: replies}
	sink := &recorder{}
	s := New(context.Background(), testSources, forwarder, sink)
	s.now = c.Now
	return s, forwarder, sink
}

func TestHandleAnswersHookspot(t *testing.T) {
	s, _, sink := newTestSession(
		reply{status: http.StatusCreated, header: http.Header{"X-Reply": []string{"yes"}}, body: "created", latency: 3 * time.Millisecond},
		reply{err: errors.New("network unavailable"), latency: 7 * time.Millisecond},
		reply{status: http.StatusOK, bodyErr: errors.New("connection reset mid-body")},
		reply{status: http.StatusOK, body: strings.Repeat("x", maxLocalResponseBodyBytes+1)},
	)

	response, err := s.Handle(delivery("rte_orders", "/orders"))
	want := ws.Response{Status: http.StatusCreated, Headers: http.Header{"X-Reply": []string{"yes"}}, Body: []byte("created"), LatencyMS: 3}
	if err != nil || !reflect.DeepEqual(response, want) {
		t.Fatalf("Handle = %#v, %v; want %#v", response, err, want)
	}

	response, err = s.Handle(delivery("rte_orders", "/orders"))
	if err != nil || !reflect.DeepEqual(response, ws.Response{Status: http.StatusBadGateway, LatencyMS: 7}) {
		t.Fatalf("Handle after a transport failure = %#v, %v; want 502", response, err)
	}
	failed := sink.recorded()[1].Entry
	if failed.Failure == nil || failed.Failure.Kind != proxy.TransportOther || failed.Target != "http://localhost:3000/orders" {
		t.Fatalf("transport failure entry = %#v", failed)
	}

	// A target that drops the connection mid-body failed to answer; listen goes on.
	response, err = s.Handle(delivery("rte_orders", "/orders"))
	if err != nil || response.Status != http.StatusBadGateway {
		t.Fatalf("Handle after a failed response body = %#v, %v; want 502", response, err)
	}
	if cut := sink.recorded()[2].Entry; cut.Failure == nil || cut.Failure.Kind != proxy.TransportOther {
		t.Fatalf("failed response body entry = %#v, want a transport failure", cut)
	}

	response, err = s.Handle(delivery("rte_orders", "/orders"))
	if err == nil || !strings.Contains(err.Error(), "local response body exceeds 16 MiB limit") || response.Status != 0 {
		t.Fatalf("Handle with an oversized response = %#v, %v; want no acknowledgement", response.Status, err)
	}
	if got := len(sink.recorded()); got != 3 {
		t.Fatalf("recorded %d entries, want the oversized response left out", got)
	}
}

func TestInspectModeAnswers200(t *testing.T) {
	sink := &recorder{}
	s := New(context.Background(), testSources, nil, sink)

	response, err := s.Handle(delivery("rte_orders", "/orders"))
	if err != nil || !reflect.DeepEqual(response, ws.Response{Status: http.StatusOK}) {
		t.Fatalf("Handle = %#v, %v; want 200", response, err)
	}
	recorded := sink.recorded()
	if len(recorded) != 1 || recorded[0].Entry.Number != 1 || recorded[0].Entry.Target != "" || recorded[0].Entry.RouteUID != "rte_orders" {
		t.Fatalf("recorded = %#v, want #1 of rte_orders with no target", recorded)
	}
	if totals := recorded[0].Totals; totals.Count != 1 || totals.OK != 0 || totals.Failed != 0 || totals.P50 != 0 {
		t.Fatalf("totals = %#v, want a count without forwarding stats", totals)
	}
	if err := s.Replay(1); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("Replay in inspect mode = %v, want ErrNoTarget", err)
	}
	if err := s.ReplayLast(); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("ReplayLast in inspect mode = %v, want ErrNoTarget", err)
	}
}

func TestReplaysAreNumberedEntriesOfTheirOriginal(t *testing.T) {
	s, forwarder, sink := newTestSession()
	if err := s.ReplayLast(); err != nil || len(forwarder.calls) != 0 {
		t.Fatalf("ReplayLast before any request = %v with %d forwards, want a no-op", err, len(forwarder.calls))
	}

	d := delivery("rte_orders", "/orders")
	if _, err := s.Handle(d); err != nil {
		t.Fatal(err)
	}
	d.Body[0] = 'X'
	d.Headers.Set("X-Test", "mutated")
	if _, err := s.Handle(delivery("rte_refunds", "/refunds")); err != nil {
		t.Fatal(err)
	}
	if err := s.Replay(1); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplayLast(); err != nil {
		t.Fatal(err)
	}

	var numbers, replayOf []int
	for _, e := range sink.recorded() {
		numbers = append(numbers, e.Entry.Number)
		replayOf = append(replayOf, e.Entry.ReplayOf)
	}
	if !reflect.DeepEqual(numbers, []int{1, 2, 3, 4}) || !reflect.DeepEqual(replayOf, []int{0, 0, 1, 3}) {
		t.Fatalf("numbers = %v, replay of = %v", numbers, replayOf)
	}
	replay := sink.recorded()[2].Entry
	if replay.RouteUID != "rte_orders" || replay.Delivery.RequestUID != "req_1" {
		t.Fatalf("replay entry = %#v, want the original's route and request", replay)
	}
	if len(forwarder.calls) != 4 {
		t.Fatalf("local forwards = %d, want 4", len(forwarder.calls))
	}
	if sent := forwarder.calls[2]; string(sent.Body) != `{"type":"created"}` || sent.Headers.Get("X-Test") != "original" || sent.Query != "a=1" {
		t.Fatalf("replayed request = %#v, want the original delivery", sent)
	}
}

func TestHistoryEviction(t *testing.T) {
	evictions := func(sink *recorder) [][]int {
		var evicted [][]int
		for _, e := range sink.recorded() {
			evicted = append(evicted, e.Evicted)
		}
		return evicted
	}

	t.Run("by count", func(t *testing.T) {
		s, _, sink := newTestSession()
		s.history.maxEntries = 3
		for range 5 {
			if _, err := s.Handle(delivery("rte_orders", "/orders")); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Replay(2); !errors.Is(err, ErrEvicted) || err.Error() != "#2: request dropped from history" {
			t.Fatalf("Replay(2) = %v, want ErrEvicted", err)
		}
		for _, n := range []int{0, 6} {
			if err := s.Replay(n); !errors.Is(err, ErrUnknown) {
				t.Fatalf("Replay(%d) = %v, want ErrUnknown", n, err)
			}
		}
		if err := s.Replay(5); err != nil {
			t.Fatal(err)
		}
		if got, want := evictions(sink), [][]int{nil, nil, nil, {1}, {2}, {3}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("evicted = %v, want %v", got, want)
		}
	})

	t.Run("by bytes", func(t *testing.T) {
		// Delivery and response bodies both count: each entry holds 4 + 2 bytes.
		var replies []reply
		for range 5 {
			replies = append(replies, reply{status: http.StatusOK, body: "ok"})
		}
		s, _, sink := newTestSession(replies...)
		s.history.maxBytes = 13
		for _, body := range []string{"1111", "2222", "3333", "4444", strings.Repeat("5", 20)} {
			d := delivery("rte_orders", "/orders")
			d.Body = []byte(body)
			if _, err := s.Handle(d); err != nil {
				t.Fatal(err)
			}
		}
		// The newest entry stays even when it alone exceeds the cap.
		if got, want := evictions(sink), [][]int{nil, nil, {1}, {2}, {3, 4}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("evicted = %v, want %v", got, want)
		}
		if err := s.Replay(4); !errors.Is(err, ErrEvicted) {
			t.Fatalf("Replay(4) = %v, want ErrEvicted", err)
		}
	})
}

func TestStats(t *testing.T) {
	s, forwarder, sink := newTestSession(
		reply{status: http.StatusOK, latency: 10 * time.Millisecond},
		reply{status: http.StatusOK, latency: 40 * time.Millisecond},
		reply{status: http.StatusOK, latency: 20 * time.Millisecond},
		reply{err: context.DeadlineExceeded, latency: 100 * time.Millisecond},
		reply{err: errors.New("connection reset by peer"), latency: 5 * time.Millisecond},
		reply{status: http.StatusInternalServerError, latency: 50 * time.Millisecond},
		reply{status: http.StatusOK, latency: 30 * time.Millisecond},
		reply{status: http.StatusOK, latency: time.Second},
	)
	for range 6 {
		if _, err := s.Handle(delivery("rte_orders", "/orders")); err != nil {
			t.Fatal(err)
		}
	}
	forwarder.clock.Add(2 * time.Minute)
	if _, err := s.Handle(delivery("", "/unmatched")); err != nil {
		t.Fatal(err)
	}
	if err := s.Replay(1); err != nil {
		t.Fatal(err)
	}

	recorded := sink.recorded()
	if timeout := recorded[3].Entry; timeout.Failure == nil || timeout.Failure.Kind != proxy.TransportTimeout {
		t.Fatalf("entry #4 = %#v, want a timeout", timeout)
	}
	if unmatched := recorded[6]; unmatched.Entry.RouteUID != "" || !reflect.DeepEqual(unmatched.Route, Stats{}) {
		t.Fatalf("unmatched entry = %#v, want no route stats", unmatched)
	}
	// Timeouts count at their duration; the reset at 5ms and the replay at 1s don't count.
	summary := func(stats Stats) Stats {
		stats.Last = Entry{Number: stats.Last.Number}
		return stats
	}
	final := recorded[7]
	if last := final.Totals.Last.Delivery; last.Body != nil || last.Headers != nil {
		t.Fatalf("last entry keeps its delivery's headers and body: %#v", last)
	}
	minute := time.Date(2026, 10, 1, 12, 2, 0, 0, time.UTC)
	wantRoute := Stats{
		Count: 6, OK: 3, Failed: 3,
		P50: 40 * time.Millisecond, P95: 100 * time.Millisecond, Max: 100 * time.Millisecond,
		Outcomes:  map[Outcome]int{{Status: http.StatusOK}: 3, {Failure: proxy.TransportTimeout}: 1, {Failure: proxy.TransportOther}: 1, {Status: http.StatusInternalServerError}: 1},
		PerMinute: [StatsMinutes]int{12: 6},
		Minute:    minute,
		Last:      Entry{Number: 6},
	}
	if got := summary(final.Route); !reflect.DeepEqual(got, wantRoute) {
		t.Fatalf("route stats = %+v\nwant %+v", got, wantRoute)
	}
	wantTotals := Stats{
		Count: 7, OK: 4, Failed: 3,
		P50: 30 * time.Millisecond, P95: 100 * time.Millisecond, Max: 100 * time.Millisecond,
		Outcomes:  map[Outcome]int{{Status: http.StatusOK}: 4, {Failure: proxy.TransportTimeout}: 1, {Failure: proxy.TransportOther}: 1, {Status: http.StatusInternalServerError}: 1},
		PerMinute: [StatsMinutes]int{12: 6, 14: 1},
		Minute:    minute,
		Last:      Entry{Number: 7},
	}
	if got := summary(final.Totals); !reflect.DeepEqual(got, wantTotals) {
		t.Fatalf("totals = %+v\nwant %+v", got, wantTotals)
	}
}

func TestLatencyKeepsTheNewestSamples(t *testing.T) {
	var s stats
	forwarded := func(latency time.Duration) Entry {
		return Entry{Target: "http://localhost:3000", Response: ws.Response{Status: http.StatusOK}, Latency: latency}
	}
	s.add(forwarded(5 * time.Second))
	for range maxLatencySamples {
		s.add(forwarded(time.Millisecond))
	}
	if got := s.snapshot(time.Time{}).Max; got != time.Millisecond {
		t.Fatalf("max = %s, want the oldest sample dropped", got)
	}
}

func TestPerMinuteWindowDropsOldMinutes(t *testing.T) {
	start := time.Date(2026, 10, 1, 12, 0, 30, 0, time.UTC)
	var s stats
	for _, offset := range []time.Duration{0, time.Minute, 3 * time.Minute, 20 * time.Minute, 21 * time.Minute} {
		s.add(Entry{Received: start.Add(offset)})
	}
	if got, want := s.snapshot(start.Add(22*time.Minute)).PerMinute, [StatsMinutes]int{12: 1, 13: 1}; got != want {
		t.Fatalf("per minute = %v, want %v", got, want)
	}
	if got := s.snapshot(start.Add(time.Hour)).PerMinute; got != [StatsMinutes]int{} {
		t.Fatalf("per minute an hour later = %v, want none", got)
	}
}

func TestPerMinuteAtMovesAnOldSnapshotOn(t *testing.T) {
	start := time.Date(2026, 10, 1, 12, 0, 30, 0, time.UTC)
	var s stats
	s.add(Entry{Received: start})
	s.add(Entry{Received: start.Add(time.Minute)})
	snapshot := s.snapshot(start.Add(time.Minute))
	if got, want := snapshot.PerMinuteAt(start.Add(3*time.Minute)), [StatsMinutes]int{11: 1, 12: 1}; got != want {
		t.Fatalf("per minute 2 minutes on = %v, want %v", got, want)
	}
	if got := snapshot.PerMinuteAt(start); got != snapshot.PerMinute {
		t.Fatalf("per minute before the snapshot = %v, want it unmoved", got)
	}
}

func TestSinkErrorIsReturned(t *testing.T) {
	wantErr := errors.New("output unavailable")
	// A failed Ready ends listen, so no hint follows it.
	ready := &recorder{err: wantErr}
	if err := New(context.Background(), testSources, nil, ready).Emit(Ready{}); !errors.Is(err, wantErr) || len(ready.events) != 1 {
		t.Fatalf("Emit(Ready) = %v with events %#v, want the sink error and no hint", err, ready.events)
	}

	s, _, sink := newTestSession(reply{
		status: http.StatusTemporaryRedirect,
		header: http.Header{"Location": []string{"/next"}},
		body:   "redirect response",
	})
	sink.err = wantErr

	response, err := s.Handle(delivery("rte_orders", "/orders"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Handle error = %v, want the sink error", err)
	}
	if response.Status != http.StatusTemporaryRedirect || response.Headers.Get("Location") != "/next" || string(response.Body) != "redirect response" {
		t.Fatalf("completed response changed after the sink failed: %#v", response)
	}
	if err := s.Replay(1); !errors.Is(err, wantErr) {
		t.Fatalf("Replay error = %v, want the sink error", err)
	}

	inspect := New(context.Background(), testSources, nil, sink)
	if response, err := inspect.Handle(delivery("rte_orders", "/orders")); !errors.Is(err, wantErr) || response.Status != 0 {
		t.Fatalf("inspect Handle = %#v, %v; want no acknowledgement", response, err)
	}
}

func TestRootNotFoundHint(t *testing.T) {
	tests := []struct {
		name   string
		base   string
		path   string
		status int
		want   bool
	}{
		{name: "root 404", path: "/", status: http.StatusNotFound, want: true},
		{name: "root 405", path: "/", status: http.StatusMethodNotAllowed, want: true},
		{name: "root 500", path: "/", status: http.StatusInternalServerError},
		{name: "non-root path", path: "/hooks", status: http.StatusNotFound},
		{name: "root under a base path", base: "/webhooks", path: "/", status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer local.Close()
			forwarder, err := proxy.New(local.URL + test.base)
			if err != nil {
				t.Fatal(err)
			}
			sink := &recorder{}
			s := New(context.Background(), testSources, forwarder, sink)
			for range 2 {
				if _, err := s.Handle(ws.Delivery{RequestUID: "req_1", Method: http.MethodPost, Path: test.path}); err != nil {
					t.Fatal(err)
				}
			}

			var hints []Event
			for i, event := range sink.events {
				if _, ok := event.(RootNotFound); ok {
					hints = append(hints, event)
					if i != 1 {
						t.Fatalf("hint is event %d, want it right after the first entry", i)
					}
				}
			}
			want := []Event(nil)
			if test.want {
				want = []Event{RootNotFound{Root: local.URL + "/", Status: test.status}}
			}
			if !reflect.DeepEqual(hints, want) {
				t.Fatalf("hints = %#v, want %#v", hints, want)
			}
		})
	}
}

func TestLatencyMilliseconds(t *testing.T) {
	tests := []struct {
		latency time.Duration
		want    int64
	}{
		{latency: 0, want: 0},
		{latency: -time.Millisecond, want: 0},
		{latency: 100 * time.Microsecond, want: 1},
		{latency: 38*time.Millisecond + 400*time.Microsecond, want: 38},
		{latency: 38*time.Millisecond + 600*time.Microsecond, want: 39},
	}
	for _, test := range tests {
		if got := latencyMilliseconds(test.latency); got != test.want {
			t.Errorf("latencyMilliseconds(%s) = %d, want %d", test.latency, got, test.want)
		}
	}
}
