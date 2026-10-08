//go:build windows

package cmd

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// privateFixtureDescriptor grants the current user and SYSTEM alone, with no
// inherited access, as the config store's own files and directories do.
func privateFixtureDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;;FA;;;SY)(A;;FA;;;" + user.User.Sid.String() + ")")
}

// makePrivateTestDirs creates dir with the private access the store requires
// of a directory it manages; os.MkdirAll leaves inherited access.
func makePrivateTestDirs(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	descriptor, err := privateFixtureDescriptor()
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func writeCommandFixture(path string, contents []byte) error {
	descriptor, err := privateFixtureDescriptor()
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
