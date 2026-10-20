//go:build windows

package config

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// makePrivateTestDirs creates dir as the store does: os.MkdirAll leaves
// inherited access, which the store rejects on a managed directory.
func makePrivateTestDirs(dir string) error { return createPrivateDirectories(dir) }

// effectiveTestDir is dir as the store reports it: absolute and otherwise as
// given. The store rejects reparse points instead of resolving them, and
// resolving would expand 8.3 short names in %TEMP%.
func effectiveTestDir(dir string) (string, error) { return filepath.Abs(dir) }

func writePrivateTestFile(path string, contents []byte) error {
	attributes, err := privateSecurityAttributes()
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, attributes, windows.CREATE_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(handle), path)
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
