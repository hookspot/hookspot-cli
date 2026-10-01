package session

import (
	"slices"
	"time"

	"hookspot/internal/proxy"
)

const (
	statsMinutes      = 15
	maxLatencySamples = 1000
)

// Stats summarizes requests since listen started. Local replays don't count.
type Stats struct {
	Count int
	// OK and Failed count forwarded requests: 2xx, and anything else.
	OK, Failed int
	// P50, P95 and Max cover the newest 1000 forwarded requests that got a
	// response or timed out.
	P50, P95, Max time.Duration
	// PerMinute counts requests in each of the last 15 minutes, oldest first.
	PerMinute [statsMinutes]int
	// Last is the newest request; its Number is 0 before the first.
	Last Entry
}

type stats struct {
	count, ok, failed int
	// latencies is a ring of the newest samples; next is where the next goes.
	latencies []time.Duration
	next      int
	// minutes ends at the minute minute.
	minutes [statsMinutes]int
	minute  time.Time
	last    Entry
}

func (s *stats) add(entry Entry) {
	s.count++
	s.last = entry
	s.shift(entry.Received)
	if back := int(s.minute.Sub(entry.Received.Truncate(time.Minute)) / time.Minute); back < statsMinutes {
		s.minutes[statsMinutes-1-back]++
	}
	if !entry.forwarded() {
		return
	}
	if entry.Failure == nil && entry.Response.Status >= 200 && entry.Response.Status < 300 {
		s.ok++
	} else {
		s.failed++
	}
	// Other transport failures end before the target answers, so their time says nothing about it.
	if entry.Failure != nil && entry.Failure.Kind != proxy.TransportTimeout {
		return
	}
	if len(s.latencies) < maxLatencySamples {
		s.latencies = append(s.latencies, entry.Latency)
	} else {
		s.latencies[s.next] = entry.Latency
	}
	s.next = (s.next + 1) % maxLatencySamples
}

// shift moves the per-minute window forward to end at t's minute.
func (s *stats) shift(t time.Time) {
	minute := t.Truncate(time.Minute)
	if !minute.After(s.minute) {
		return
	}
	if gone := int(minute.Sub(s.minute) / time.Minute); gone < statsMinutes {
		copy(s.minutes[:], s.minutes[gone:])
		clear(s.minutes[statsMinutes-gone:])
	} else {
		clear(s.minutes[:])
	}
	s.minute = minute
}

func (s *stats) snapshot(now time.Time) Stats {
	s.shift(now)
	snapshot := Stats{Count: s.count, OK: s.ok, Failed: s.failed, PerMinute: s.minutes, Last: s.last}
	if len(s.latencies) > 0 {
		sorted := slices.Sorted(slices.Values(s.latencies))
		snapshot.P50 = percentile(sorted, 50)
		snapshot.P95 = percentile(sorted, 95)
		snapshot.Max = sorted[len(sorted)-1]
	}
	return snapshot
}

// percentile is the nearest-rank percentile p of sorted samples.
func percentile(sorted []time.Duration, p int) time.Duration {
	return sorted[(len(sorted)*p+99)/100-1]
}
