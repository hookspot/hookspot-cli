//go:build !windows

package cmd

import "os"

func makePrivateTestDirs(dir string) error { return os.MkdirAll(dir, 0o700) }

func writeCommandFixture(path string, contents []byte) error {
	return os.WriteFile(path, contents, 0o600)
}
