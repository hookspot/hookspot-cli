//go:build !windows

package cmd

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"

	"github.com/charmbracelet/x/termios"
	"github.com/charmbracelet/x/xpty"
	"golang.org/x/sys/unix"
)

// terminalMode is the terminal's termios.
type terminalMode = unix.Termios

// readTerminalMode reads the termios from the master side, which outlives
// the command.
func readTerminalMode(t *testing.T, pty xpty.Pty) terminalMode {
	t.Helper()
	var mode *unix.Termios
	var err error
	controlErr := pty.(*xpty.UnixPty).Control(func(fd uintptr) {
		mode, err = termios.GetTermios(int(fd))
	})
	if err := errors.Join(controlErr, err); err != nil {
		t.Fatal(err)
	}
	return *mode
}

// controlTerminal makes the terminal, the command's file descriptor fd, its
// controlling terminal, so a resize sends it SIGWINCH and Ctrl-C outside raw
// mode sends SIGINT.
func controlTerminal(command *exec.Cmd, fd int) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: fd}
}
