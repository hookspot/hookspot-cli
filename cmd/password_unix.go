//go:build linux || darwin

package cmd

import "golang.org/x/sys/unix"

func disableInputEcho(fd int) (func() error, error) {
	old, err := unix.IoctlGetTermios(fd, readTermios)
	if err != nil {
		return nil, err
	}
	mode := *old
	mode.Lflag &^= unix.ECHO
	mode.Lflag |= unix.ICANON | unix.ISIG
	mode.Iflag |= unix.ICRNL
	if err := unix.IoctlSetTermios(fd, writeTermios, &mode); err != nil {
		return nil, err
	}
	return func() error { return unix.IoctlSetTermios(fd, writeTermios, old) }, nil
}
