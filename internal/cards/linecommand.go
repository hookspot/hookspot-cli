package cards

import (
	"slices"
	"strconv"
	"strings"
)

// LineCommand is a line typed into listen's stream: ↵ replays the last
// request, r N replays #N, c N copies #N as cURL, e N exports it as a
// fixture, t [source] sends a test event, and ? asks for help.
type LineCommand struct {
	// Op is "↵", "r", "c", "e", "t" or "?"; "" for any other line.
	Op string
	// N is r, c and e's request number.
	N int
	// Source is the source t tests, "" for the only one.
	Source string
}

// ParseLineCommand reads a typed line.
func ParseLineCommand(line string) LineCommand {
	fields := strings.Fields(line)
	switch {
	case len(fields) == 0:
		return LineCommand{Op: "↵"}
	case fields[0] == "t":
		return LineCommand{Op: "t", Source: strings.Join(fields[1:], " ")}
	case len(fields) == 1 && fields[0] == "?":
		return LineCommand{Op: "?"}
	case len(fields) == 2 && slices.Contains([]string{"r", "c", "e"}, fields[0]):
		if n, err := strconv.Atoi(fields[1]); err == nil {
			return LineCommand{Op: fields[0], N: n}
		}
	}
	return LineCommand{}
}

// LineCommandHints name the line commands; without --forward-to nothing
// replays.
func LineCommandHints(forwarding bool) []string {
	hints := []string{"c N copy as cURL", "e N export fixture", "t test event"}
	if forwarding {
		hints = append([]string{"↵ replay last", "r N replay #N"}, hints...)
	}
	return hints
}
