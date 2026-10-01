// Package tui runs listen's terminal screens on bubbletea.
package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"hookspot/internal/cards"
)

// ErrClosed is returned for output sent after the program exited.
var ErrClosed = errors.New("terminal output closed")

// Program runs one listen run's model and owns its shutdown: the first Ctrl-C
// stops listening, the program quits once listen has, and a second Ctrl-C
// kills it.
type Program struct {
	input   io.Reader
	output  io.Writer
	profile colorprofile.Profile
	stop    context.CancelFunc
	exit    func(int)

	program  *tea.Program
	started  chan struct{}
	finished chan struct{}
	// stopping is set by the first Ctrl-C or Stop; only the event loop uses it.
	stopping bool
}

// NewProgram draws on output and reads keys from input, which is nil when
// stdin isn't a terminal. stop cancels listening.
func NewProgram(input io.Reader, output io.Writer, stop context.CancelFunc) *Program {
	return &Program{
		input:    input,
		output:   output,
		profile:  colorprofile.Detect(output, os.Environ()),
		stop:     stop,
		exit:     os.Exit,
		started:  make(chan struct{}),
		finished: make(chan struct{}),
	}
}

// Run runs model until it quits. However it ends, listening stops before
// output fails with ErrClosed, so a delivery cut short ends with the cancelled
// context rather than an error. A second Ctrl-C's kill isn't an error; a
// panic is.
func (p *Program) Run(model tea.Model) (tea.Model, error) {
	p.program = tea.NewProgram(model,
		// main.go stays the only signal handler.
		tea.WithoutSignalHandler(),
		tea.WithInput(p.input),
		tea.WithOutput(p.output),
		tea.WithColorProfile(p.profile),
		// A terminal's own size replaces this; it's the width rule's fallback.
		tea.WithWindowSize(cards.DefaultWidth, 24),
		tea.WithFilter(p.filter),
	)
	close(p.started)
	final, err := p.program.Run()
	p.stop()
	close(p.finished)
	// Kill returns ErrProgramKilled bare; wrapped, it carries a panic or an
	// input failure.
	if err == tea.ErrProgramKilled {
		err = nil
	}
	return final, err
}

// Println prints text above the view, in order with Send. tea's Println blocks
// once the program stops reading, so this returns ErrClosed as soon as Run has
// returned.
func (p *Program) Println(text string) error {
	<-p.started
	// tea's Println erases right after each line, which in a terminal clears
	// the last column of a line that fills whole rows, and it counts such a
	// line a row longer. A space after the line makes both right.
	width := cards.Width(p.output)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > 0 && w%width == 0 {
			lines[i] += " "
		}
	}
	// Printed lines bypass the renderer, which downsamples only the view.
	var styled strings.Builder
	_, _ = (&colorprofile.Writer{Forward: &styled, Profile: p.profile}).WriteString(strings.Join(lines, "\n"))
	printed := make(chan struct{})
	go func() {
		p.program.Println(styled.String())
		close(printed)
	}()
	select {
	case <-printed:
		return nil
	case <-p.finished:
		return ErrClosed
	}
}

// Send hands msg to the model; once the program has exited it does nothing.
func (p *Program) Send(msg tea.Msg) {
	<-p.started
	p.program.Send(msg)
}

// Quit ends the program once listening has stopped.
func (p *Program) Quit() {
	<-p.started
	p.program.Send(stoppedMsg{})
	p.program.Quit()
}

// Stop is a Cmd that stops listening as the first Ctrl-C does.
func Stop() tea.Msg {
	return stopMsg{}
}

type (
	stopMsg struct{}
	// stoppingMsg tells the model listening is stopping.
	stoppingMsg struct{}
	// stoppedMsg tells the model listening has stopped, before it quits.
	stoppedMsg struct{}
)

// filter runs Ctrl-C ahead of the model; in raw mode it's a key, not a
// SIGINT. The first press, like Stop, stops listening; a second kills the
// program and exits 130.
func (p *Program) filter(_ tea.Model, msg tea.Msg) tea.Msg {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() != "ctrl+c" {
			return msg
		}
		if p.stopping {
			p.program.Kill()
			p.exit(130)
			return nil
		}
	case stopMsg:
		if p.stopping {
			return nil
		}
	default:
		return msg
	}
	p.stopping = true
	p.stop()
	return stoppingMsg{}
}
