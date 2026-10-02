//go:build windows

package cmd

// foreground is always true: Windows has no background jobs.
func foreground(any) bool { return true }
