//go:build linux || darwin

package config

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestConfigRejectsFIFOAndSymlinkWithoutOpeningThem(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "config.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "config-link")
	if err := os.Symlink(fifo, symlink); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		path string
		open func(string) error
	}{
		{"read FIFO", fifo, func(path string) error {
			_, err := New(Options{ExplicitPath: path, ExplicitPathSet: true})
			return err
		}},
		{"read symlink", symlink, func(path string) error {
			_, err := New(Options{ExplicitPath: path, ExplicitPathSet: true})
			return err
		}},
		{"replace FIFO", fifo, func(path string) error {
			return writeFileAtomic(path, []byte("not-written"), false)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() { done <- test.open(test.path) }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("unsafe path was accepted")
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("unsafe path blocked while opening")
			}
		})
	}
}

func TestConfigValidatesEffectiveDirectoryAncestry(t *testing.T) {
	clearConfigEnvironment(t)
	home := setIsolatedHome(t)
	// Umask 002 leaves ~/.config group-writable.
	shared := filepath.Join(home, ".config")
	if err := os.Mkdir(shared, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o775); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOOKSPOT_CLI_KEY", "environment-key")
	store, err := New(Options{})
	if err != nil {
		t.Fatalf("absent config under a group-writable ancestor was rejected: %v", err)
	}
	cfg, err := store.Resolve(Overrides{})
	if err != nil || cfg.CLIKey != "environment-key" {
		t.Fatalf("resolved config = %+v, %v", cfg, err)
	}
	if err = store.SaveCLIKey("saved-key"); err == nil {
		t.Fatal("write under a group-writable ancestor was accepted")
	}
	existing := filepath.Join(shared, "hookspot", "config.toml")
	writeConfigFixture(t, existing, "schema_version = 1\n")
	// Unreadable, so only a folder check made before the open names the folder.
	if err = os.Chmod(existing, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = New(Options{}); err == nil || !strings.Contains(err.Error(), "allow untrusted mutation") {
		t.Fatalf("existing config under a group-writable ancestor: %v", err)
	}

	root := t.TempDir()
	if err = os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, "real")
	if err = os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err = os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	store, err = New(Options{
		ExplicitPath:    filepath.Join(alias, "private", "config.toml"),
		ExplicitPathSet: true, Intent: LoginCreate,
	})
	if err != nil {
		t.Fatalf("safe directory alias rejected: %v", err)
	}
	effectiveReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(effectiveReal, "private", "config.toml")
	if store.Path() != want {
		t.Fatalf("effective path = %q, want %q", store.Path(), want)
	}
	if err := store.SaveCLIKey("saved-key"); err != nil {
		t.Fatal(err)
	}
}

func TestConfigRejectsForeignOwnedAncestorWhenPrivileged(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to create a foreign-owned fixture")
	}
	root := t.TempDir()
	foreign := filepath.Join(root, "foreign")
	if err := os.Mkdir(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(foreign, 1234, 1234); err != nil {
		t.Fatal(err)
	}
	store, err := New(Options{
		ExplicitPath:    filepath.Join(foreign, "private", "config.toml"),
		ExplicitPathSet: true, Intent: LoginCreate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveCLIKey("saved-key"); err == nil {
		t.Fatal("foreign-owned ancestor was accepted")
	}
}
