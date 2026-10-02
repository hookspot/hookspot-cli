//go:build !windows

package cmd

import "golang.org/x/sys/unix"

// foreground reports whether this process's group owns the terminal f. A
// background job (`hookspot listen > log &`) that reads it is stopped by
// SIGTTIN.
func foreground(f any) bool {
	file, ok := f.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	group, err := unix.IoctlGetInt(int(file.Fd()), unix.TIOCGPGRP)
	return err == nil && group == unix.Getpgrp()
}
