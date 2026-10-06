package session

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"hookspot/internal/ws"
)

func TestReplayComparesWithItsOriginal(t *testing.T) {
	s, _, sink := newTestSession(
		reply{status: http.StatusUnprocessableEntity, body: `{"error":"missing customer_id"}`, latency: 9 * time.Millisecond},
		reply{status: http.StatusOK, body: `{"received":true}`, latency: 41 * time.Millisecond},
	)
	if _, err := s.Handle(delivery("rte_orders", "/orders")); err != nil {
		t.Fatal(err)
	}
	if err := s.Replay(1); err != nil {
		t.Fatal(err)
	}

	recorded := sink.recorded()
	if recorded[0].Entry.Replay != nil {
		t.Fatalf("delivery comparison = %#v, want none", recorded[0].Entry.Replay)
	}
	want := &Comparison{
		Original: 1,
		Status:   http.StatusUnprocessableEntity,
		Latency:  9 * time.Millisecond,
		Size:     len(`{"error":"missing customer_id"}`),
		Removed:  []string{`  "error": "missing customer_id"`},
		Added:    []string{`  "received": true`},
	}
	if got := recorded[1].Entry.Replay; !reflect.DeepEqual(got, want) {
		t.Fatalf("replay comparison = %#v, want %#v", got, want)
	}
}

func TestCompareBodies(t *testing.T) {
	lines := func(prefix string) string {
		var lines []string
		for i := range 10 {
			lines = append(lines, prefix+strconv.Itoa(i))
		}
		return strings.Join(lines, "\n")
	}
	tests := []struct {
		name           string
		before, after  string
		removed, added []string
		more           int
		binary         bool
	}{
		{name: "JSON compares indented", before: `{"a":1,"b":2}`, after: "{\n    \"a\": 1,\n\"b\": 3}\n", removed: []string{`  "b": 2`}, added: []string{`  "b": 3`}},
		{name: "JSON spacing alone is no change", before: `{"a":1}`, after: `{ "a": 1 }`},
		{name: "line endings", before: "a\r\nb\r\n", after: "a\nc", removed: []string{"b"}, added: []string{"c"}},
		{name: "empty body", after: "one\ntwo", added: []string{"one", "two"}},
		{name: "few removed lines leave room for added", before: "x", after: lines("+"), removed: []string{"x"}, added: []string{"+0", "+1", "+2", "+3", "+4"}, more: 5},
		{name: "few added lines leave room for removed", before: lines("-"), after: "x", removed: []string{"-0", "-1", "-2", "-3", "-4"}, added: []string{"x"}, more: 5},
		{name: "same binary", before: "\x89PNG\xff", after: "\x89PNG\xff"},
		{name: "binary against text", before: "text", after: "\x89PNG\xff", binary: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := Compare(Entry{Response: ws.Response{Body: []byte(test.before)}}, Entry{Response: ws.Response{Body: []byte(test.after)}})
			if !reflect.DeepEqual(c.Removed, test.removed) || !reflect.DeepEqual(c.Added, test.added) || c.More != test.more || c.Binary != test.binary {
				t.Fatalf("Compare = removed %q, added %q, %d more, binary %v", c.Removed, c.Added, c.More, c.Binary)
			}
		})
	}
}

func TestCompareClipsLongLines(t *testing.T) {
	// The cut lands inside a two-byte rune.
	long := "x" + strings.Repeat("é", maxDiffLineBytes)
	c := Compare(Entry{Response: ws.Response{Body: []byte("short")}}, Entry{Response: ws.Response{Body: []byte(long)}})
	if added := c.Added[0]; len(added) != maxDiffLineBytes-1 || !utf8.ValidString(added) || !strings.HasPrefix(long, added) {
		t.Fatalf("clipped line = %d bytes, valid UTF-8 %v", len(added), utf8.ValidString(added))
	}
}
