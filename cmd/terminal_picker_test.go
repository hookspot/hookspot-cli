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

// TestTerminalProjectPicker runs project use without arguments in a terminal:
// the picker opens on the saved project, reads keys in raw mode, and leaves
// the screen and terminal mode as it found them.
func TestTerminalProjectPicker(t *testing.T) {
	tests := []struct {
		name, keys string
		// saved is the project in the config afterwards, screen all that's
		// left on the screen.
		saved, screen string
	}{
		{name: "down then enter selects", keys: "\x1b[B\r", saved: "proj_billing", screen: "Active project set to Other | Billing"},
		{name: "esc cancels", keys: "\x1b", saved: "proj_payments"},
		{name: "ctrl-c cancels", keys: "\x03", saved: "proj_payments"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/cli/projects" {
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(selectionProjects())
			}))
			defer server.Close()
			config := filepath.Join(t.TempDir(), "config.toml")
			if err := writeCommandFixture(config, []byte("schema_version = 1\ncli_key = 'key'\nproject = 'proj_payments'\n")); err != nil {
				t.Fatal(err)
			}

			run := startTerminal(t, terminalOptions{width: 80, height: 24}, developmentMetadata(server.URL), "--config", config, "project", "use")
			run.waitForText("› Acme Inc.  Payments    current")
			run.send(test.keys)
			if code := run.wait(); code != 0 {
				t.Fatalf("exit code = %d, want 0; screen:\n%s", code, run.text())
			}
			if screen := strings.TrimSpace(run.text()); screen != test.screen {
				t.Errorf("screen after exit:\n%s\nwant:\n%s", screen, test.screen)
			}
			run.requireRestored()
			contents, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(contents), "project = '"+test.saved+"'") {
				t.Errorf("config after exit:\n%s\nwant project %s", contents, test.saved)
			}
		})
	}
}
