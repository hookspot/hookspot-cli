//go:build !windows

package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestConfigWritingCommandsStopBeforeAPIUnderUnsafeFolder(t *testing.T) {
	credentials := map[string]string{"HOOKSPOT_CLI_KEY": "test-key"}
	for _, test := range []struct {
		name        string
		input       string
		environment map[string]string
		args        []string
	}{
		{name: "browser login", args: []string{"login"}},
		{name: "flag key login", args: []string{"--cli-key", "flag-key", "login"}},
		{name: "interactive key login", input: "typed-key\n", args: []string{"login", "-i"}},
		{name: "project use", environment: credentials, args: []string{"project", "use"}},
		{name: "local project use", environment: credentials, args: []string{"project", "use", "--local"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Umask 002 leaves home group-writable; it holds both the global and the local config.
			home := t.TempDir()
			if err := os.Chmod(home, 0o775); err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			environment := map[string]string{"HOME": home, "USERPROFILE": home}
			for key, value := range test.environment {
				environment[key] = value
			}

			result := runCommandProcessDirectoryEnvironment(t, home, test.input, developmentMetadata(server.URL), environment, test.args...)
			if result.err == nil || !strings.Contains(result.stderr, "permissions 775 allow untrusted mutation") {
				t.Fatalf("result = %v, stderr = %q", result.err, result.stderr)
			}
			if got := requests.Load(); got != 0 {
				t.Fatalf("API requests = %d, want 0", got)
			}
			if result.stdout != "" {
				t.Fatalf("stdout = %q, want no prompt", result.stdout)
			}
			if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
				t.Fatalf("home entries = %v, %v; want none", entries, err)
			}
		})
	}
}
