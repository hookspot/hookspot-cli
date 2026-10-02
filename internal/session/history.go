package session

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"hookspot/internal/proxy"
	"hookspot/internal/ws"
)

const (
	maxHistoryEntries = 1000
	maxHistoryBytes   = 64 * 1024 * 1024
)

var (
	// ErrUnknown names a number this run hasn't reached.
	ErrUnknown = errors.New("no such request")
	// ErrEvicted names a number history dropped to stay within its caps.
	ErrEvicted = errors.New("request dropped from history")
)

// Entry is one numbered request: a delivery or a replay, and what forwarding
// it did. Entries never change once recorded.
type Entry struct {
	Number   int
	Delivery ws.Delivery
	// RouteUID is the route that produced the delivery; "" when unmatched.
	RouteUID string
	// Target is the URL the delivery was forwarded to; "" in inspect mode.
	Target   string
	Response ws.Response
	Latency  time.Duration
	// Failure is set when forwarding got no HTTP response.
	Failure  *proxy.TransportFailure
	Received time.Time
	// ReplayOf is the number of the entry this one replays; 0 for a delivery.
	ReplayOf int
	// Replay compares a replay with the entry it replays; nil for a delivery.
	Replay *Comparison
	// Test is set on a delivery of a test event this run sent.
	Test bool
}

// forwarded reports whether the entry went to a local target.
func (e Entry) forwarded() bool { return e.Target != "" }

// Failed reports whether the entry was forwarded and got no 2xx.
func (e Entry) Failed() bool {
	return e.forwarded() && (e.Failure != nil || !Success(e.Response.Status))
}

// Success reports whether status is 2xx.
func Success(status int) bool {
	return status >= 200 && status < 300
}

// StatusText is status with its name, such as 500 Internal Server Error.
func StatusText(status int) string {
	if text := http.StatusText(status); text != "" {
		return strconv.Itoa(status) + " " + text
	}
	return strconv.Itoa(status)
}

// Timed reports whether Latency is how long the target took: transport
// failures other than a timeout end before it answers.
func (e Entry) Timed() bool {
	return e.Failure == nil || e.Failure.Kind == proxy.TransportTimeout
}

// history keeps the newest entries within a count and a body-size cap.
type history struct {
	maxEntries int
	maxBytes   int
	// entries are oldest first and numbered consecutively up to last.
	entries []Entry
	bytes   int
	last    int
}

// add never evicts the new entry, even one over the caps on its own.
func (h *history) add(entry Entry) (Entry, []int) {
	h.last++
	entry.Number = h.last
	h.entries = append(h.entries, entry)
	h.bytes += bodyBytes(entry)
	var evicted []int
	for len(h.entries) > 1 && (len(h.entries) > h.maxEntries || h.bytes > h.maxBytes) {
		oldest := h.entries[0]
		h.entries[0] = Entry{} // releases its bodies
		h.entries = h.entries[1:]
		h.bytes -= bodyBytes(oldest)
		evicted = append(evicted, oldest.Number)
	}
	return entry, evicted
}

func (h *history) get(n int) (Entry, error) {
	first := h.last - len(h.entries) + 1
	switch {
	case n < 1 || n > h.last:
		return Entry{}, fmt.Errorf("#%d: %w", n, ErrUnknown)
	case n < first:
		return Entry{}, fmt.Errorf("#%d: %w", n, ErrEvicted)
	}
	return h.entries[n-first], nil
}

func bodyBytes(entry Entry) int {
	return len(entry.Delivery.Body) + len(entry.Response.Body)
}

func cloneDelivery(delivery ws.Delivery) ws.Delivery {
	delivery.Body = append([]byte(nil), delivery.Body...)
	delivery.Headers = delivery.Headers.Clone()
	return delivery
}
