package tui

import (
	"strconv"
	"strings"
)

// Command is a line typed into the stream: at the › prompt, or into a plain
// stream's terminal.
type Command struct {
	Key CommandKey
	// N is the request r, c and e act on.
	N int
	// Source is the source t tests; its name may contain spaces.
	Source string
}

// CommandKey names a command by the key it's typed with.
type CommandKey string

const (
	// ReplayLastKey is an empty line.
	ReplayLastKey CommandKey = ""
	ReplayKey     CommandKey = "r"
	CurlKey       CommandKey = "c"
	ExportKey     CommandKey = "e"
	TestKey       CommandKey = "t"
	HelpKey       CommandKey = "?"
)

// ParseCommand reads a typed line; ok is false when it isn't a command.
func ParseCommand(line string) (command Command, ok bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return Command{Key: ReplayLastKey}, true
	}
	key := CommandKey(fields[0])
	switch {
	case key == TestKey:
		return Command{Key: TestKey, Source: strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), string(TestKey)))}, true
	case len(fields) == 1 && key == HelpKey:
		return Command{Key: HelpKey}, true
	case len(fields) == 2 && (key == ReplayKey || key == CurlKey || key == ExportKey):
		n, err := strconv.Atoi(fields[1])
		return Command{Key: key, N: n}, err == nil
	}
	return Command{}, false
}

// QuitHint names the key that stops listening.
const QuitHint = "ctrl-c quit"

var commandHints = map[CommandKey]string{
	ReplayLastKey: "↵ replay last",
	ReplayKey:     "r N replay #N",
	CurlKey:       "c N cURL",
	ExportKey:     "e N fixture",
	TestKey:       "t test event",
	HelpKey:       "? help",
}

// hints name commands in order; without --forward-to nothing replays.
func hints(forwarding bool, commands ...CommandKey) []string {
	var named []string
	for _, command := range commands {
		if forwarding || (command != ReplayLastKey && command != ReplayKey) {
			named = append(named, commandHints[command])
		}
	}
	return named
}

// CommandHints name the commands; without --forward-to nothing replays.
func CommandHints(forwarding bool) []string {
	return hints(forwarding, ReplayLastKey, ReplayKey, CurlKey, ExportKey, TestKey)
}

// CommandUsage answers a line that isn't a command.
func CommandUsage(forwarding bool) string {
	return "commands: " + strings.Join(append(CommandHints(forwarding), commandHints[HelpKey]), " · ")
}

// CommandHelp spells out every command.
func CommandHelp(forwarding bool) string {
	requests := "c N     copy request #N as cURL\ne N     export request #N as a fixture\nt NAME  send a test event to source NAME\n"
	if !forwarding {
		return requests + "replays need --forward-to\nctrl-c  stop listening"
	}
	return "↵       replay the last request\nr N     replay request #N\n" + requests + "ctrl-c  stop listening"
}
