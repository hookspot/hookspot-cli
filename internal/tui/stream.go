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

// Stream is listen's terminal stream: cards scroll above a status line and,
// when stdin is a terminal, the › prompt for request commands.
type Stream struct {
	Replayer Replayer
	Project  string
	// Forwarding is set with --forward-to; without it nothing replays.
	Forwarding bool
	// Prompt is set when stdin is a terminal.
	Prompt bool

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
	case len(fields) == 1 && fields[0] == "?":
		m.reply = m.help()
		return m, nil
	}
	m.reply = "commands: " + strings.Join(m.commands(), " · ")
	return m, nil
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
	if !m.Forwarding {
		return "replays need --forward-to\nctrl-c  stop listening"
	}
	return "↵       replay the last request\nr N     replay request #N\nctrl-c  stop listening"
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
