//go:build !windows

package config

import "os"

func writePrivateTestFile(path string, contents []byte) error {
	return os.WriteFile(path, contents, 0o600)
}
