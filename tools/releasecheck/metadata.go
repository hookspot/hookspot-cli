package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"hookspot/internal/endpoint"
)

const maxToolchainLockBytes = 64 * 1024

var (
	commitPattern       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	prodVersionPattern  = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	stageVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-stage\.[1-9][0-9]*$`)
	snapshotPattern     = regexp.MustCompile(`^0\.0\.0-snapshot\.[0-9a-f]{7,40}$`)
	toolVersionPattern  = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	toolchainLine       = regexp.MustCompile(`^[A-Z][A-Z0-9_]*=[^[:space:]]+$`)
)

type buildMetadata struct {
	SchemaVersion     int           `json:"schema_version"`
	GoreleaserVersion string        `json:"goreleaser_version"`
	Version           string        `json:"version"`
	Environment       string        `json:"environment"`
	ServerURL         string        `json:"server_url"`
	Commit            string        `json:"commit"`
	SourceDate        string        `json:"source_date"`
	BuildKind         string        `json:"build_kind"`
	GoVersion         string        `json:"go_version"`
	Targets           []buildTarget `json:"targets"`
}

type buildTarget struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

var releaseTargets = []buildTarget{
	{OS: "darwin", Arch: "amd64"},
	{OS: "darwin", Arch: "arm64"},
	{OS: "linux", Arch: "amd64"},
	{OS: "linux", Arch: "arm64"},
	{OS: "windows", Arch: "amd64"},
	{OS: "windows", Arch: "arm64"},
}

func runMetadata(args []string) error {
	flags := flag.NewFlagSet("releasecheck metadata", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	version := flags.String("version", "", "resolved release version")
	commit := flags.String("commit", "", "source commit")
	sourceDate := flags.String("source-date", "", "source commit time")
	kind := flags.String("kind", "", "build kind")
	output := flags.String("output", "", "metadata output path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid metadata arguments")
	}

	environment := os.Getenv("RELEASE_ENV")
	serverURL := os.Getenv("SERVER_URL")
	manifest, err := loadEnvironmentManifest("release/environments.json")
	if err != nil {
		return err
	}
	if err := checkEnvironment(manifest, environment, serverURL); err != nil {
		return err
	}
	if err := validateBuildIdentity(environment, *version, *commit, *sourceDate, *kind); err != nil {
		return err
	}
	if *output == "" || !filepath.IsAbs(*output) {
		return errors.New("metadata output must be an absolute path")
	}
	goreleaserVersion, err := readGoreleaserVersion("release/toolchain.env")
	if err != nil {
		return err
	}

	base, err := endpoint.Parse(serverURL, environment)
	if err != nil {
		return errors.New("selected environment URL is invalid")
	}
	metadata := buildMetadata{
		SchemaVersion:     1,
		GoreleaserVersion: goreleaserVersion,
		Version:           *version,
		Environment:       environment,
		ServerURL:         base.String(),
		Commit:            *commit,
		SourceDate:        *sourceDate,
		BuildKind:         *kind,
		GoVersion:         runtime.Version(),
		Targets:           append([]buildTarget(nil), releaseTargets...),
	}
	return writeBuildMetadata(*output, metadata)
}

func validateBuildIdentity(environment, version, commit, sourceDate, kind string) error {
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
		valid := environment == "stage" && stageVersionPattern.MatchString(version) ||
			environment == "prod" && prodVersionPattern.MatchString(version)
		if !valid {
			return errors.New("metadata release version does not match its environment")
		}
	default:
		return errors.New("metadata build kind must be snapshot or release")
	}
	return nil
}

func readGoreleaserVersion(path string) (string, error) {
	return readToolchainVersion(path, "GORELEASER_VERSION", true)
}

func readGoVersion(path string) (string, error) {
	return readToolchainVersion(path, "GO_VERSION", false)
}

func readToolchainVersion(path, selectedKey string, leadingV bool) (string, error) {
	contents, err := readBoundedRegularFile(path, maxToolchainLockBytes)
	if err != nil {
		return "", fmt.Errorf("read toolchain lock: %w", err)
	}
	if len(contents) == 0 {
		return "", errors.New("toolchain lock has invalid size")
	}
	version := ""
	for _, line := range strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !toolchainLine.MatchString(line) {
			return "", errors.New("toolchain lock contains invalid data")
		}
		key, value, _ := strings.Cut(line, "=")
		if key != selectedKey {
			continue
		}
		if version != "" {
			return "", errors.New("toolchain lock repeats selected version")
		}
		version = value
	}
	valid := toolVersionPattern.MatchString(version)
	if !leadingV {
		valid = prodVersionPattern.MatchString(version)
	}
	if !valid {
		return "", errors.New("toolchain lock has invalid selected version")
	}
	return version, nil
}

func writeBuildMetadata(path string, metadata buildMetadata) error {
	return writeExclusiveFile(path, "build metadata", func(writer io.Writer) error {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(metadata)
	})
}
