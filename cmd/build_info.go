package cmd

import (
	"encoding/hex"
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
	if info.Environment == "prod" && !info.validDistributionMetadata() {
		return endpoint.Base{}, newCommandError("invalid prod build metadata", releaseInstallHint)
	}
	base, err := endpoint.Parse(info.ServerURL, info.Environment)
	if err != nil {
		return endpoint.Base{}, wrapCommandError("resolve server endpoint", endpointHint(info.Environment), err)
	}
	return base, nil
}

const releaseInstallHint = "Uninstall this binary and install the correct prod release."

func endpointHint(environment string) string {
	switch environment {
	case "dev":
		return "Rebuild with SERVER_URL set to the development server."
	case "prod":
		return releaseInstallHint
	}
	return "Install an official Hookspot release."
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
