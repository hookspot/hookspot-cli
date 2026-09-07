//go:build windows

package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const fileDeleteChild windows.ACCESS_MASK = 0x00000040

func validateConfigFileType(path string, _ os.FileInfo) error {
	attributes, err := windows.GetFileAttributes(mustUTF16(path))
	if err != nil {
		return fmt.Errorf("inspect config file: %w", err)
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("config file must not be a reparse point")
	}
	if attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return errors.New("config path must be a regular file")
	}
	return nil
}

func validateConfigFile(_ string, file *os.File, _ os.FileInfo) error {
	return validateWindowsHandleAccess(file, true, true)
}

func prepareConfigDirectory(path string, managed bool) error {
	dir := filepath.Dir(path)
	if err := createPrivateDirectories(dir); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := validateWindowsDirectoryChain(dir); err != nil {
		return err
	}
	return validateWindowsAccess(dir, managed, false)
}

func canonicalConfigPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}
	current := filepath.Dir(absolute)
	for {
		if _, err := os.Lstat(current); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect config directory: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("config path has no existing directory ancestor")
		}
		current = parent
	}
	if err := validateWindowsDirectoryChain(current); err != nil {
		return "", err
	}
	return absolute, nil
}

func writeFileAtomic(path string, contents []byte, noOverwrite bool) error {
	if info, err := os.Lstat(path); err == nil {
		if err := validateConfigFileType(path, info); err != nil {
			return err
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return statErr
		}
		if !os.SameFile(info, openedInfo) {
			_ = file.Close()
			return errors.New("config target changed while it was being opened")
		}
		validateErr := validateConfigFile(path, file, info)
		_ = file.Close()
		if validateErr != nil {
			return validateErr
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect config target: %w", err)
	}

	temp, tempPath, err := createPrivateTemp(filepath.Dir(path))
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = temp.Close()
		}
		_ = os.Remove(tempPath)
	}()
	if err := validateWindowsHandleAccess(temp, true, true); err != nil {
		return err
	}
	written, err := temp.Write(contents)
	if err != nil {
		return fmt.Errorf("write temporary config: %w", err)
	}
	if written != len(contents) {
		return io.ErrShortWrite
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := temp.Close(); err != nil {
		closed = true
		return fmt.Errorf("close temporary config: %w", err)
	}
	closed = true

	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if !noOverwrite {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	if err := windows.MoveFileEx(mustUTF16(tempPath), mustUTF16(path), flags); err != nil {
		return fmt.Errorf("publish config: %w", err)
	}
	return nil
}

func createPrivateTemp(dir string) (*os.File, string, error) {
	attributes, err := privateSecurityAttributes()
	if err != nil {
		return nil, "", err
	}
	for attempt := 0; attempt < 100; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", fmt.Errorf("generate temporary config name: %w", err)
		}
		path := filepath.Join(dir, ".hookspot-config-"+hex.EncodeToString(random[:]))
		handle, err := windows.CreateFile(
			mustUTF16(path), windows.GENERIC_READ|windows.GENERIC_WRITE, 0, attributes,
			windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_WRITE_THROUGH, 0,
		)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("create temporary config: %w", err)
		}
		return os.NewFile(uintptr(handle), path), path, nil
	}
	return nil, "", errors.New("create temporary config: too many name collisions")
}

func createPrivateDirectories(path string) error {
	missing := make([]string, 0)
	current := filepath.Clean(path)
	for {
		if _, err := os.Lstat(current); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return errors.New("no existing config directory ancestor")
		}
		current = parent
	}
	if err := validateWindowsDirectoryChain(current); err != nil {
		return err
	}
	attributes, err := privateSecurityAttributes()
	if err != nil {
		return err
	}
	for index := len(missing) - 1; index >= 0; index-- {
		if err := windows.CreateDirectory(mustUTF16(missing[index]), attributes); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return err
		}
		if err := validateWindowsDirectoryChain(missing[index]); err != nil {
			return err
		}
	}
	return nil
}

func validateWindowsDirectoryChain(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		attributes, err := windows.GetFileAttributes(mustUTF16(current))
		if err != nil {
			return fmt.Errorf("inspect config directory: %w", err)
		}
		if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("config directory chain must not contain reparse points")
		}
		if attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return errors.New("config parent must be a directory")
		}
		if err := validateWindowsAccess(current, false, false); err != nil {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func validateWindowsAccess(path string, requireProtected, file bool) error {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("inspect config access: %w", err)
	}
	return validateWindowsDescriptor(descriptor, requireProtected, file)
}

func validateWindowsHandleAccess(file *os.File, requireProtected, privateFile bool) error {
	descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("inspect config access: %w", err)
	}
	return validateWindowsDescriptor(descriptor, requireProtected, privateFile)
}

func validateWindowsDescriptor(descriptor *windows.SECURITY_DESCRIPTOR, requireProtected, file bool) error {
	if descriptor == nil {
		return errors.New("config access descriptor is missing")
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return fmt.Errorf("inspect config access control: %w", err)
	}
	if requireProtected && control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("config access is inherited instead of private")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.New("config access list is missing")
	}
	user, system, administrators, err := trustedWindowsSIDs()
	if err != nil {
		return err
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil {
		return errors.New("config owner is missing")
	}
	trustedOwner := owner.Equals(user) || owner.Equals(system) || !file && owner.Equals(administrators)
	if !trustedOwner {
		return errors.New("config is not owned by a trusted account")
	}
	// Creating a sibling does not let another principal replace an existing
	// trusted child. Reject rights that can delete that child or rewrite the
	// directory's access rules, while allowing common create-only drive roots.
	unsafeDirectoryRights := windows.ACCESS_MASK(
		windows.GENERIC_ALL | windows.GENERIC_WRITE | windows.DELETE |
			windows.WRITE_DAC | windows.WRITE_OWNER |
			windows.FILE_WRITE_ATTRIBUTES | windows.FILE_WRITE_EA,
	)
	unsafeDirectoryRights |= fileDeleteChild
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return fmt.Errorf("inspect config access entry: %w", err)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("config access list contains an unsupported allow entry")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		trusted := sid.Equals(user) || sid.Equals(system) || !file && sid.Equals(administrators)
		if !trusted && (file || ace.Mask&unsafeDirectoryRights != 0) {
			return errors.New("config access list grants untrusted access")
		}
	}
	return nil
}

func privateSecurityAttributes() (*windows.SecurityAttributes, error) {
	user, _, _, err := trustedWindowsSIDs()
	if err != nil {
		return nil, err
	}
	descriptor, err := windows.SecurityDescriptorFromString("O:" + user.String() + "D:P(A;;FA;;;SY)(A;;FA;;;" + user.String() + ")")
	if err != nil {
		return nil, fmt.Errorf("create private access descriptor: %w", err)
	}
	return &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}, nil
}

func trustedWindowsSIDs() (*windows.SID, *windows.SID, *windows.SID, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, nil, nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, nil, nil, err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, nil, nil, err
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, nil, nil, err
	}
	return user.User.Sid, system, administrators, nil
}

func mustUTF16(value string) *uint16 {
	pointer, err := windows.UTF16PtrFromString(value)
	if err != nil {
		panic("config path contains a NUL byte")
	}
	return pointer
}
