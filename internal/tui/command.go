package tui

import (
	"strconv"
	"strings"
)

// Command is a line typed into the stream: at the › prompt, or into a plain
// stream's terminal.
type Command struct {
	// Key is "" for an empty line, which replays the last request, or one of
	// "r", "c", "e", "t" and "?".
	Key string
	// N is the request r, c and e act on.
	N int
	// Source is the source t tests; its name may contain spaces.
	Source string
}

// ParseCommand reads a typed line; ok is false when it isn't a command.
func ParseCommand(line string) (command Command, ok bool) {
	fields := strings.Fields(line)
	switch {
	case len(fields) == 0:
		return Command{}, true
	case fields[0] == "t":
		return Command{Key: "t", Source: strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "t"))}, true
	case len(fields) == 1 && fields[0] == "?":
		return Command{Key: "?"}, true
	case len(fields) == 2 && (fields[0] == "r" || fields[0] == "c" || fields[0] == "e"):
		n, err := strconv.Atoi(fields[1])
		return Command{Key: fields[0], N: n}, err == nil
	}
	return Command{}, false
}

// CommandHints name the commands; without --forward-to nothing replays.
func CommandHints(forwarding bool) []string {
	hints := []string{"c N cURL", "e N fixture", "t test event"}
	if forwarding {
		return append([]string{"↵ replay last", "r N replay #N"}, hints...)
	}
	return hints
}

// CommandUsage answers a line that isn't a command.
func CommandUsage(forwarding bool) string {
	return "commands: " + strings.Join(append(CommandHints(forwarding), "? help"), " · ")
}

// CommandHelp spells out every command.
func CommandHelp(forwarding bool) string {
	requests := "c N     copy request #N as cURL\ne N     export request #N as a fixture\nt NAME  send a test event to source NAME\n"
	if !forwarding {
		return requests + "replays need --forward-to\nctrl-c  stop listening"
	}
	return "↵       replay the last request\nr N     replay request #N\n" + requests + "ctrl-c  stop listening"
}
