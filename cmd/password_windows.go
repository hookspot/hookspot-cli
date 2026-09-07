//go:build windows

package cmd

import "golang.org/x/sys/windows"

func disableInputEcho(fd int) (func() error, error) {
	handle := windows.Handle(fd)
	var old uint32
	if err := windows.GetConsoleMode(handle, &old); err != nil {
		return nil, err
	}
	mode := old &^ windows.ENABLE_ECHO_INPUT
	mode |= windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT
	if err := windows.SetConsoleMode(handle, mode); err != nil {
		return nil, err
	}
	return func() error { return windows.SetConsoleMode(handle, old) }, nil
}
