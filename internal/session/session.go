// Package session is listen's UI-free core: it forwards deliveries and
// replays, numbers them in a bounded history, keeps per-route stats, and
// reports everything to a Sink in one order.
package session

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"hookspot/internal/api"
	"hookspot/internal/proxy"
	"hookspot/internal/ws"
)

const maxLocalResponseBodyBytes = 16 * 1024 * 1024

var errLocalResponseBodyTooLarge = errors.New("local response body exceeds 16 MiB limit")

// ErrNoTarget refuses a replay in inspect mode, where nothing local answers.
var ErrNoTarget = errors.New("nothing to replay without --forward-to")

// Forwarder sends a delivery to the local target.
type Forwarder interface {
	Forward(ctx context.Context, method, path, query string, body []byte, headers http.Header) (*http.Response, error)
	DestinationURL(path, query string) (*url.URL, error)
	// String is the --forward-to base URL.
	String() string
}

// Sink receives events in record order. An error from an entry's event fails
// the delivery or replay that produced it.
type Sink interface {
	Emit(Event) error
}

// Event is a Connecting, Ready, ConnectionLost, Reconnected, RootNotFound or
// Recorded.
type Event interface{ event() }

// Connecting precedes the first join.
type Connecting struct{}

// Ready follows the first join.
type Ready struct{}

// ConnectionLost reports a failed session and the coming retry.
type ConnectionLost struct {
	Err     error
	RetryIn time.Duration
}

// Reconnected follows every join after the first. Requests that arrived while
// offline are never retried.
type Reconnected struct {
	Offline time.Duration
}

// RootNotFound is the once-per-run hint after a 404 or 405 at the bare
// --forward-to root, which usually lacks the app's webhook route.
type RootNotFound struct {
	Root   string
	Status int
}

// Recorded reports an entry just added to the history, with stats as of then.
type Recorded struct {
	Entry Entry
	// Route is the stats of the entry's route; zero when unmatched.
	Route  Stats
	Totals Stats
	// Evicted lists the numbers history dropped to make room, oldest first.
	Evicted []int
}

func (Connecting) event()     {}
func (Ready) event()          {}
func (ConnectionLost) event() {}
func (Reconnected) event()    {}
func (RootNotFound) event()   {}
func (Recorded) event()       {}

// Session is one listen run. Recording an entry and emitting it happen under
// one mutex, so numbers always match the order events reach the sink.
type Session struct {
	ctx       context.Context
	sources   []api.Source
	forwarder Forwarder
	sink      Sink
	now       func() time.Time

	mu         sync.Mutex
	history    history
	totals     stats
	routes     map[string]*stats
	rootHinted bool
}

// New starts a session; forwarder is nil in inspect mode.
func New(ctx context.Context, sources []api.Source, forwarder Forwarder, sink Sink) *Session {
	return &Session{
		ctx:       ctx,
		sources:   sources,
		forwarder: forwarder,
		sink:      sink,
		now:       time.Now,
		history:   history{maxEntries: maxHistoryEntries, maxBytes: maxHistoryBytes},
		routes:    map[string]*stats{},
	}
}

// Emit sends a connection state or notice to the sink, in order with entries.
func (s *Session) Emit(event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sink.Emit(event)
}

// Handle is the websocket handler: it forwards the delivery, or only records
// it in inspect mode, and returns Hookspot's response. A sink error comes with
// the response, so a completed forward is still acknowledged.
func (s *Session) Handle(delivery ws.Delivery) (ws.Response, error) {
	entry := Entry{Delivery: cloneDelivery(delivery), Received: s.now()}
	if route, ok := RouteFor(s.sources, delivery); ok {
		entry.RouteUID = route.UID
	}
	if s.forwarder == nil {
		if err := s.record(entry); err != nil {
			return ws.Response{}, err
		}
		return ws.Response{Status: http.StatusOK}, nil
	}

	entry, err := s.forward(entry)
	if err != nil {
		return ws.Response{}, err
	}
	response := entry.Response
	if entry.Failure != nil {
		response = ws.Response{Status: http.StatusBadGateway, LatencyMS: latencyMilliseconds(entry.Latency)}
	}
	err = s.record(entry)
	return response, err
}

// Replay forwards entry n again as a new entry. It's local only: the
// delivery's status in Hookspot never changes.
func (s *Session) Replay(n int) error {
	if s.forwarder == nil {
		return ErrNoTarget
	}
	s.mu.Lock()
	original, err := s.history.get(n)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	entry, err := s.forward(Entry{Delivery: original.Delivery, RouteUID: original.RouteUID, Received: s.now(), ReplayOf: n})
	if err != nil {
		return err
	}
	return s.record(entry)
}

// ReplayLast replays the newest entry; it does nothing before the first.
func (s *Session) ReplayLast() error {
	s.mu.Lock()
	last := s.history.last
	s.mu.Unlock()
	if last == 0 {
		return nil
	}
	return s.Replay(last)
}

func (s *Session) forward(entry Entry) (Entry, error) {
	d := entry.Delivery
	entry.Target = s.forwarder.String()
	if destination, err := s.forwarder.DestinationURL(d.Path, ""); err == nil {
		entry.Target = destination.String()
	}
	started := s.now()
	response, err := s.forwarder.Forward(s.ctx, d.Method, d.Path, d.Query, d.Body, d.Headers)
	if err != nil {
		entry.Latency = s.now().Sub(started)
		entry.Failure = proxy.Failure(err)
		return entry, nil
	}
	body, err := readLocalResponseBody(response.Body)
	_ = response.Body.Close()
	entry.Latency = s.now().Sub(started)
	if errors.Is(err, errLocalResponseBodyTooLarge) {
		return Entry{}, err
	}
	if err != nil {
		entry.Failure = proxy.Failure(err)
		return entry, nil
	}
	entry.Response = ws.Response{
		Status:    response.StatusCode,
		Headers:   response.Header.Clone(),
		Body:      body,
		LatencyMS: latencyMilliseconds(entry.Latency),
	}
	return entry, nil
}

// record numbers entry, updates the stats and emits it, then the root hint
// when entry earns it.
func (s *Session) record(entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, evicted := s.history.add(entry)
	if entry.ReplayOf == 0 {
		s.totals.add(entry)
		if entry.RouteUID != "" {
			s.route(entry.RouteUID).add(entry)
		}
	}
	now := s.now()
	event := Recorded{Entry: entry, Totals: s.totals.snapshot(now), Evicted: evicted}
	if entry.RouteUID != "" {
		event.Route = s.route(entry.RouteUID).snapshot(now)
	}
	if err := s.sink.Emit(event); err != nil {
		return err
	}

	status := entry.Response.Status
	if s.rootHinted || (status != http.StatusNotFound && status != http.StatusMethodNotAllowed) {
		return nil
	}
	if target, err := url.Parse(entry.Target); err != nil || target.Path != "/" {
		return nil
	}
	s.rootHinted = true
	return s.sink.Emit(RootNotFound{Root: entry.Target, Status: status})
}

func (s *Session) route(uid string) *stats {
	if s.routes[uid] == nil {
		s.routes[uid] = &stats{}
	}
	return s.routes[uid]
}

func readLocalResponseBody(body io.Reader) ([]byte, error) {
	contents, err := io.ReadAll(io.LimitReader(body, maxLocalResponseBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(contents) > maxLocalResponseBodyBytes {
		return nil, errLocalResponseBodyTooLarge
	}
	return contents, nil
}

func latencyMilliseconds(latency time.Duration) int64 {
	if latency <= 0 {
		return 0
	}
	rounded := latency.Round(time.Millisecond)
	if rounded < time.Millisecond {
		return 1
	}
	return rounded.Milliseconds()
}
