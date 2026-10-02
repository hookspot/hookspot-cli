package session

import (
	"maps"
	"slices"
	"time"

	"hookspot/internal/proxy"
)

const (
	// StatsMinutes is how many minutes PerMinute covers.
	StatsMinutes      = 15
	maxLatencySamples = 1000
)

// Stats summarizes requests since listen started. Local replays don't count.
type Stats struct {
	Count int
	// OK and Failed count forwarded requests: 2xx, and anything else.
	OK, Failed int
	// P50, P95 and Max cover the newest maxLatencySamples forwarded requests
	// that got a response or timed out.
	P50, P95, Max time.Duration
	// Outcomes counts forwarded requests by how they ended.
	Outcomes map[Outcome]int
	// PerMinute counts requests in each of the StatsMinutes minutes up to
	// Minute, oldest first.
	PerMinute [StatsMinutes]int
	Minute    time.Time
	// Last is the newest request without its headers and bodies; its Number
	// is 0 before the first.
	Last Entry
}

// Outcome is how a forwarded request ended: its status, or the transport
// failure that left it without one.
type Outcome struct {
	Status  int
	Failure proxy.TransportErrorKind
}

type stats struct {
	count, ok, failed int
	outcomes          map[Outcome]int
	// latencies is a ring of the newest samples; next is where the next goes.
	latencies []time.Duration
	next      int
	// minutes counts requests per minute, oldest first, ending at minute.
	minutes [StatsMinutes]int
	minute  time.Time
	last    Entry
}

func (s *stats) add(entry Entry) {
	s.count++
	// The last entry can outlive its place in history, so it holds no headers
	// or bodies.
	s.last = entry
	s.last.Delivery.Headers, s.last.Delivery.Body = nil, nil
	s.last.Response.Headers, s.last.Response.Body = nil, nil
	s.shift(entry.Received)
	if back := int(s.minute.Sub(entry.Received.Truncate(time.Minute)) / time.Minute); back < StatsMinutes {
		s.minutes[StatsMinutes-1-back]++
	}
	if !entry.forwarded() {
		return
	}
	if entry.Failed() {
		s.failed++
	} else {
		s.ok++
	}
	outcome := Outcome{Status: entry.Response.Status}
	if entry.Failure != nil {
		outcome = Outcome{Failure: entry.Failure.Kind}
	}
	if s.outcomes == nil {
		s.outcomes = map[Outcome]int{}
	}
	s.outcomes[outcome]++
	if !entry.Timed() {
		return
	}
	if len(s.latencies) < maxLatencySamples {
		s.latencies = append(s.latencies, entry.Latency)
	} else {
		s.latencies[s.next] = entry.Latency
	}
	s.next = (s.next + 1) % maxLatencySamples
}

func (s *stats) shift(t time.Time) {
	minute := t.Truncate(time.Minute)
	if !minute.After(s.minute) {
		return
	}
	if gone := int(minute.Sub(s.minute) / time.Minute); gone < StatsMinutes {
		copy(s.minutes[:], s.minutes[gone:])
		clear(s.minutes[StatsMinutes-gone:])
	} else {
		clear(s.minutes[:])
	}
	s.minute = minute
}

func (s *stats) snapshot(now time.Time) Stats {
	s.shift(now)
	snapshot := Stats{Count: s.count, OK: s.ok, Failed: s.failed, Outcomes: maps.Clone(s.outcomes), PerMinute: s.minutes, Minute: s.minute, Last: s.last}
	if len(s.latencies) > 0 {
		sorted := slices.Sorted(slices.Values(s.latencies))
		snapshot.P50 = percentile(sorted, 50)
		snapshot.P95 = percentile(sorted, 95)
		snapshot.Max = sorted[len(sorted)-1]
	}
	return snapshot
}

// PerMinuteAt is PerMinute moved on to end at now's minute, for a snapshot
// taken earlier.
func (s Stats) PerMinuteAt(now time.Time) [StatsMinutes]int {
	window := stats{minutes: s.PerMinute, minute: s.Minute}
	window.shift(now)
	return window.minutes
}

// percentile is the nearest-rank percentile p of sorted samples.
func percentile(sorted []time.Duration, p int) time.Duration {
	return sorted[(len(sorted)*p+99)/100-1]
}
