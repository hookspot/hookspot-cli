package tui

import (
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

// Tester sends test events, as *session.Session does.
type Tester interface {
	SendTest(source string) (string, error)
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
	// Tester sends the t command's test events.
	Tester Tester

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
		return []string{"t test event", "? help"}
	}
	return []string{"↵ replay last", "r N replay #N", "t test event", "? help"}
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

// run runs one typed line: ↵ replays the last request, r N replays #N, c N
// copies #N as cURL, e N exports it as a fixture, t [source] sends a test
// event, ? shows help, and anything else gets the command list.
func (m Stream) run(line string) (tea.Model, tea.Cmd) {
	m.reply = ""
	command, ok := ParseCommand(line)
	if !ok {
		m.reply = CommandUsage(m.Forwarding)
		return m, nil
	}
	switch command.Key {
	case "":
		return m.replay(m.Replayer.ReplayLast)
	case "r":
		replayer := m.Replayer
		return m.replay(func() error { return replayer.Replay(command.N) })
	case "c":
		return m, copyCurl(m.Exporter, command.N, !m.ShowSensitiveHeaders)
	case "e":
		return m, exportFixture(m.Exporter, command.N, !m.ShowSensitiveHeaders)
	case "t":
		return m, sendTest(m.Tester, command.Source)
	}
	m.reply = CommandHelp(m.Forwarding)
	return m, nil
}

// copyCurl builds request n's cURL command off the event loop: the full one
// for the clipboard, and the one to show, redacted when redact is set.
func copyCurl(exporter Exporter, n int, redact bool) tea.Cmd {
	return func() tea.Msg {
		curl, err := exporter.Curl(n, redact)
		if err != nil {
			return replyMsg(cards.Line(err.Error()))
		}
		return copiedMsg{notes: cards.CurlNotes(n, curl, true), command: curl.Command, shown: cards.Sanitize(curl.Shown)}
	}
}

// exportFixture writes request n's fixture off the event loop.
func exportFixture(exporter Exporter, n int, redact bool) tea.Cmd {
	return func() tea.Msg {
		fixture, err := exporter.ExportFixture(n, redact)
		if err != nil {
			return replyMsg(cards.Line(err.Error()))
		}
		return replyMsg(cards.Exported(n, fixture))
	}
}

// replay refuses without --forward-to, where nothing replays.
func (m Stream) replay(replay func() error) (tea.Model, tea.Cmd) {
	if !m.Forwarding {
		m.reply = session.ErrNoTarget.Error()
		return m, nil
	}
	return m, runReplay(replay)
}

// runReplay replays off the event loop, which must never wait on the
// session; only a failure comes back, as the reply.
func runReplay(replay func() error) tea.Cmd {
	return func() tea.Msg {
		if err := replay(); err != nil {
			return replyMsg(cards.Line(err.Error()))
		}
		return nil
	}
}

// sendTest sends a test event off the event loop; the reply says where it
// went or why it didn't.
func sendTest(tester Tester, source string) tea.Cmd {
	return func() tea.Msg {
		name, err := tester.SendTest(source)
		if err != nil {
			return replyMsg(cards.Line(err.Error()))
		}
		return replyMsg("test event sent to " + cards.Line(name))
	}
}

// StreamSink prints each request's card, the reconnect notice, the root hint
// and the test hint above the stream, and keeps its status line current.
// Source warnings print before the program starts.
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
		// tea's Println erases right after each line, which in a terminal
		// clears the last column of a line that fills it.
		text = s.listen.Entry(e.Entry, cards.Width(s.program.output)-1)
	case session.Reconnected:
		text = cards.Reconnected(e.Offline, s.requestsURL)
	case session.RootNotFound:
		text = cards.RootNotFound(e.Root, e.Status)
	case session.TestHint:
		text = cards.TestHint(e, s.program.input != nil)
	}
	if text != "" {
		if err := s.program.Println(text); err != nil {
			return err
		}
	}
	s.program.Send(event)
	return nil
}
