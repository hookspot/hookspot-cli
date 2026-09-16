package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

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
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return ""
	}
	return release.TagName
}

func needsToUpgrade(current, latest string) bool {
	if latest == "" {
		return false
	}
	current = strings.TrimPrefix(current, "v")
	latest = strings.TrimPrefix(latest, "v")

	// Never suggest moving from a GA release to a pre-release.
	if !strings.Contains(current, "-") && strings.Contains(latest, "-") {
		return false
	}
	return semverGreater(latest, current)
}

// semverGreater reports whether a is semantically greater than b. Neither
// argument may carry a "v" prefix.
func semverGreater(a, b string) bool {
	aNums, aPre := parseVersion(a)
	bNums, bPre := parseVersion(b)

	for i := range max(len(aNums), len(bNums)) {
		var av, bv int
		if i < len(aNums) {
			av = aNums[i]
		}
		if i < len(bNums) {
			bv = bNums[i]
		}
		if av != bv {
			return av > bv
		}
	}

	// Same base version: GA beats any pre-release.
	if aPre == "" && bPre != "" {
		return true
	}
	if aPre != "" && bPre == "" {
		return false
	}
	return comparePreRelease(aPre, bPre) > 0
}

// parseVersion splits a version without its "v" prefix into numeric
// components and an optional pre-release identifier.
func parseVersion(v string) (nums []int, pre string) {
	parts := strings.SplitN(v, "-", 2)
	if len(parts) > 1 {
		pre = parts[1]
	}
	for _, s := range strings.Split(parts[0], ".") {
		n, _ := strconv.Atoi(s)
		nums = append(nums, n)
	}
	return nums, pre
}

// comparePreRelease compares two pre-release identifiers dot by dot. Numeric
// parts compare numerically and everything else lexically.
func comparePreRelease(a, b string) int {
	if a == b {
		return 0
	}
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	for i := range max(len(aParts), len(bParts)) {
		if i >= len(aParts) {
			return -1
		}
		if i >= len(bParts) {
			return 1
		}
		aN, aErr := strconv.Atoi(aParts[i])
		bN, bErr := strconv.Atoi(bParts[i])
		if aErr == nil && bErr == nil {
			if aN != bN {
				if aN > bN {
					return 1
				}
				return -1
			}
			continue
		}
		if aParts[i] > bParts[i] {
			return 1
		}
		if aParts[i] < bParts[i] {
			return -1
		}
	}
	return 0
}

func init() {
	versionCmd.Flags().BoolVar(&versionJSON, "json", false, "print stable JSON build information")
	rootCmd.AddCommand(versionCmd)
}
