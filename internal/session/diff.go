package session

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"hookspot/internal/proxy"
)

const (
	maxDiffLines = 6
	// maxDiffLineBytes caps each kept line, so a comparison never holds on to
	// a whole one-line body.
	maxDiffLineBytes = 512
)

// Comparison sets a replay beside the entry it replays.
type Comparison struct {
	// Original is the replayed entry's number. Status (0 after a transport
	// failure), Failure, Latency and Size (its response body's length) are
	// what forwarding it got.
	Original int
	Status   int
	Failure  *proxy.TransportFailure
	Latency  time.Duration
	Size     int
	// Removed and Added are the first response body lines only the original
	// or only the replay has, at most six together; More counts the rest.
	// Bodies compare only when both requests got a response.
	Removed, Added []string
	More           int
	// Binary is set instead when the bodies differ and either isn't text, so
	// only their sizes compare.
	Binary bool
}

// Compare sets replay beside original: what forwarding each got, and the
// first lines that differ between their response bodies.
func Compare(original, replay Entry) Comparison {
	c := Comparison{
		Original: original.Number,
		Status:   original.Response.Status,
		Failure:  original.Failure,
		Latency:  original.Latency,
		Size:     len(original.Response.Body),
	}
	before, after := original.Response.Body, replay.Response.Body
	if original.Failure != nil || replay.Failure != nil || bytes.Equal(before, after) {
		return c
	}
	if !utf8.Valid(before) || !utf8.Valid(after) {
		c.Binary = true
		return c
	}

	removed, added := bodyLines(before), bodyLines(after)
	for len(removed) > 0 && len(added) > 0 && removed[0] == added[0] {
		removed, added = removed[1:], added[1:]
	}
	for len(removed) > 0 && len(added) > 0 && removed[len(removed)-1] == added[len(added)-1] {
		removed, added = removed[:len(removed)-1], added[:len(added)-1]
	}
	// Either side keeps at least half the lines when both have more.
	keepRemoved := min(len(removed), max(maxDiffLines/2, maxDiffLines-len(added)))
	keepAdded := min(len(added), maxDiffLines-keepRemoved)
	c.Removed, c.Added = clip(removed[:keepRemoved]), clip(added[:keepAdded])
	c.More = len(removed) + len(added) - keepRemoved - keepAdded
	return c
}

// bodyLines splits a text body as cards show it, indenting JSON.
func bodyLines(body []byte) []string {
	if len(body) == 0 {
		return nil
	}
	var indented bytes.Buffer
	if json.Indent(&indented, bytes.TrimSpace(body), "", "  ") == nil {
		body = indented.Bytes()
	}
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n"), "\n")
}

// clip copies lines, cut to maxDiffLineBytes, so they don't pin the body.
func clip(lines []string) []string {
	var clipped []string
	for _, line := range lines {
		if len(line) > maxDiffLineBytes {
			line = strings.ToValidUTF8(line[:maxDiffLineBytes], "")
		}
		clipped = append(clipped, strings.Clone(line))
	}
	return clipped
}
