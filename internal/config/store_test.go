package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	keys := []string{
		"HOOKSPOT_ENVIRONMENT", "HOOKSPOT_CONFIG_FILE", "HOOKSPOT_CLI_KEY",
		"HOOKSPOT_ORGANIZATION_SLUG", "HOOKSPOT_PROJECT_SLUG", "HOOKSPOT_PROJECT",
	}
	for _, environment := range []string{"DEV", "STAGE", "PROD"} {
		for _, suffix := range []string{"CONFIG_FILE", "CLI_KEY", "ORGANIZATION_SLUG", "PROJECT_SLUG"} {
			keys = append(keys, "HOOKSPOT_"+environment+"_"+suffix)
		}
	}
	for _, key := range keys {
		value, set := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if set {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}

func writeConfigFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateTestFile(path, []byte(contents)); err != nil {
		t.Fatal(err)
	}
}

func stringPointer(value string) *string { return &value }

func setIsolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestNewSelectsEnvironmentSpecificPaths(t *testing.T) {
	clearConfigEnvironment(t)
	home := setIsolatedHome(t)

	store, err := New(Options{Environment: "stage"})
	if err != nil {
		t.Fatal(err)
	}
	effectiveHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(effectiveHome, ".config", "hookspot", "stage", "config.toml")
	if store.Path() != want {
		t.Fatalf("Path() = %q, want %q", store.Path(), want)
	}

	t.Setenv("HOOKSPOT_STAGE_CONFIG_FILE", filepath.Join(home, "scoped.toml"))
	t.Setenv("HOOKSPOT_PROD_CONFIG_FILE", filepath.Join(home, "ignored.toml"))
	if _, err = New(Options{Environment: "stage"}); err == nil {
		t.Fatal("ordinary read accepted a missing scoped path")
	}
	store, err = New(Options{Environment: "stage", Intent: LoginCreate})
	if err != nil {
		t.Fatal(err)
	}
	if store.Path() != filepath.Join(effectiveHome, "scoped.toml") {
		t.Fatalf("scoped Path() = %q", store.Path())
	}

	explicit := filepath.Join(home, "explicit.toml")
	if _, err := New(Options{Environment: "stage", ExplicitPath: explicit, ExplicitPathSet: true}); err == nil {
		t.Fatal("ordinary read accepted a missing explicit path")
	}
	store, err = New(Options{Environment: "stage", ExplicitPath: explicit, ExplicitPathSet: true, Intent: LoginCreate})
	if err != nil {
		t.Fatal(err)
	}
	if store.Path() != filepath.Join(effectiveHome, "explicit.toml") {
		t.Fatalf("explicit Path() = %q", store.Path())
	}
}

func TestNewRequiresValidPathInputsAndLegacyAssertion(t *testing.T) {
	clearConfigEnvironment(t)
	setIsolatedHome(t)
	if _, err := New(Options{Environment: "qa"}); err == nil {
		t.Fatal("unknown environment was accepted")
	}
	if _, err := New(Options{Environment: "dev", ExplicitPathSet: true}); err == nil {
		t.Fatal("explicit empty path was accepted")
	}

	legacy := filepath.Join(t.TempDir(), "legacy.toml")
	t.Setenv("HOOKSPOT_CONFIG_FILE", legacy)
	if _, err := New(Options{Environment: "stage"}); err == nil {
		t.Fatal("legacy path without assertion was accepted")
	}
	t.Setenv("HOOKSPOT_ENVIRONMENT", "prod")
	if _, err := New(Options{Environment: "stage"}); err == nil {
		t.Fatal("legacy path with conflicting assertion was accepted")
	}
	t.Setenv("HOOKSPOT_ENVIRONMENT", "stage")
	store, err := New(Options{Environment: "stage", Intent: LoginCreate})
	if err != nil {
		t.Fatal(err)
	}
	effectiveLegacyParent, err := filepath.EvalSymlinks(filepath.Dir(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if store.Path() != filepath.Join(effectiveLegacyParent, filepath.Base(legacy)) {
		t.Fatalf("Path() = %q, want legacy path", store.Path())
	}
}

func TestNewValidatesMarkedRecordBeforeResolution(t *testing.T) {
	clearConfigEnvironment(t)
	setIsolatedHome(t)
	tests := []struct {
		name     string
		contents string
		wantErr  bool
	}{
		{"valid", "schema_version = 1\nenvironment = 'stage'\ncli_key = 'file-key'\nproject = 'proj_1'\nlog_level = 'obsolete'\n", false},
		{"wrong schema", "schema_version = 2\nenvironment = 'stage'\ncli_key = 'file-key'\n", true},
		{"wrong environment", "schema_version = 1\nenvironment = 'prod'\ncli_key = 'file-key'\n", true},
		{"legacy markerless", "cli_key = 'file-key'\nproject = 'proj_1'\n", true},
		{"malformed", "schema_version = [", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			writeConfigFixture(t, path, test.contents)
			store, err := New(Options{Environment: "stage", ExplicitPath: path, ExplicitPathSet: true})
			if (err != nil) != test.wantErr {
				t.Fatalf("New() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil {
				cfg, err := store.Resolve(Overrides{NeedProject: true})
				if err != nil {
					t.Fatal(err)
				}
				if cfg.CLIKey != "file-key" || cfg.Project != "proj_1" {
					t.Fatalf("resolved config = %+v", cfg)
				}
			}
		})
	}
}

func TestEnvironmentStoresRemainIndependent(t *testing.T) {
	clearConfigEnvironment(t)
	home := setIsolatedHome(t)
	stage, err := New(Options{Environment: "stage", Intent: LoginCreate})
	if err != nil {
		t.Fatal(err)
	}
	prod, err := New(Options{Environment: "prod", Intent: LoginCreate})
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.SaveCLIKey("stage-key"); err != nil {
		t.Fatal(err)
	}
	if err := prod.SaveCLIKey("prod-key"); err != nil {
		t.Fatal(err)
	}
	prodBefore, err := os.ReadFile(prod.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.SaveProject("stage-project"); err != nil {
		t.Fatal(err)
	}
	prodAfter, err := os.ReadFile(prod.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(prodAfter) != string(prodBefore) {
		t.Fatal("stage mutation changed prod config")
	}
	effectiveHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if stage.Path() != filepath.Join(effectiveHome, ".config", "hookspot", "stage", "config.toml") ||
		prod.Path() != filepath.Join(effectiveHome, ".config", "hookspot", "prod", "config.toml") {
		t.Fatalf("unexpected namespace paths: %q %q", stage.Path(), prod.Path())
	}
}

func TestResolveUsesOnlyTheWinningCredentialTier(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	writeConfigFixture(t, path, "schema_version = 1\nenvironment = 'stage'\ncli_key = 'file-key'\n")
	t.Setenv("HOOKSPOT_CLI_KEY", "legacy-key")
	t.Setenv("HOOKSPOT_ENVIRONMENT", "wrong")
	t.Setenv("HOOKSPOT_STAGE_CLI_KEY", "scoped-key")

	store, err := New(Options{Environment: "stage", ExplicitPath: path, ExplicitPathSet: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := store.Resolve(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CLIKey != "scoped-key" {
		t.Fatalf("scoped config = %+v", cfg)
	}

	cfg, err = store.Resolve(Overrides{CLIKey: stringPointer("")})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CLIKey != "" {
		t.Fatalf("explicit empty flag fell through: %+v", cfg)
	}
}

func TestResolveCredentialIgnoresIncompleteProjectEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	writeConfigFixture(t, path, "schema_version = 1\nenvironment = 'dev'\ncli_key = 'file-key'\nproject = 'stored-project'\n")
	t.Setenv("HOOKSPOT_DEV_ORGANIZATION_SLUG", "organization-without-project")

	store, err := New(Options{Environment: "dev", ExplicitPath: path, ExplicitPathSet: true, Intent: LoginCreate})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := store.Resolve(Overrides{})
	if err != nil {
		t.Fatalf("credential-only resolution failed: %v", err)
	}
	if cfg.CLIKey != "file-key" || cfg.Project != "" {
		t.Fatalf("credential-only config = %+v", cfg)
	}
}

func TestResolveRequiresAssertionOnlyWhenLegacyCredentialWins(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	writeConfigFixture(t, path, "schema_version = 1\nenvironment = 'prod'\ncli_key = 'file-key'\n")
	t.Setenv("HOOKSPOT_CLI_KEY", "legacy-key")

	store, err := New(Options{Environment: "prod", ExplicitPath: path, ExplicitPathSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(Overrides{}); err == nil {
		t.Fatal("legacy key without assertion was accepted")
	}
	t.Setenv("HOOKSPOT_ENVIRONMENT", "prod")
	cfg, err := store.Resolve(Overrides{NeedProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CLIKey != "legacy-key" {
		t.Fatalf("legacy config = %+v", cfg)
	}
}

func TestResolveProjectPrecedenceDoesNotMixSlugPairs(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	writeConfigFixture(t, path, "schema_version = 1\nenvironment = 'dev'\nproject = 'stored-project'\n")
	store, err := New(Options{Environment: "dev", ExplicitPath: path, ExplicitPathSet: true})
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOOKSPOT_DEV_ORGANIZATION_SLUG", "scoped-org")
	cfg, err := store.Resolve(Overrides{Project: stringPointer("flag-project"), NeedProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project != "flag-project" || cfg.OrganizationSlug != "" || cfg.ProjectSlug != "" {
		t.Fatalf("project flag did not win: %+v", cfg)
	}
	if _, err := store.Resolve(Overrides{NeedProject: true}); err == nil {
		t.Fatal("incomplete scoped slug pair was accepted")
	}

	t.Setenv("HOOKSPOT_DEV_PROJECT_SLUG", "scoped-project")
	t.Setenv("HOOKSPOT_PROJECT_SLUG", "ignored-legacy-project")
	cfg, err = store.Resolve(Overrides{NeedProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project != "" || cfg.OrganizationSlug != "scoped-org" || cfg.ProjectSlug != "scoped-project" {
		t.Fatalf("scoped pair was mixed or ignored: %+v", cfg)
	}
}

func TestResolveIgnoresGenericProjectUIDEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	writeConfigFixture(t, path, "schema_version = 1\nenvironment = 'dev'\nproject = 'stored-project'\n")
	t.Setenv("HOOKSPOT_PROJECT", "ignored-project")
	store, err := New(Options{Environment: "dev", ExplicitPath: path, ExplicitPathSet: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := store.Resolve(Overrides{NeedProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project != "stored-project" {
		t.Fatalf("Project = %q", cfg.Project)
	}
}

func TestStoreMutationsPersistOnlyMarkedOwnedFields(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("HOOKSPOT_DEV_CLI_KEY", "environment-key-sentinel")
	t.Setenv("HOOKSPOT_DEV_ORGANIZATION_SLUG", "environment-org")
	t.Setenv("HOOKSPOT_DEV_PROJECT_SLUG", "environment-project")
	store, err := New(Options{Environment: "dev", ExplicitPath: path, ExplicitPathSet: true, Intent: LoginCreate})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProject("saved-project"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	for _, forbidden := range []string{"environment-key-sentinel", "environment-org", "environment-project", "organization_slug", "project_slug"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("persisted resolved value %q", forbidden)
		}
	}
	for _, required := range []string{"schema_version = 1", `environment = 'dev'`, `project = 'saved-project'`} {
		if !strings.Contains(text, required) {
			t.Fatalf("config missing %q:\n%s", required, text)
		}
	}

	if err := store.SaveCLIKey("saved-key"); err != nil {
		t.Fatal(err)
	}
	if err := store.ClearCLIKey(); err != nil {
		t.Fatal(err)
	}
	contents, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "saved-key") || !strings.Contains(string(contents), "saved-project") {
		t.Fatalf("unexpected config after logout: %s", contents)
	}
}

func TestEnvironmentCLIKeyActiveUsesEnvironmentTierOnly(t *testing.T) {
	clearConfigEnvironment(t)
	setIsolatedHome(t)
	store, err := New(Options{Environment: "stage"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOOKSPOT_CLI_KEY", "legacy-key")
	t.Setenv("HOOKSPOT_ENVIRONMENT", "wrong")
	if store.EnvironmentCLIKeyActive() {
		t.Fatal("mismatched generic key reported active")
	}
	t.Setenv("HOOKSPOT_STAGE_CLI_KEY", "scoped-key")
	if !store.EnvironmentCLIKeyActive() {
		t.Fatal("nonempty scoped key was not reported active")
	}
	t.Setenv("HOOKSPOT_STAGE_CLI_KEY", "")
	if store.EnvironmentCLIKeyActive() {
		t.Fatal("empty winning scoped key reported active")
	}
}

func TestClearCLIKeySkipsAbsentAndAlreadyEmptyStores(t *testing.T) {
	clearConfigEnvironment(t)
	for _, contents := range []string{"", "schema_version = 1\nenvironment = 'dev'\n"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		intent := LoginCreate
		if contents != "" {
			writeConfigFixture(t, path, contents)
			intent = Read
		}
		store, err := New(Options{Environment: "dev", ExplicitPath: path, ExplicitPathSet: true, Intent: intent})
		if err != nil {
			t.Fatal(err)
		}
		store.write = func(string, []byte, bool) error {
			t.Fatal("unnecessary config write")
			return nil
		}
		if err := store.ClearCLIKey(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStoreMutationFailurePreservesDiskAndMemory(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "schema_version = 1\nenvironment = 'prod'\ncli_key = 'old-key'\nproject = 'old-project'\n"
	writeConfigFixture(t, path, original)
	store, err := New(Options{Environment: "prod", ExplicitPath: path, ExplicitPathSet: true})
	if err != nil {
		t.Fatal(err)
	}
	store.write = func(string, []byte, bool) error { return errors.New("publish failed") }
	if err := store.SaveProject("new-project"); err == nil {
		t.Fatal("SaveProject succeeded")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != original {
		t.Fatalf("disk changed: %q", contents)
	}
	cfg, err := store.Resolve(Overrides{NeedProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project != "old-project" {
		t.Fatalf("in-memory project = %q", cfg.Project)
	}
}

func TestImportDoesNotOverwriteDestination(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	store, err := New(Options{Environment: "stage", ExplicitPath: path, ExplicitPathSet: true, Intent: MigrationCreate})
	if err != nil {
		t.Fatal(err)
	}
	writeConfigFixture(t, path, "schema_version = 1\nenvironment = 'stage'\ncli_key = 'concurrent-key'\n")
	if err := store.Import("imported-key", "imported-project"); err == nil {
		t.Fatal("Import overwrote destination")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "concurrent-key") || strings.Contains(string(contents), "imported-key") {
		t.Fatalf("destination changed: %s", contents)
	}
}
