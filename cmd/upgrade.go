package cmd

import (
	"os"
	"path/filepath"
	"strings"
)

// upgradeDocsURL covers the installs no command upgrades, such as an archive.
const upgradeDocsURL = "https://hookspot.dev/docs/cli#upgrading"

// dockerEnvPath marks a Docker container; tests turn it off.
var dockerEnvPath = "/.dockerenv"

// upgradeCommand is how the channel that installed this binary upgrades it.
// Upgrading through another channel leaves two binaries shadowing each other.
func upgradeCommand() string {
	executable, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	_, err := os.Stat(dockerEnvPath)
	return channelUpgradeCommand(executable, err == nil)
}

// channelUpgradeCommand tells the channel from the resolved executable path.
// The image's binary is in no package manager's tree, so a container is
// Docker only when the path names neither.
func channelUpgradeCommand(executable string, docker bool) string {
	switch {
	case strings.Contains(executable, "/Cellar/"):
		return "brew upgrade hookspot-cli"
	case strings.Contains(executable, "node_modules"):
		return "npm install -g @hookspot/cli@latest"
	case docker:
		return "docker pull hookspot/cli"
	}
	return upgradeDocsURL
}
