//go:build linux || darwin

package config

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestUnixAtomicWriterFaultsPreserveStoreAndRemoveTemp(t *testing.T) {
	clearConfigEnvironment(t)
	wantErr := errors.New("injected writer failure")
	tests := []struct {
		name   string
		change func(*unixWriteOperations)
	}{
		{"write", func(operations *unixWriteOperations) {
			operations.write = func(*os.File, []byte) (int, error) { return 0, wantErr }
		}},
		{"sync", func(operations *unixWriteOperations) {
			operations.sync = func(*os.File) error { return wantErr }
		}},
		{"close", func(operations *unixWriteOperations) {
			operations.close = func(file *os.File) error { _ = file.Close(); return wantErr }
		}},
		{"publish", func(operations *unixWriteOperations) {
			operations.rename = func(string, string) error { return wantErr }
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			original := "schema_version = 1\nenvironment = 'dev'\ncli_key = 'old-key'\nproject = 'old-project'\n"
			writeConfigFixture(t, path, original)
			store, err := New(Options{Environment: "dev", ExplicitPath: path, ExplicitPathSet: true})
			if err != nil {
				t.Fatal(err)
			}
			operations := unixWriteOperations{
				write:  func(file *os.File, contents []byte) (int, error) { return file.Write(contents) },
				sync:   func(file *os.File) error { return file.Sync() },
				close:  func(file *os.File) error { return file.Close() },
				link:   os.Link,
				rename: os.Rename,
			}
			test.change(&operations)
			store.write = func(path string, contents []byte, noOverwrite bool) error {
				return writeFileAtomicWithOperations(path, contents, noOverwrite, operations)
			}
			if err := store.SaveProject("new-project"); !errors.Is(err, wantErr) {
				t.Fatalf("SaveProject error = %v", err)
			}
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != original {
				t.Fatalf("old config changed: %v %q", err, contents)
			}
			resolved, err := store.Resolve(Overrides{NeedProject: true})
			if err != nil || resolved.Project != "old-project" {
				t.Fatalf("in-memory record changed: %+v, %v", resolved, err)
			}
			matches, err := filepath.Glob(filepath.Join(dir, ".hookspot-config-*"))
			if err != nil || len(matches) != 0 {
				t.Fatalf("temporary files remain: %v, %v", matches, err)
			}
		})
	}
}

func TestUnixAtomicWriterCreatesPrivateFileUnderPermissiveUmask(t *testing.T) {
	clearConfigEnvironment(t)
	dir := t.TempDir()
	oldUmask := syscall.Umask(0)
	defer syscall.Umask(oldUmask)
	path := filepath.Join(dir, "config.toml")
	store, err := New(Options{Environment: "dev", ExplicitPath: path, ExplicitPathSet: true, Intent: LoginCreate})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCLIKey("test-key"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}
}

func TestUnixStoreRejectsSymlinkTargetAndPreservesParentMode(t *testing.T) {
	clearConfigEnvironment(t)
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.toml")
	writeConfigFixture(t, realPath, "schema_version = 1\nenvironment = 'dev'\n")
	symlinkPath := filepath.Join(dir, "link.toml")
	if err := os.Symlink(realPath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{Environment: "dev", ExplicitPath: symlinkPath, ExplicitPathSet: true}); err == nil {
		t.Fatal("symlink config target was accepted")
	}

	path := filepath.Join(dir, "public-parent", "config.toml")
	if err := os.Mkdir(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := New(Options{Environment: "dev", ExplicitPath: path, ExplicitPathSet: true, Intent: LoginCreate})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCLIKey("saved-key"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o", info.Mode().Perm())
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if parent.Mode().Perm() != 0o755 {
		t.Fatalf("explicit parent mode changed to %o", parent.Mode().Perm())
	}
}

func TestAbsentLocalConfigFallsBackFromGroupWritableWorkingDirectory(t *testing.T) {
	clearConfigEnvironment(t)
	home := setIsolatedHome(t)
	globalPath := filepath.Join(home, ".config", "hookspot", "dev", "config.toml")
	writeConfigFixture(t, globalPath, "schema_version = 1\nenvironment = 'dev'\nproject = 'global-project'\n")

	working := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(working, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(working, 0o770); err != nil {
		t.Fatal(err)
	}
	setWorkingDirectory(t, working)

	store, err := New(Options{Environment: "dev"})
	if err != nil {
		t.Fatalf("global fallback failed: %v", err)
	}
	wantGlobalPath := canonicalTestPath(t, globalPath)
	if store.Path() != wantGlobalPath {
		t.Fatalf("Path() = %q, want %q", store.Path(), wantGlobalPath)
	}
}

func TestExistingLocalConfigRejectsGroupWritableDirectory(t *testing.T) {
	clearConfigEnvironment(t)
	setIsolatedHome(t)
	working := t.TempDir()
	localDirectory := filepath.Join(working, ".hookspot", "dev")
	if err := os.MkdirAll(localDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(localDirectory, "config.toml")
	if err := writePrivateTestFile(localPath, []byte("schema_version = 1\nenvironment = 'dev'\nproject = 'local-project'\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(localDirectory, 0o770); err != nil {
		t.Fatal(err)
	}
	setWorkingDirectory(t, working)

	if _, err := New(Options{Environment: "dev"}); err == nil {
		t.Fatal("local config in a group-writable directory was accepted")
	}
}
