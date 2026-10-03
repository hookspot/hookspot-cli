//go:build windows

package config

import (
	"os"
	"path/filepath"
	"unsafe"

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
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	descriptor, err := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;;FA;;;SY)(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, &attributes, windows.CREATE_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
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
