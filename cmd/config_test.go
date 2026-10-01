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
		"version": "dev", "server_url": serverURL,
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
	if !strings.Contains(string(contents), "schema_version = 1") {
		t.Fatalf("new config is not marked: %s", contents)
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
	environment := map[string]string{"HOOKSPOT_ORGANIZATION_SLUG": "incomplete"}

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
			if err := writeCommandFixture(path, []byte("schema_version = 1\ncli_key = 'stored-key'\n")); err != nil {
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
			name: "no environment key",
		},
		{
			name: "CLI flag does not hide environment key",
			environment: map[string]string{
				"HOOKSPOT_CLI_KEY": "environment-key",
			},
			args:        []string{"--cli-key", "flag-key"},
			wantWarning: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := writeCommandFixture(path, []byte("schema_version = 1\ncli_key = 'stored-key'\nproject = 'project_1'\n")); err != nil {
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
