//go:build darwin

package config

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const aclAttributeBufferSize = 4096

// Darwin mode bits do not account for inherited ACL grants. Inspect the
// opened inode and reject relevant allow entries before reading or writing it.
func validatePlatformAccess(file *os.File, privateFile bool) error {
	attributes := unix.Attrlist{
		Bitmapcount: 5,
		Commonattr:  unix.ATTR_CMN_EXTENDED_SECURITY,
	}
	buffer := make([]byte, aclAttributeBufferSize)
	_, _, errno := syscall.Syscall6(
		syscall.SYS_FGETATTRLIST,
		file.Fd(),
		uintptr(unsafe.Pointer(&attributes)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		0,
		0,
	)
	runtime.KeepAlive(file)
	if errno != 0 {
		return fmt.Errorf("inspect Darwin ACL: %w", errno)
	}
	return validateDarwinACLBuffer(buffer, privateFile)
}
