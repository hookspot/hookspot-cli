package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_ReadsValuesFromFile(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")

	contents := "cli_key = \"from-file\"\nproject = \"proj_1\"\n"
	if err := os.WriteFile(configFile, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cfg := Load(v)
	if cfg.CLIKey != "from-file" {
		t.Fatalf("CLIKey = %q, want %q", cfg.CLIKey, "from-file")
	}
	if cfg.Project != "proj_1" {
		t.Fatalf("Project = %q, want %q", cfg.Project, "proj_1")
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(configFile, []byte("cli_key = \"from-file\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("HOOKSPOT_CLI_KEY", "from-env")

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cfg := Load(v)
	if cfg.CLIKey != "from-env" {
		t.Fatalf("CLIKey = %q, want %q", cfg.CLIKey, "from-env")
	}
}

func TestLoad_ReadsProjectSlugsFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")

	t.Setenv("HOOKSPOT_ORGANIZATION_SLUG", "acme")
	t.Setenv("HOOKSPOT_PROJECT_SLUG", "payments")

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cfg := Load(v)
	if cfg.OrganizationSlug != "acme" {
		t.Fatalf("OrganizationSlug = %q, want %q", cfg.OrganizationSlug, "acme")
	}
	if cfg.ProjectSlug != "payments" {
		t.Fatalf("ProjectSlug = %q, want %q", cfg.ProjectSlug, "payments")
	}
}

func TestLoad_DoesNotReadProjectFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(configFile, []byte("project = \"from-file\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("HOOKSPOT_PROJECT", "from-env")

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if cfg := Load(v); cfg.Project != "from-file" {
		t.Fatalf("Project = %q, want %q", cfg.Project, "from-file")
	}
}

func TestSave_WritesAndReloads(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "nested", "config.toml")

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	v.Set("cli_key", "saved-key")

	if err := Save(v, configFile); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := New(configFile)
	if err != nil {
		t.Fatalf("New (reload): %v", err)
	}

	cfg := Load(reloaded)
	if cfg.CLIKey != "saved-key" {
		t.Fatalf("CLIKey = %q, want %q", cfg.CLIKey, "saved-key")
	}
}

func TestSave_SetsRestrictivePermissions(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	v.Set("cli_key", "saved-key")

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
