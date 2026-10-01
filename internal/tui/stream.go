package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"hookspot/internal/cards"
	"hookspot/internal/session"
)

// Replayer replays requests by number, as *session.Session does.
type Replayer interface {
	Replay(n int) error
	ReplayLast() error
}

// Exporter copies requests as cURL and exports them as fixtures, as
// *session.Session does.
type Exporter interface {
	Curl(n int, redact bool) (session.Curl, error)
	ExportFixture(n int, redact bool) (session.Fixture, error)
}

// Stream is listen's terminal stream: cards scroll above a status line and,
// when stdin is a terminal, the › prompt for request commands.
type Stream struct {
	Replayer Replayer
	Exporter Exporter
	Project  string
	// Forwarding is set with --forward-to; without it nothing replays.
	Forwarding bool
	// Prompt is set when stdin is a terminal.
	Prompt bool
	// ShowSensitiveHeaders keeps sensitive header values in shown commands
	// and fixtures.
	ShowSensitiveHeaders bool

	width  int
	state  cards.State
	lost   error
	totals session.Stats
	input  string
	reply  string
}

func (m Stream) Init() tea.Cmd { return nil }

func (m Stream) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case session.Ready, session.Reconnected:
		m = m.connection(cards.StateLive, nil)
	case session.ConnectionLost:
		m = m.connection(cards.StateOffline, msg.Err)
	case session.Recorded:
		m.totals = msg.Totals
	case stoppingMsg:
		m.state, m.reply = cards.StateStopping, ""
	case stoppedMsg:
		m.state, m.reply = cards.StateStopped, ""
	case replyMsg:
		m.reply = string(msg)
	case copiedMsg:
		m.reply = msg.notes
		return m, tea.Batch(tea.SetClipboard(msg.command), tea.Println(msg.shown))
	case tea.KeyPressMsg:
		if m.prompting() {
			return m.key(msg)
		}
	}
	return m, nil
}

func (m Stream) View() tea.View {
	var lines []string
	if m.reply != "" {
		lines = strings.Split(m.reply, "\n")
	}
	lines = append(lines, cards.Status{State: m.state, Err: m.lost, Project: m.Project, Totals: m.totals, Hints: m.statusHints()}.Line(m.width))
	if m.prompting() {
		lines = append(lines, cards.Prompt(m.input, append(m.commands(), "ctrl-c quit"), m.width))
	}
	// The renderer erases the frame's last line on exit, so it's left empty.
	return tea.NewView(strings.Join(lines, "\n") + "\n")
}

// replyMsg answers a command; it shows above the status line until the next.
type replyMsg string

// copiedMsg carries a request's cURL command: the full one for the clipboard,
// the shown one to print above the stream.
type copiedMsg struct {
	notes, command, shown string
}

// connection follows the connection until listening stops.
func (m Stream) connection(state cards.State, lost error) Stream {
	if m.state < cards.StateStopping {
		m.state, m.lost = state, lost
	}
	return m
}

func (m Stream) prompting() bool {
	return m.Prompt && m.state < cards.StateStopping
}

// commands are the prompt's commands; without --forward-to nothing replays.
func (m Stream) commands() []string {
	if !m.Forwarding {
		return []string{"? help"}
	}
	return []string{"↵ replay last", "r N replay #N", "? help"}
}

// statusHints are the keys the status line names; the prompt names its own.
func (m Stream) statusHints() []string {
	switch {
	case m.state == cards.StateStopped, m.prompting():
		return nil
	case m.Prompt:
		return []string{"ctrl-c force quit"}
	}
	return []string{"ctrl-c quit"}
}

func (m Stream) key(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "enter":
		line := m.input
		m.input = ""
		return m.run(line)
	case "backspace":
		runes := []rune(m.input)
		m.input = string(runes[:max(0, len(runes)-1)])
	default:
		m.input += key.Text
	}
	return m, nil
}

// run runs one typed line: ↵ replays the last request, r N replays #N, ?
// shows help, and anything else gets the command list.
func (m Stream) run(line string) (tea.Model, tea.Cmd) {
	m.reply = ""
	fields := strings.Fields(line)
	switch {
	case len(fields) == 0:
		return m.replay(m.Replayer.ReplayLast)
	case len(fields) == 2 && fields[0] == "r":
		if n, err := strconv.Atoi(fields[1]); err == nil {
			replayer := m.Replayer
			return m.replay(func() error { return replayer.Replay(n) })
		}
	case len(fields) == 2 && fields[0] == "c":
		if n, err := strconv.Atoi(fields[1]); err == nil {
			return m, m.copyCurl(n)
		}
	case len(fields) == 2 && fields[0] == "e":
		if n, err := strconv.Atoi(fields[1]); err == nil {
			return m, m.exportFixture(n)
		}
	case len(fields) == 1 && fields[0] == "?":
		m.reply = m.help()
		return m, nil
	}
	m.reply = m.usage()
	return m, nil
}

// usage is the one-line help, naming every command.
func (m Stream) usage() string {
	commands := []string{"c N copy as cURL", "e N export fixture", "? help"}
	if m.Forwarding {
		commands = append([]string{"↵ replay last", "r N replay #N"}, commands...)
	}
	return "commands: " + strings.Join(commands, " · ")
}

// copyCurl builds request n's cURL command off the event loop. The full
// command goes to the clipboard; the shown one, redacted unless
// --show-sensitive-headers, prints above the stream.
func (m Stream) copyCurl(n int) tea.Cmd {
	exporter, redact := m.Exporter, !m.ShowSensitiveHeaders
	return func() tea.Msg {
		curl, err := exporter.Curl(n, redact)
		if err != nil {
			return replyMsg(cards.Line(err.Error()))
		}
		return copiedMsg{notes: cards.CurlNotes(n, curl, true), command: curl.Command, shown: cards.Sanitize(curl.Shown)}
	}
}

// exportFixture writes request n's fixture off the event loop.
func (m Stream) exportFixture(n int) tea.Cmd {
	exporter, redact := m.Exporter, !m.ShowSensitiveHeaders
	return func() tea.Msg {
		fixture, err := exporter.ExportFixture(n, redact)
		if err != nil {
			return replyMsg(cards.Line(err.Error()))
		}
		return replyMsg(cards.Exported(n, fixture))
	}
}

// replay runs off the event loop, which must never wait on the session; only
// a failure comes back, as the reply.
func (m Stream) replay(replay func() error) (tea.Model, tea.Cmd) {
	if !m.Forwarding {
		m.reply = session.ErrNoTarget.Error()
		return m, nil
	}
	return m, func() tea.Msg {
		if err := replay(); err != nil {
			return replyMsg(cards.Line(err.Error()))
		}
		return nil
	}
}

func (m Stream) help() string {
	requests := "c N     copy request #N as cURL\ne N     export request #N as a fixture\n"
	if !m.Forwarding {
		return requests + "replays need --forward-to\nctrl-c  stop listening"
	}
	return "↵       replay the last request\nr N     replay request #N\n" + requests + "ctrl-c  stop listening"
}

// StreamSink prints each request's card, the reconnect notice and the root
// hint above the stream, and keeps its status line current. Source warnings
// print before the program starts.
func (p *Program) StreamSink(l cards.Listen, requestsURL string) session.Sink {
	return streamSink{program: p, listen: l, requestsURL: requestsURL}
}

type streamSink struct {
	program     *Program
	listen      cards.Listen
	requestsURL string
}

func (s streamSink) Emit(event session.Event) error {
	var text string
	switch e := event.(type) {
	case session.Recorded:
		text = s.listen.Entry(e.Entry, cards.Width(s.program.output))
	case session.Reconnected:
		text = cards.Reconnected(e.Offline, s.requestsURL)
	case session.RootNotFound:
		text = cards.RootNotFound(e.Root, e.Status)
	}
	if text != "" {
		if err := s.program.Println(text); err != nil {
			return err
		}
	}
	s.program.Send(event)
	return nil
}
