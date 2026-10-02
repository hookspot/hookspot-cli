package tui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"hookspot/internal/cards"
	"hookspot/internal/session"
)

// Requests is what listen's commands ask of the session, as
// *session.Session answers them.
type Requests interface {
	Replay(n int) error
	ReplayLast() error
	Curl(n int, redact bool) (session.Curl, error)
	ExportFixture(n int, redact bool) (session.Fixture, error)
	SendTest(source string) (string, error)
}

// Stream is listen's terminal stream: cards scroll above a status line and,
// when stdin is a terminal, the › prompt for request commands.
type Stream struct {
	Requests Requests
	// Println prints above the stream, as Program.Println does.
	Println func(string) error
	Project string
	// Forwarding is set with --forward-to; without it nothing replays.
	Forwarding bool
	// Prompt is set when stdin is a terminal.
	Prompt bool
	// ShowSensitiveHeaders keeps sensitive header values in shown commands
	// and fixtures.
	ShowSensitiveHeaders bool

	connection
	width  int
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
		m.connection = m.follow(cards.StateLive, nil)
	case session.ConnectionLost:
		m.connection = m.follow(cards.StateOffline, msg.Err)
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
		return m, tea.Batch(tea.SetClipboard(msg.command), func() tea.Msg {
			_ = m.Println(msg.shown)
			return nil
		})
	case tea.PasteMsg:
		if m.prompting() {
			m.input += pasted(msg.Content)
		}
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
		// The renderer cuts lines at the terminal's width, and replies name
		// paths and warnings that must show whole.
		lines = wrap(m.width, strings.Split(m.reply, "\n"))
	}
	lines = append(lines, cards.Status{State: m.state, Err: m.lost, Project: m.Project, Totals: m.totals, Hints: m.statusHints()}.Line(m.width))
	if m.prompting() {
		lines = append(lines, cards.Prompt(m.input, append(m.commands(), QuitHint), m.width))
	}
	// The renderer erases the frame's last line on exit, so the final frame
	// ends with an empty one. Live frames don't: on the screen's last row the
	// renderer leaves that line undrawn, and every print lands a row too high.
	if m.state == cards.StateStopped {
		lines = append(lines, "")
	}
	return tea.NewView(strings.Join(lines, "\n"))
}

// replyMsg answers a command; it shows above the status line until the next.
type replyMsg string

// copiedMsg carries a request's cURL command: the full one for the clipboard,
// the shown one to print above the stream.
type copiedMsg struct {
	notes, command, shown string
}

// connection is the listen connection's state, as the status line shows it.
type connection struct {
	state cards.State
	// lost is why the connection dropped, while offline.
	lost error
}

// follow takes the connection's new state until listening stops.
func (c connection) follow(state cards.State, lost error) connection {
	if c.state < cards.StateStopping {
		c.state, c.lost = state, lost
	}
	return c
}

// pasted is pasted text as typed: line breaks and other control characters
// become spaces.
func pasted(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
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
	return []string{QuitHint}
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

// run runs one typed line; ? shows help, and anything that isn't a command
// gets the command list.
func (m Stream) run(line string) (tea.Model, tea.Cmd) {
	m.reply = ""
	command, ok := ParseCommand(line)
	if !ok {
		m.reply = CommandUsage(m.Forwarding)
		return m, nil
	}
	requests := m.Requests
	switch command.Key {
	case "":
		return m.replay(requests.ReplayLast)
	case "r":
		return m.replay(func() error { return requests.Replay(command.N) })
	case "c":
		return m, copyCurl(requests, command.N, !m.ShowSensitiveHeaders)
	case "e":
		return m, exportFixture(requests, command.N, !m.ShowSensitiveHeaders)
	case "t":
		return m, sendTest(requests, command.Source)
	}
	m.reply = CommandHelp(m.Forwarding)
	return m, nil
}

// copyCurl builds request n's cURL command off the event loop: the full one
// for the clipboard, and the one to show, redacted when redact is set.
func copyCurl(requests Requests, n int, redact bool) tea.Cmd {
	return func() tea.Msg {
		curl, err := requests.Curl(n, redact)
		if err != nil {
			return replyMsg(cards.Line(err.Error()))
		}
		return copiedMsg{notes: cards.CurlNotes(n, curl, true), command: curl.Command, shown: cards.Sanitize(curl.Shown)}
	}
}

// exportFixture writes request n's fixture off the event loop.
func exportFixture(requests Requests, n int, redact bool) tea.Cmd {
	return func() tea.Msg {
		fixture, err := requests.ExportFixture(n, redact)
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
func sendTest(requests Requests, source string) tea.Cmd {
	return func() tea.Msg {
		name, err := requests.SendTest(source)
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
		// Println pads a line that fills the terminal onto another row, so
		// cards leave its last column free.
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
