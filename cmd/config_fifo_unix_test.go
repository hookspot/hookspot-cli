//go:build linux || darwin

package cmd

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestLegacyConfigRejectsFIFOBeforeOpening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := readLegacyConfig(path); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("legacy FIFO was accepted")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("legacy FIFO blocked while opening")
	}
}
