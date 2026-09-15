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
	setWorkingDirectory(t, t.TempDir())
	return home
}

func setWorkingDirectory(t *testing.T, path string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(parent, filepath.Base(path))
}

func TestNewPrefersEnvironmentLocalConfigAndKeepsEnvironmentsIndependent(t *testing.T) {
	clearConfigEnvironment(t)
	home := setIsolatedHome(t)
	working := t.TempDir()
	setWorkingDirectory(t, working)

	writeConfigFixture(t, filepath.Join(home, ".config", "hookspot", "dev", "config.toml"), "schema_version = 1\nenvironment = 'dev'\ncli_key = 'global-key'\nproject = 'global-project'\n")
	localPath := filepath.Join(working, ".hookspot", "dev", "config.toml")
	writeConfigFixture(t, localPath, "schema_version = 1\nenvironment = 'dev'\ncli_key = 'local-key'\nproject = 'local-project'\n")
	writeConfigFixture(t, filepath.Join(working, ".hookspot", "stage", "config.toml"), "schema_version = 1\nenvironment = 'stage'\nproject = 'stage-project'\n")

	store, err := New(Options{Environment: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	wantLocalPath := canonicalTestPath(t, localPath)
	if store.Path() != wantLocalPath {
		t.Fatalf("Path() = %q, want %q", store.Path(), wantLocalPath)
	}
	cfg, err := store.Resolve(Overrides{NeedProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CLIKey != "local-key" || cfg.Project != "local-project" {
		t.Fatalf("resolved local config = %+v", cfg)
	}
}

func TestNewConfigPathOverridesWinOverLocalConfig(t *testing.T) {
	clearConfigEnvironment(t)
	setIsolatedHome(t)
	working := t.TempDir()
	setWorkingDirectory(t, working)
	writeConfigFixture(t, filepath.Join(working, ".hookspot", "dev", "config.toml"), "schema_version = 1\nenvironment = 'dev'\nproject = 'local-project'\n")

	explicit := filepath.Join(t.TempDir(), "explicit.toml")
	writeConfigFixture(t, explicit, "schema_version = 1\nenvironment = 'dev'\nproject = 'explicit-project'\n")
	store, err := New(Options{Environment: "dev", ExplicitPath: explicit, ExplicitPathSet: true})
	if err != nil {
		t.Fatal(err)
	}
	wantExplicit := canonicalTestPath(t, explicit)
	if store.Path() != wantExplicit {
		t.Fatalf("explicit Path() = %q, want %q", store.Path(), wantExplicit)
	}

	scoped := filepath.Join(t.TempDir(), "scoped.toml")
	writeConfigFixture(t, scoped, "schema_version = 1\nenvironment = 'dev'\nproject = 'scoped-project'\n")
	t.Setenv("HOOKSPOT_DEV_CONFIG_FILE", scoped)
	store, err = New(Options{Environment: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	wantScoped := canonicalTestPath(t, scoped)
	if store.Path() != wantScoped {
		t.Fatalf("scoped Path() = %q, want %q", store.Path(), wantScoped)
	}
}

func TestNewRejectsMalformedLocalConfigInsteadOfFallingBack(t *testing.T) {
	clearConfigEnvironment(t)
	home := setIsolatedHome(t)
	working := t.TempDir()
	setWorkingDirectory(t, working)
	writeConfigFixture(t, filepath.Join(home, ".config", "hookspot", "dev", "config.toml"), "schema_version = 1\nenvironment = 'dev'\ncli_key = 'global-key'\n")
	writeConfigFixture(t, filepath.Join(working, ".hookspot", "dev", "config.toml"), "schema_version = [")

	if _, err := New(Options{Environment: "dev"}); err == nil || !strings.Contains(err.Error(), "parse config file") {
		t.Fatalf("New() error = %v, want malformed local config error", err)
	}
}

func TestNewLocalCopiesOnlyPersistedGlobalKey(t *testing.T) {
	clearConfigEnvironment(t)
	home := setIsolatedHome(t)
	working := t.TempDir()
	setWorkingDirectory(t, working)
	globalPath := filepath.Join(home, ".config", "hookspot", "prod", "config.toml")
	writeConfigFixture(t, globalPath, "schema_version = 1\nenvironment = 'prod'\ncli_key = 'persisted-key'\nproject = 'global-project'\n")
	t.Setenv("HOOKSPOT_PROD_CLI_KEY", "ephemeral-key")

	store, err := New(Options{Environment: "prod", Local: true})
	if err != nil {
		t.Fatal(err)
	}
	effectiveWorking, err := filepath.EvalSymlinks(working)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := store.Path(), filepath.Join(effectiveWorking, ".hookspot", "prod", "config.toml"); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
	cfg, err := store.Resolve(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CLIKey != "ephemeral-key" {
		t.Fatalf("resolved CLI key = %q, want environment key", cfg.CLIKey)
	}
	if store.SavedProject() != "global-project" {
		t.Fatalf("saved project default = %q, want global-project", store.SavedProject())
	}
	if err := store.SaveProject("local-project"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if !strings.Contains(text, "persisted-key") || !strings.Contains(text, "local-project") || strings.Contains(text, "ephemeral-key") || strings.Contains(text, "global-project") {
		t.Fatalf("unexpected local config:\n%s", text)
	}
	global, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(global), "global-project") || strings.Contains(string(global), "local-project") {
		t.Fatalf("global config changed:\n%s", global)
	}
}

func TestNewLocalPreservesExistingLocalKey(t *testing.T) {
	clearConfigEnvironment(t)
	setIsolatedHome(t)
	working := t.TempDir()
	setWorkingDirectory(t, working)
	localPath := filepath.Join(working, ".hookspot", "stage", "config.toml")
	writeConfigFixture(t, localPath, "schema_version = 1\nenvironment = 'stage'\ncli_key = 'local-key'\nproject = 'old-project'\n")

	store, err := New(Options{Environment: "stage", Local: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProject("new-project"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "local-key") || !strings.Contains(string(contents), "new-project") {
		t.Fatalf("existing local record was not preserved:\n%s", contents)
	}
}

func TestNewLocalRejectsConfigPathOverrides(t *testing.T) {
	for _, test := range []struct {
		name        string
		options     Options
		environment map[string]string
	}{
		{name: "explicit", options: Options{ExplicitPath: "ignored.toml", ExplicitPathSet: true}},
		{name: "scoped", environment: map[string]string{"HOOKSPOT_DEV_CONFIG_FILE": "ignored.toml"}},
		{name: "legacy", environment: map[string]string{"HOOKSPOT_CONFIG_FILE": "ignored.toml", "HOOKSPOT_ENVIRONMENT": "dev"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnvironment(t)
			setIsolatedHome(t)
			for key, value := range test.environment {
				t.Setenv(key, value)
			}
			test.options.Environment = "dev"
			test.options.Local = true
			if _, err := New(test.options); err == nil || !strings.Contains(err.Error(), "--local") {
				t.Fatalf("conflict error = %v", err)
			}
		})
	}
}

func TestNewDoesNotSearchParentDirectoriesForLocalConfig(t *testing.T) {
	clearConfigEnvironment(t)
	home := setIsolatedHome(t)
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	writeConfigFixture(t, filepath.Join(parent, ".hookspot", "dev", "config.toml"), "schema_version = 1\nenvironment = 'dev'\nproject = 'parent-project'\n")
	globalPath := filepath.Join(home, ".config", "hookspot", "dev", "config.toml")
	writeConfigFixture(t, globalPath, "schema_version = 1\nenvironment = 'dev'\nproject = 'global-project'\n")
	setWorkingDirectory(t, child)

	store, err := New(Options{Environment: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if store.Path() != canonicalTestPath(t, globalPath) || store.SavedProject() != "global-project" {
		t.Fatalf("selected store path = %q, project = %q", store.Path(), store.SavedProject())
	}
}

func TestNewLocalUsesIndependentEnvironmentPaths(t *testing.T) {
	clearConfigEnvironment(t)
	setIsolatedHome(t)
	working := t.TempDir()
	setWorkingDirectory(t, working)

	for _, environment := range []string{"dev", "stage", "prod"} {
		store, err := New(Options{Environment: environment, Local: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SaveProject(environment + "-project"); err != nil {
			t.Fatal(err)
		}
	}
	for _, environment := range []string{"dev", "stage", "prod"} {
		path := filepath.Join(working, ".hookspot", environment, "config.toml")
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(contents), "environment = '"+environment+"'") || !strings.Contains(string(contents), "project = '"+environment+"-project'") {
			t.Fatalf("unexpected %s local config:\n%s", environment, contents)
		}
	}
}

func TestNewLocalDoesNotOverwriteConcurrentDestination(t *testing.T) {
	clearConfigEnvironment(t)
	setIsolatedHome(t)
	working := t.TempDir()
	setWorkingDirectory(t, working)

	store, err := New(Options{Environment: "dev", Local: true})
	if err != nil {
		t.Fatal(err)
	}
	writeConfigFixture(t, store.Path(), "schema_version = 1\nenvironment = 'dev'\ncli_key = 'concurrent-key'\n")
	if err := store.SaveProject("selected-project"); err == nil {
		t.Fatal("SaveProject overwrote a concurrently created local config")
	}
	contents, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "concurrent-key") || strings.Contains(string(contents), "selected-project") {
		t.Fatalf("concurrent config changed:\n%s", contents)
	}
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
