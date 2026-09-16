package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"

	"hookspot/internal/printer"
)

var versionJSON bool

var versionCmd = &cobra.Command{
	Use:     "version",
	Args:    cobra.NoArgs,
	Short:   "Get the version of the Hookspot CLI",
	Long:    "Print the CLI version and check whether a new version is available.",
	Example: "  $ hookspot version",
	RunE: func(cmd *cobra.Command, args []string) error {
		info := CurrentBuildInfo()
		out := cmd.OutOrStdout()
		if versionJSON {
			return json.NewEncoder(out).Encode(info)
		}
		if _, err := fmt.Fprintf(out, "%s version %s\n", cmd.Root().Name(), info.Version); err != nil {
			return err
		}
		checkLatestVersion(cmd.Context(), out, info.Version)
		return nil
	},
}

// githubAPIBaseURL is the GitHub REST API root. It is a variable so tests can
// point it at an httptest server.
var githubAPIBaseURL = "https://api.github.com"

// latestReleaseTimeout bounds the upgrade check so an offline machine never
// makes `version` hang.
const latestReleaseTimeout = 5 * time.Second

// checkLatestVersion asks GitHub for the newest published release and prints
// an upgrade notice when it is newer than the running binary. Development
// builds are never compared, and failures are silently ignored: the upgrade
// hint must never break the command the user ran.
func checkLatestVersion(ctx context.Context, out io.Writer, current string) {
	if current == "dev" {
		return
	}
	latest := latestVersion(ctx, current)
	if !needsToUpgrade(current, latest) {
		return
	}
	notice := "A newer version of the Hookspot CLI is available, please update to: " + latest
	if printer.SupportsColor(out) {
		notice = "\x1b[3m" + notice + "\x1b[0m"
	}
	fmt.Fprintln(out, notice)
}

func latestVersion(ctx context.Context, current string) string {
	ctx, cancel := context.WithTimeout(ctx, latestReleaseTimeout)
	defer cancel()

	url := githubAPIBaseURL + "/repos/hookspot/hookspot-cli/releases/latest"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "hookspot-cli/"+current)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release); err != nil {
		return ""
	}
	return release.TagName
}

// needsToUpgrade reports whether latest is newer than current. Neither
// argument needs a "v" prefix; an empty or malformed latest never upgrades.
func needsToUpgrade(current, latest string) bool {
	return semver.Compare("v"+strings.TrimPrefix(latest, "v"), "v"+strings.TrimPrefix(current, "v")) > 0
}

func init() {
	versionCmd.Flags().BoolVar(&versionJSON, "json", false, "print stable JSON build information")
	rootCmd.AddCommand(versionCmd)
}
