package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew_DefaultServerURL(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cfg := Load(v)
	if cfg.ServerURL != DefaultServerURL {
		t.Fatalf("ServerURL = %q, want %q", cfg.ServerURL, DefaultServerURL)
	}
}

func TestLoad_ReadsValuesFromFile(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")

	contents := "token = \"from-file\"\nproject = \"proj_1\"\n"
	if err := os.WriteFile(configFile, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cfg := Load(v)
	if cfg.Token != "from-file" {
		t.Fatalf("Token = %q, want %q", cfg.Token, "from-file")
	}
	if cfg.Project != "proj_1" {
		t.Fatalf("Project = %q, want %q", cfg.Project, "proj_1")
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(configFile, []byte("token = \"from-file\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("HOOKSPOT_TOKEN", "from-env")

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cfg := Load(v)
	if cfg.Token != "from-env" {
		t.Fatalf("Token = %q, want %q", cfg.Token, "from-env")
	}
}

func TestSave_WritesAndReloads(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "nested", "config.toml")

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	v.Set("token", "saved-token")

	if err := Save(v, configFile); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := New(configFile)
	if err != nil {
		t.Fatalf("New (reload): %v", err)
	}

	cfg := Load(reloaded)
	if cfg.Token != "saved-token" {
		t.Fatalf("Token = %q, want %q", cfg.Token, "saved-token")
	}
}

func TestSave_SetsRestrictivePermissions(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	v.Set("token", "saved-token")

	if err := Save(v, configFile); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(configFile)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file mode = %o, want %o", perm, 0o600)
	}
}
