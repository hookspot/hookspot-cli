package tui

import (
	"cmp"
	"net"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"hookspot/internal/cards"
	"hookspot/internal/session"
)

const dialTimeout = time.Second

// wait is w's: it dials the target until it answers, then replays request
// number. id tells its messages from an earlier wait's.
type wait struct {
	id, number int
	// replaying is set once the target answered; result is the replay's
	// outcome.
	replaying bool
	result    string
}

type (
	// dialedMsg is a dial of wait id's target; err is nil when it answered.
	dialedMsg struct {
		id  int
		err error
	}
	// waitReplayedMsg ends wait id with its replay's error.
	waitReplayedMsg struct {
		id  int
		err error
	}
)

func (m Fullscreen) waiting() bool {
	return m.wait.number != 0 && m.wait.result == ""
}

// startWait waits on the selected request when it found nothing listening,
// pausing on it so its detail shows the wait.
func (m Fullscreen) startWait(i int) (Fullscreen, tea.Cmd) {
	switch {
	case !m.forwarding():
		return m.show(session.ErrNoTarget.Error())
	case i < 0:
		return m, nil
	case m.entries[i].Failure == nil:
		return m.show("#" + strconv.Itoa(m.entries[i].Number) + " got a response; r replays it now")
	}
	m.paused, m.selected = true, m.entries[i].Number
	m.wait = wait{id: m.wait.id + 1, number: m.selected}
	return m, dial(m.targetAddress(), 0, m.wait.id)
}

// dial tries address after a pause, off the event loop.
func dial(address string, after time.Duration, id int) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(after)
		conn, err := net.DialTimeout("tcp", address, dialTimeout)
		if err == nil {
			_ = conn.Close()
		}
		return dialedMsg{id: id, err: err}
	}
}

// dialed replays once the target answers, else dials again after dialEvery.
func (m Fullscreen) dialed(msg dialedMsg) (Fullscreen, tea.Cmd) {
	if msg.id != m.wait.id || !m.waiting() {
		return m, nil
	}
	if msg.err != nil {
		return m, dial(m.targetAddress(), m.dialInterval(), msg.id)
	}
	m.wait.replaying = true
	requests, n := m.Requests, m.wait.number
	return m, func() tea.Msg { return waitReplayedMsg{id: msg.id, err: requests.Replay(n)} }
}

// waitReplayed puts the replay's outcome in place of the waiting line. The
// replay's entry was recorded before Replay returned.
func (m Fullscreen) waitReplayed(msg waitReplayedMsg) Fullscreen {
	if msg.id != m.wait.id || !m.waiting() {
		return m
	}
	if msg.err != nil {
		m.wait.result = errorStyle.Render("✗") + " " + cards.Line(msg.err.Error())
		return m
	}
	m.wait.result = markStyle.Render("↻") + " replayed"
	for i := len(m.entries) - 1; i >= 0; i-- {
		if e := m.entries[i]; e.ReplayOf == m.wait.number {
			m.wait.result += " as #" + strconv.Itoa(e.Number) + " " + outcomeWithLatency(e)
			break
		}
	}
	return m
}

// waitLine is w's progress: waiting, replaying, then the replay's outcome.
func (m Fullscreen) waitLine() string {
	address := m.targetAddress()
	switch {
	case m.wait.result != "":
		return m.wait.result
	case m.wait.replaying:
		return markStyle.Render("↻") + " " + address + " answered, replaying #" + strconv.Itoa(m.wait.number) + "…"
	}
	return markStyle.Render("○") + " waiting for " + address + faintStyle.Render(" · checking every "+m.dialInterval().String()+" · esc stops")
}

func (m Fullscreen) dialInterval() time.Duration {
	return cmp.Or(m.dialEvery, time.Second)
}
