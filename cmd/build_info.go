package cmd

import (
	"runtime"

	"hookspot/internal/endpoint"
)

var (
	version      = "dev"
	serverURL    string
	configPrefix = "dev"
	commit       = "unknown"
	sourceDate   = "unknown"
	buildKind    = "dev"
)

// BuildInfo is an immutable snapshot of linker-provided binary identity.
type BuildInfo struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	SourceDate string `json:"source_date"`
	BuildKind  string `json:"build_kind"`
	GoVersion  string `json:"go_version"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	ServerURL  string `json:"server_url"`
}

// CurrentBuildInfo returns a copy of the binary's build identity.
func CurrentBuildInfo() BuildInfo {
	publicServerURL := ""
	if base, err := endpoint.Parse(serverURL); err == nil {
		publicServerURL = base.String()
	}
	return BuildInfo{
		Version: version, Commit: commit,
		SourceDate: sourceDate, BuildKind: buildKind, GoVersion: runtime.Version(),
		OS: runtime.GOOS, Arch: runtime.GOARCH, ServerURL: publicServerURL,
	}
}

// userAgent names this release on every call to the Hookspot server, which
// refuses releases below its minimum CLI version.
func userAgent() string {
	return "hookspot-cli/" + version + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
}

func (info BuildInfo) networkEndpoint() (endpoint.Base, error) {
	base, err := endpoint.Parse(info.ServerURL)
	if err != nil {
		return endpoint.Base{}, wrapCommandError("resolve server endpoint", "Rebuild with SERVER_URL set to the Hookspot server.", err)
	}
	return base, nil
}
