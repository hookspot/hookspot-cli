//go:build !windows

package cmd

import "os"

func writeCommandFixture(path string, contents []byte) error {
	return os.WriteFile(path, contents, 0o600)
}
