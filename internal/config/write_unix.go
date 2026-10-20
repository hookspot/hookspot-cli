//go:build linux || darwin

package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func canonicalConfigPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}
	existing, _, err := existingAncestor(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", fmt.Errorf("resolve config directory aliases: %w", err)
	}
	missing, err := filepath.Rel(existing, absolute)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, missing), nil
}

func validateConfigFileType(_ string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("config file must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return errors.New("config path must be a regular file")
	}
	return nil
}

func validateConfigFile(path string, file *os.File, info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("config file permissions %o are not private", info.Mode().Perm())
	}
	if err := validateUnixOwner(info, "config file"); err != nil {
		return err
	}
	if err := validatePlatformAccess(file, true); err != nil {
		return fmt.Errorf("config file access is not private: %w", err)
	}
	return validateUnixAncestry(filepath.Dir(path))
}

func prepareConfigDirectory(path string, managed bool) error {
	dir := filepath.Dir(path)
	if err := createUnixDirectories(dir); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := validateUnixAncestry(dir); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect config directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("config parent must be a directory, not a symlink")
	}
	permissions := info.Mode().Perm()
	if managed && permissions&0o077 != 0 {
		return fmt.Errorf("managed config directory permissions %o are not private", permissions)
	}
	return nil
}

func createUnixDirectories(path string) error {
	existing, missing, err := existingAncestor(path)
	if err != nil {
		return err
	}
	if err = validateUnixAncestry(existing); err != nil {
		return err
	}
	for _, dir := range missing {
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := validateUnixAncestry(dir); err != nil {
			return err
		}
	}
	return nil
}

func validateUnixAncestry(path string) error {
	directories := make([]string, 0)
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		directories = append(directories, current)
		if parent := filepath.Dir(current); parent == current {
			break
		}
	}
	for index := len(directories) - 1; index >= 0; index-- {
		info, err := os.Lstat(directories[index])
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect config directory ancestry: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("effective config directory ancestry must contain only directories")
		}
		if err = validateUnixOwner(info, "config directory"); err != nil {
			return err
		}
		permissions := info.Mode().Perm()
		stickyRoot := info.Mode()&os.ModeSticky != 0 && unixOwnerID(info) == 0
		if permissions&0o022 != 0 && !stickyRoot {
			return fmt.Errorf("config directory permissions %o allow untrusted mutation", permissions)
		}
		file, err := os.Open(directories[index])
		if err != nil {
			return fmt.Errorf("open config directory for access check: %w", err)
		}
		accessErr := validatePlatformAccess(file, false)
		_ = file.Close()
		if accessErr != nil {
			return fmt.Errorf("config directory access permits untrusted mutation: %w", accessErr)
		}
	}
	return nil
}

func validateUnixOwner(info os.FileInfo, kind string) error {
	owner := unixOwnerID(info)
	if owner != 0 && owner != uint32(os.Geteuid()) {
		return fmt.Errorf("%s is not owned by the current user or root", kind)
	}
	return nil
}

func unixOwnerID(info os.FileInfo) uint32 {
	return info.Sys().(*syscall.Stat_t).Uid
}

func writeFileAtomic(path string, contents []byte, noOverwrite bool) error {
	return writeFileAtomicWithOperations(path, contents, noOverwrite, unixWriteOperations{
		write:  func(file *os.File, contents []byte) (int, error) { return file.Write(contents) },
		sync:   func(file *os.File) error { return file.Sync() },
		close:  func(file *os.File) error { return file.Close() },
		link:   os.Link,
		rename: os.Rename,
	})
}

type unixWriteOperations struct {
	write  func(*os.File, []byte) (int, error)
	sync   func(*os.File) error
	close  func(*os.File) error
	link   func(string, string) error
	rename func(string, string) error
}

func writeFileAtomicWithOperations(path string, contents []byte, noOverwrite bool, operations unixWriteOperations) error {
	if err := validateConfigTarget(path); err != nil {
		return err
	}

	temp, err := os.CreateTemp(filepath.Dir(path), ".hookspot-config-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tempPath := temp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temp.Close()
		}
		_ = os.Remove(tempPath)
	}()
	if err = validatePlatformAccess(temp, true); err != nil {
		return fmt.Errorf("temporary config access is not private: %w", err)
	}

	written, err := operations.write(temp, contents)
	if err != nil {
		return fmt.Errorf("write temporary config: %w", err)
	}
	if written != len(contents) {
		return io.ErrShortWrite
	}
	if err := operations.sync(temp); err != nil {
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := operations.close(temp); err != nil {
		closed = true
		return fmt.Errorf("close temporary config: %w", err)
	}
	closed = true

	if noOverwrite {
		if err := operations.link(tempPath, path); err != nil {
			return fmt.Errorf("publish new config: %w", err)
		}
		return nil
	}
	if err := operations.rename(tempPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}
