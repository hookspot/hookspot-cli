//go:build darwin

package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func addDarwinACL(t *testing.T, path, entry string) {
	t.Helper()
	output, err := exec.Command("/bin/chmod", "+a", entry, path).CombinedOutput()
	if err != nil {
		t.Fatalf("add fixture ACL: %v: %s", err, output)
	}
}

func TestDarwinInheritedReadACLIsRejectedBeforeSecretWrite(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "inherited-read")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	addDarwinACL(t, parent, "everyone allow read,readattr,readextattr,readsecurity,file_inherit,directory_inherit")
	path := filepath.Join(parent, "config.toml")
	store, err := New(Options{Environment: "dev", ExplicitPath: path, ExplicitPathSet: true, Intent: LoginCreate})
	if err != nil {
		t.Fatal(err)
	}
	secret := "darwin-acl-test-key"
	if err := store.SaveCLIKey(secret); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("SaveCLIKey error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config exists after ACL rejection: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(parent, ".hookspot-config-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files remain: %v, %v", matches, err)
	}
}

func TestDarwinDenyOnlyACLIsAccepted(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "deny-only")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	addDarwinACL(t, parent, "everyone deny delete")
	t.Cleanup(func() { _ = exec.Command("/bin/chmod", "-N", parent).Run() })
	path := filepath.Join(parent, "config.toml")
	store, err := New(Options{Environment: "dev", ExplicitPath: path, ExplicitPathSet: true, Intent: LoginCreate})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCLIKey("test-key"); err != nil {
		t.Fatal(err)
	}
}

func TestDarwinMutationACLAndExistingReadACLAreRejected(t *testing.T) {
	root := t.TempDir()
	mutationParent := filepath.Join(root, "mutation")
	if err := os.Mkdir(mutationParent, 0o755); err != nil {
		t.Fatal(err)
	}
	addDarwinACL(t, mutationParent, "everyone allow add_file,delete_child")
	if _, err := New(Options{
		Environment: "dev", ExplicitPath: filepath.Join(mutationParent, "config.toml"),
		ExplicitPathSet: true, Intent: LoginCreate,
	}); err == nil {
		t.Fatal("directory mutation ACL was accepted")
	}

	existing := filepath.Join(root, "existing.toml")
	writeConfigFixture(t, existing, "schema_version = 1\nenvironment = 'dev'\n")
	addDarwinACL(t, existing, "everyone allow read")
	if _, err := New(Options{Environment: "dev", ExplicitPath: existing, ExplicitPathSet: true}); err == nil {
		t.Fatal("existing file read ACL was accepted")
	}
}
