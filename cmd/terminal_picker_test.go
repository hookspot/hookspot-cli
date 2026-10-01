package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTerminalProjectPicker runs project use without arguments in a terminal:
// the picker opens on the saved project, reads keys in raw mode, and leaves
// the screen and terminal mode as it found them.
func TestTerminalProjectPicker(t *testing.T) {
	// start runs project use with three projects, proj_payments saved.
	start := func(t *testing.T, options terminalOptions) (*terminalRun, string) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/cli/projects" {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(selectionProjects())
		}))
		t.Cleanup(server.Close)
		config := filepath.Join(t.TempDir(), "config.toml")
		if err := writeCommandFixture(config, []byte("schema_version = 1\ncli_key = 'key'\nproject = 'proj_payments'\n")); err != nil {
			t.Fatal(err)
		}
		return startTerminal(t, options, developmentMetadata(server.URL), "--config", config, "project", "use"), config
	}
	requireSaved := func(t *testing.T, config, project string) {
		t.Helper()
		contents, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(contents), "project = '"+project+"'") {
			t.Errorf("config after exit:\n%s\nwant project %s", contents, project)
		}
	}

	tests := []struct {
		name, keys string
		// saved is the project in the config afterwards, screen all that's
		// left on the screen.
		saved, screen string
	}{
		{name: "down then enter selects", keys: "\x1b[B\r", saved: "proj_billing", screen: "✓ Active project set to Other | Billing"},
		{name: "esc cancels", keys: "\x1b", saved: "proj_payments"},
		{name: "ctrl-c cancels", keys: "\x03", saved: "proj_payments"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run, config := start(t, terminalOptions{width: 80, height: 24})
			run.waitForText("› Acme Inc.  Payments    current")
			run.send(test.keys)
			if code := run.wait(); code != 0 {
				t.Fatalf("exit code = %d, want 0; screen:\n%s", code, run.text())
			}
			if screen := strings.TrimSpace(run.text()); screen != test.screen {
				t.Errorf("screen after exit:\n%s\nwant:\n%s", screen, test.screen)
			}
			run.requireRestored()
			requireSaved(t, config, test.saved)
		})
	}

	t.Run("stdout piped", func(t *testing.T) {
		run, config := start(t, terminalOptions{width: 100, height: 24, pipeStdout: true})
		if code := run.wait(); code != 1 {
			t.Fatalf("exit code = %d, want 1; screen:\n%s", code, run.text())
		}
		// stderr is still the terminal, so the error is a box.
		errorBox := regexp.MustCompile(`(?m)^╭─ ✗ Error ─+╮\n│ project selection requires an interactive terminal; pass ORGANIZATION PROJECT or PROJECT_UID +│$`)
		if screen := run.text(); !errorBox.MatchString(screen) || run.piped() != "" {
			t.Errorf("screen:\n%s\nstdout: %q", screen, run.piped())
		}
		requireSaved(t, config, "proj_payments")
	})
}
