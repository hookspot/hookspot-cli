package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChannelUpgradeCommand(t *testing.T) {
	tests := []struct {
		executable string
		docker     bool
		want       string
	}{
		{"/opt/homebrew/Cellar/hookspot-cli/1.2.3/bin/hookspot", false, "brew upgrade hookspot-cli"},
		{`C:\Users\dev\AppData\Roaming\npm\node_modules\@hookspot\cli\binaries\windows-amd64\hookspot.exe`, false, "npm install -g @hookspot/cli@latest"},
		// A Node image installs through npm.
		{"/usr/local/lib/node_modules/@hookspot/cli/binaries/linux-arm64/hookspot", true, "npm install -g @hookspot/cli@latest"},
		{"/usr/local/bin/hookspot", true, "docker pull hookspot/cli"},
		{"/usr/local/bin/hookspot", false, upgradeDocsURL},
	}
	for _, test := range tests {
		if got := channelUpgradeCommand(test.executable, test.docker); got != test.want {
			t.Errorf("channelUpgradeCommand(%q, %v) = %q, want %q", test.executable, test.docker, got, test.want)
		}
	}
}

// TestUpgradeCommandReadsTheDockerMarker covers the running binary, which no
// package manager installed.
func TestUpgradeCommandReadsTheDockerMarker(t *testing.T) {
	original := dockerEnvPath
	t.Cleanup(func() { dockerEnvPath = original })
	dockerEnvPath = filepath.Join(t.TempDir(), ".dockerenv")
	if got := upgradeCommand(); got != upgradeDocsURL {
		t.Fatalf("upgradeCommand() without the marker = %q", got)
	}
	if err := os.WriteFile(dockerEnvPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := upgradeCommand(); got != "docker pull hookspot/cli" {
		t.Fatalf("upgradeCommand() with the marker = %q", got)
	}
}
