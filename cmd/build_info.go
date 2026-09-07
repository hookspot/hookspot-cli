package cmd

import (
	"encoding/hex"
	"fmt"
	"runtime"
	"time"

	"hookspot/internal/endpoint"
)

var (
	version          = "dev"
	serverURL        string
	buildEnvironment = "dev"
	commit           = "unknown"
	sourceDate       = "unknown"
	buildKind        = "dev"
)

// BuildInfo is an immutable snapshot of linker-provided binary identity.
type BuildInfo struct {
	Version     string `json:"version"`
	Environment string `json:"environment"`
	Commit      string `json:"commit"`
	SourceDate  string `json:"source_date"`
	BuildKind   string `json:"build_kind"`
	GoVersion   string `json:"go_version"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	ServerURL   string `json:"server_url"`
}

// CurrentBuildInfo returns a copy of the binary's build identity.
func CurrentBuildInfo() BuildInfo {
	publicServerURL := ""
	if base, err := endpoint.Parse(serverURL, buildEnvironment); err == nil {
		publicServerURL = base.String()
	}
	return BuildInfo{
		Version: version, Environment: buildEnvironment, Commit: commit,
		SourceDate: sourceDate, BuildKind: buildKind, GoVersion: runtime.Version(),
		OS: runtime.GOOS, Arch: runtime.GOARCH, ServerURL: publicServerURL,
	}
}

func (info BuildInfo) networkEndpoint() (endpoint.Base, error) {
	switch info.Environment {
	case "dev":
		base, err := endpoint.Parse(info.ServerURL, "dev")
		if err != nil {
			return endpoint.Base{}, newCommandError(
				"development build has no valid Hookspot server URL",
				"Rebuild with SERVER_URL set to the development server.",
			)
		}
		return base, nil
	case "stage", "prod":
		if !info.validDistributionMetadata() {
			return endpoint.Base{}, newCommandError(
				"invalid "+info.Environment+" build metadata",
				"Uninstall this binary and install the correct "+info.Environment+" release.",
			)
		}
		base, err := endpoint.Parse(info.ServerURL, info.Environment)
		if err != nil {
			return endpoint.Base{}, newCommandError(
				"invalid "+info.Environment+" release endpoint",
				"Uninstall this binary and install the correct "+info.Environment+" release.",
			)
		}
		return base, nil
	default:
		return endpoint.Base{}, newCommandError(
			fmt.Sprintf("unknown build environment %q", info.Environment),
			"Install an official Hookspot release.",
		)
	}
}

func (info BuildInfo) validDistributionMetadata() bool {
	if info.Version == "" || info.Version == "dev" || info.BuildKind != "release" && info.BuildKind != "snapshot" {
		return false
	}
	decoded, err := hex.DecodeString(info.Commit)
	if err != nil || len(decoded) != 20 {
		return false
	}
	_, err = time.Parse(time.RFC3339, info.SourceDate)
	return err == nil
}
