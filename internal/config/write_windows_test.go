//go:build windows

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsStoreRejectsReparseTarget(t *testing.T) {
	clearConfigEnvironment(t)
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.toml")
	writeConfigFixture(t, realPath, "schema_version = 1\nenvironment = 'dev'\n")
	symlinkPath := filepath.Join(dir, "link.toml")
	if err := os.Symlink(realPath, symlinkPath); err != nil {
		if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
			t.Skip("creating a symlink is not permitted for this test process")
		}
		t.Fatal(err)
	}
	if _, err := New(Options{Environment: "dev", ExplicitPath: symlinkPath, ExplicitPathSet: true}); err == nil {
		t.Fatal("reparse-point config target was accepted")
	}
}

func TestWindowsStoreCreatesPrivateFileAndPreservesParentAccess(t *testing.T) {
	clearConfigEnvironment(t)
	dir := t.TempDir()
	parent := filepath.Join(dir, "public-parent")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := windows.GetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "config.toml")
	store, err := New(Options{Environment: "dev", ExplicitPath: path, ExplicitPathSet: true, Intent: LoginCreate})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCLIKey("saved-key"); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWindowsHandleAccess(file, true, true); err != nil {
		_ = file.Close()
		t.Fatalf("created config access = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := windows.GetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if before.String() != after.String() {
		t.Fatal("explicit parent access descriptor changed")
	}
}

func TestWindowsDescriptorRejectsForeignOwnerAndUntrustedMutation(t *testing.T) {
	user, _, _, err := trustedWindowsSIDs()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		descriptor string
		file       bool
	}{
		{
			name:       "foreign file owner",
			descriptor: "O:BUD:P(A;;FA;;;SY)(A;;FA;;;" + user.String() + ")",
			file:       true,
		},
		{
			name:       "untrusted directory mutation",
			descriptor: "O:" + user.String() + "D:P(A;;FA;;;SY)(A;;FA;;;" + user.String() + ")(A;;DC;;;WD)",
			file:       false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor, err := windows.SecurityDescriptorFromString(test.descriptor)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateWindowsDescriptor(descriptor, true, test.file); err == nil {
				t.Fatal("unsafe descriptor was accepted")
			} else if strings.Contains(err.Error(), user.String()) {
				t.Fatal("error exposed account identifier")
			}
		})
	}
}

func TestWindowsDescriptorAllowsCreateOnlyAncestorRights(t *testing.T) {
	user, _, _, err := trustedWindowsSIDs()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		"O:" + user.String() + "D:P(A;;FA;;;SY)(A;;FA;;;" + user.String() + ")(A;;0x00000006;;;WD)",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWindowsDescriptor(descriptor, false, false); err != nil {
		t.Fatalf("create-only ancestor rejected: %v", err)
	}
}
