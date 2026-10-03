//go:build !windows

package config

import (
	"os"
	"path/filepath"
)

func writePrivateTestFile(path string, contents []byte) error {
	return os.WriteFile(path, contents, 0o600)
}

func makePrivateTestDirs(dir string) error { return os.MkdirAll(dir, 0o700) }

// effectiveTestDir is dir as the store reports it, with symlinks resolved:
// t.TempDir() is under one on macOS (/var).
func effectiveTestDir(dir string) (string, error) { return filepath.EvalSymlinks(dir) }
