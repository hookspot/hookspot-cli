package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func developmentMetadata(serverURL string) map[string]string {
	return map[string]string{
		"version": "dev", "server_url": serverURL, "environment": "dev",
		"commit": "unknown", "source_date": "unknown", "build_kind": "dev",
	}
}

func TestOrdinaryCommandRejectsMissingExplicitConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	result := runCommandProcess(t, "", developmentMetadata("http://127.0.0.1:1"), "--config", path, "project", "list")
	if result.err == nil {
		t.Fatal("ordinary command accepted a missing explicit config")
	}
	if !strings.Contains(result.stderr, "selected config file does not exist") ||
		!strings.Contains(result.stderr, "hookspot --config PATH login") {
		t.Fatalf("missing recovery guidance: %s", result.stderr)
	}
}

func TestStageRecoveryGuidanceUsesStageExecutable(t *testing.T) {
	metadata := map[string]string{
		"version": "1.2.3-stage.1", "server_url": "https://stage.example.invalid", "environment": "stage",
		"commit": strings.Repeat("a", 40), "source_date": "2026-09-05T10:11:12Z", "build_kind": "release",
	}
	path := filepath.Join(t.TempDir(), "missing.toml")
	result := runCommandProcess(t, "", metadata, "--config", path, "project", "list")
	if result.err == nil || !strings.Contains(result.stderr, "hookspot-stage --config PATH login") ||
		!strings.Contains(result.stderr, "hookspot-stage config migrate") || strings.Contains(result.stderr, "'hookspot ") {
		t.Fatalf("unexpected stage guidance: %q", result.stderr)
	}
}

func TestStageMarkerlessConfigUsesOnlyStageRecoveryCommands(t *testing.T) {
	metadata := map[string]string{
		"version": "1.2.3-stage.1", "server_url": "https://stage.example.invalid", "environment": "stage",
		"commit": strings.Repeat("a", 40), "source_date": "2026-09-05T10:11:12Z", "build_kind": "release",
	}
	path := filepath.Join(t.TempDir(), "legacy.toml")
	if err := writeCommandFixture(path, []byte("cli_key = 'legacy-key'\n")); err != nil {
		t.Fatal(err)
	}
	result := runCommandProcess(t, "", metadata, "--config", path, "project", "list")
	if result.err == nil || !strings.Contains(result.stderr, "explicit migration is required") ||
		!strings.Contains(result.stderr, "hookspot-stage config migrate") || strings.Contains(result.stderr, "'hookspot ") {
		t.Fatalf("unexpected markerless stage guidance: %q", result.stderr)
	}
}

func TestLoginMayCreateMissingExplicitConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cli/me" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"uid": "user_1", "email": "user@example.invalid\nInjected\x1b"})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "new", "config.toml")
	result := runCommandProcess(t, "", developmentMetadata(server.URL), "--config", path, "--cli-key", "test-key", "login")
	if result.err != nil {
		t.Fatalf("login failed: %v\n%s", result.err, result.stderr)
	}
	if strings.Contains(result.stdout, "example.invalid\nInjected") || strings.ContainsRune(result.stdout, '\x1b') ||
		!strings.Contains(result.stdout, `example.invalid\nInjected\x1b`) {
		t.Fatalf("login output did not escape backend text: %q", result.stdout)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "schema_version = 1") || !strings.Contains(string(contents), "environment = 'dev'") {
		t.Fatalf("new config is not marked: %s", contents)
	}
}

func TestStageHelpUsesMetadataExecutableName(t *testing.T) {
	metadata := map[string]string{
		"version": "1.2.3-stage.1", "server_url": "https://stage.example.invalid", "environment": "stage",
		"commit": strings.Repeat("a", 40), "source_date": "2026-09-05T10:11:12Z", "build_kind": "release",
	}
	result := runCommandProcess(t, "", metadata, "--help")
	if result.err != nil || !strings.Contains(result.stdout, "hookspot-stage") {
		t.Fatalf("stage help = %q, %v", result.stdout, result.err)
	}
}

func TestCredentialOnlyCommandsIgnoreIncompleteProjectEnvironment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cli/me":
			_ = json.NewEncoder(w).Encode(map[string]string{"uid": "user_1", "email": "user@example.invalid"})
		case "/cli/projects":
			_ = json.NewEncoder(w).Encode([]any{})
		case "/cli/projects/project_1":
			_ = json.NewEncoder(w).Encode(map[string]any{"uid": "project_1", "name": "Project", "organization": map[string]string{"name": "Organization"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	environment := map[string]string{"HOOKSPOT_DEV_ORGANIZATION_SLUG": "incomplete"}

	for _, test := range []struct {
		name string
		args []string
	}{
		{"login", []string{"--cli-key", "test-key", "login"}},
		{"project list", []string{"--cli-key", "test-key", "project", "list"}},
		{"project use explicit UID", []string{"--cli-key", "test-key", "project", "use", "project_1"}},
		{"logout", []string{"logout"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := writeCommandFixture(path, []byte("schema_version = 1\nenvironment = 'dev'\ncli_key = 'stored-key'\n")); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"--config", path}, test.args...)
			result := runCommandProcessEnvironment(t, "", developmentMetadata(server.URL), environment, args...)
			if result.err != nil {
				t.Fatalf("command failed: %v\n%s", result.err, result.stderr)
			}
		})
	}
}

func TestLogoutClearsSavedKeyWithoutCredentialResolution(t *testing.T) {
	for _, test := range []struct {
		name        string
		environment map[string]string
		args        []string
		wantWarning bool
	}{
		{
			name: "invalid unused generic assertion",
			environment: map[string]string{
				"HOOKSPOT_CLI_KEY": "generic-key", "HOOKSPOT_ENVIRONMENT": "prod",
			},
		},
		{
			name: "CLI flag does not hide scoped environment key",
			environment: map[string]string{
				"HOOKSPOT_DEV_CLI_KEY": "scoped-key",
			},
			args:        []string{"--cli-key", "flag-key"},
			wantWarning: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := writeCommandFixture(path, []byte("schema_version = 1\nenvironment = 'dev'\ncli_key = 'stored-key'\nproject = 'project_1'\n")); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"--config", path}, test.args...)
			args = append(args, "logout")
			result := runCommandProcessEnvironment(t, "", developmentMetadata(""), test.environment, args...)
			if result.err != nil {
				t.Fatalf("logout failed: %v\n%s", result.err, result.stderr)
			}
			if got := strings.Contains(result.stdout, "environment-provided CLI key"); got != test.wantWarning {
				t.Fatalf("warning = %v, output = %q", got, result.stdout)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(contents), "stored-key") || !strings.Contains(string(contents), "project_1") {
				t.Fatalf("unexpected config after logout: %s", contents)
			}
		})
	}
}

func TestConfigMigrateValidatesAndImportsOnce(t *testing.T) {
	requestPaths := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPaths = append(requestPaths, r.URL.Path)
		switch r.URL.Path {
		case "/cli/me":
			_ = json.NewEncoder(w).Encode(map[string]string{"uid": "user_1", "email": "user@example.invalid"})
		case "/cli/projects/project_1":
			_ = json.NewEncoder(w).Encode(map[string]any{"uid": "project_1", "name": "Project", "organization": map[string]string{"name": "Organization"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	source := filepath.Join(dir, "legacy.toml")
	destination := filepath.Join(dir, "new", "config.toml")
	original := "cli_key = 'legacy-key'\nproject = 'project_1'\nlog_level = 'obsolete'\n"
	if err := writeCommandFixture(source, []byte(original)); err != nil {
		t.Fatal(err)
	}
	result := runCommandProcessEnvironment(t, "", developmentMetadata(server.URL), map[string]string{
		"HOOKSPOT_DEV_CLI_KEY": "ignored-key",
	}, "--config", destination, "config", "migrate", "--from", source, "--confirm-environment", "dev")
	if result.err != nil {
		t.Fatalf("migration failed: %v\n%s", result.err, result.stderr)
	}
	if strings.Join(requestPaths, ",") != "/cli/me,/cli/projects/project_1" {
		t.Fatalf("request paths = %v", requestPaths)
	}
	unchanged, err := os.ReadFile(source)
	if err != nil || string(unchanged) != original {
		t.Fatalf("source changed: %v %q", err, unchanged)
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if !strings.Contains(text, "legacy-key") || strings.Contains(text, "ignored-key") || !strings.Contains(text, "project_1") {
		t.Fatalf("unexpected destination: %s", text)
	}
}

func TestConfigMigratePreservesFilesOnValidationFailure(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, `{"message":"rejected"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	dir := t.TempDir()
	source := filepath.Join(dir, "legacy.toml")
	destination := filepath.Join(dir, "new.toml")
	original := "cli_key = 'legacy-key'\nproject = 'project_1'\n"
	if err := writeCommandFixture(source, []byte(original)); err != nil {
		t.Fatal(err)
	}
	result := runCommandProcess(t, "", developmentMetadata(server.URL), "--config", destination, "config", "migrate", "--from", source, "--confirm-environment", "dev")
	if result.err == nil || requests != 1 {
		t.Fatalf("result = %v, requests = %d", result.err, requests)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after rejection: %v", err)
	}
	contents, err := os.ReadFile(source)
	if err != nil || string(contents) != original {
		t.Fatalf("source changed: %v %q", err, contents)
	}
}

func TestConfigMigrateDoesNotOverwriteDestinationCreatedDuringValidation(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "legacy.toml")
	destination := filepath.Join(dir, "destination.toml")
	original := "cli_key = 'legacy-key'\nproject = 'project_1'\n"
	concurrent := "schema_version = 1\nenvironment = 'dev'\ncli_key = 'concurrent-key'\n"
	if err := writeCommandFixture(source, []byte(original)); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/cli/me" {
			_ = json.NewEncoder(w).Encode(map[string]string{"uid": "user_1", "email": "user@example.invalid"})
			return
		}
		if err := writeCommandFixture(destination, []byte(concurrent)); err != nil {
			t.Errorf("create concurrent destination: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"uid": "project_1", "name": "Project", "organization": map[string]string{"name": "Organization"}})
	}))
	defer server.Close()

	result := runCommandProcess(t, "", developmentMetadata(server.URL), "--config", destination, "config", "migrate", "--from", source, "--confirm-environment", "dev")
	if result.err == nil || requests != 2 || !strings.Contains(result.stderr, "never overwritten") {
		t.Fatalf("result = %v, requests = %d, stderr = %q", result.err, requests, result.stderr)
	}
	contents, err := os.ReadFile(destination)
	if err != nil || string(contents) != concurrent {
		t.Fatalf("concurrent destination changed: %v %q", err, contents)
	}
}

func TestConfigMigrateRejectsExistingDestinationBeforeReadingOrNetwork(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	defer server.Close()
	dir := t.TempDir()
	source := filepath.Join(dir, "legacy.toml")
	destination := filepath.Join(dir, "existing.toml")
	sourceContents := "cli_key = 'legacy-key'\nproject = 'project_1'\n"
	destinationContents := "cli_key = 'other-key'\nproject = 'other-project'\n"
	if err := writeCommandFixture(source, []byte(sourceContents)); err != nil {
		t.Fatal(err)
	}
	if err := writeCommandFixture(destination, []byte(destinationContents)); err != nil {
		t.Fatal(err)
	}
	result := runCommandProcess(t, "", developmentMetadata(server.URL), "--config", destination, "config", "migrate", "--from", filepath.Join(dir, "missing-source.toml"), "--confirm-environment", "dev")
	if result.err == nil || requests != 0 || !strings.Contains(result.stderr, "destination already exists") {
		t.Fatalf("result = %v, requests = %d, stderr = %q", result.err, requests, result.stderr)
	}
	gotDestination, err := os.ReadFile(destination)
	if err != nil || string(gotDestination) != destinationContents {
		t.Fatalf("destination changed: %v %q", err, gotDestination)
	}
	gotSource, err := os.ReadFile(source)
	if err != nil || string(gotSource) != sourceContents {
		t.Fatalf("source changed: %v %q", err, gotSource)
	}
}

func TestConfigMigrateRequiresMatchingConfirmationBeforeReadingOrNetwork(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "destination.toml")
	result := runCommandProcess(t, "", developmentMetadata(server.URL), "--config", destination, "config", "migrate", "--from", filepath.Join(t.TempDir(), "missing.toml"), "--confirm-environment", "prod")
	if result.err == nil || requests != 0 {
		t.Fatalf("result = %v, requests = %d", result.err, requests)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists: %v", err)
	}
}
