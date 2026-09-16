package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"hookspot/internal/endpoint"
)

const buildEnvironment = "prod"

var (
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// releaseVersionPattern is semver without build metadata; the pre-release
	// suffix covers tags such as v1.2.3-rc.1.
	releaseVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	snapshotPattern       = regexp.MustCompile(`^0\.0\.0-snapshot\.[0-9a-f]{7,40}$`)
)

type buildMetadata struct {
	Version     string `json:"version"`
	Environment string `json:"environment"`
	ServerURL   string `json:"server_url"`
	Commit      string `json:"commit"`
	SourceDate  string `json:"source_date"`
	BuildKind   string `json:"build_kind"`
	GoVersion   string `json:"go_version"`
}

func runMetadata(args []string) error {
	flags := flag.NewFlagSet("releasecheck metadata", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	version := flags.String("version", "", "resolved release version")
	commit := flags.String("commit", "", "source commit")
	sourceDate := flags.String("source-date", "", "source commit time")
	kind := flags.String("kind", "", "build kind")
	serverURL := flags.String("server-url", "", "production server URL")
	output := flags.String("output", "", "metadata output path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid metadata arguments")
	}

	base, err := endpoint.Parse(*serverURL, buildEnvironment)
	if err != nil {
		return fmt.Errorf("metadata server URL: %w", err)
	}
	if err := validateBuildIdentity(*version, *commit, *sourceDate, *kind); err != nil {
		return err
	}
	if *output == "" {
		return errors.New("metadata output path is required")
	}

	metadata := buildMetadata{
		Version:     *version,
		Environment: buildEnvironment,
		ServerURL:   base.String(),
		Commit:      *commit,
		SourceDate:  *sourceDate,
		BuildKind:   *kind,
		GoVersion:   runtime.Version(),
	}
	contents, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	// Unlink first so a symlink planted at the output path (which git ignores)
	// is replaced rather than written through.
	if err := os.Remove(*output); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale build metadata: %w", err)
	}
	if err := os.WriteFile(*output, append(contents, '\n'), 0o644); err != nil {
		return fmt.Errorf("write build metadata: %w", err)
	}
	return nil
}

func validateBuildIdentity(version, commit, sourceDate, kind string) error {
	if !commitPattern.MatchString(commit) {
		return errors.New("metadata commit must be a full lowercase Git object ID")
	}
	if _, err := time.Parse(time.RFC3339, sourceDate); err != nil {
		return errors.New("metadata source date must be RFC3339")
	}
	switch kind {
	case "snapshot":
		if !snapshotPattern.MatchString(version) {
			return errors.New("metadata snapshot version is invalid")
		}
		suffix := strings.TrimPrefix(version, "0.0.0-snapshot.")
		if !strings.HasPrefix(commit, suffix) {
			return errors.New("metadata snapshot version does not match its source commit")
		}
	case "release":
		if !releaseVersionPattern.MatchString(version) {
			return errors.New("metadata release version must be semver without build metadata")
		}
	default:
		return errors.New("metadata build kind must be snapshot or release")
	}
	return nil
}
