//go:build darwin

package cmd

import "golang.org/x/sys/unix"

const readTermios = unix.TIOCGETA
const writeTermios = unix.TIOCSETA
