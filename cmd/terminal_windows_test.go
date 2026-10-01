//go:build windows

package cmd

import (
	"os/exec"
	"testing"

	"github.com/charmbracelet/x/xpty"
)

// terminalMode is empty: ConPTY doesn't expose the console mode the command
// sets.
type terminalMode struct{}

func readTerminalMode(*testing.T, xpty.Pty) terminalMode { return terminalMode{} }

// controlTerminal does nothing: ConPTY attaches its console to the command.
func controlTerminal(*exec.Cmd, int) {}
