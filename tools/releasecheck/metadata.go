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

const (
	buildEnvironment      = "prod"
	maxToolchainLockBytes = 64 * 1024
)

var (
	commitPattern      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	prodVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	snapshotPattern    = regexp.MustCompile(`^0\.0\.0-snapshot\.[0-9a-f]{7,40}$`)
	toolVersionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	toolchainLine      = regexp.MustCompile(`^[A-Z][A-Z0-9_]*=[^[:space:]]+$`)
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
	goreleaserVersion, err := readGoreleaserVersion("release/toolchain.env")
	if err != nil {
		return err
	}

	metadata := buildMetadata{
		SchemaVersion:     1,
		GoreleaserVersion: goreleaserVersion,
		Version:           *version,
		Environment:       buildEnvironment,
		ServerURL:         base.String(),
		Commit:            *commit,
		SourceDate:        *sourceDate,
		BuildKind:         *kind,
		GoVersion:         runtime.Version(),
		Targets:           append([]buildTarget(nil), releaseTargets...),
	}
	return writeBuildMetadata(*output, metadata)
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
		if !prodVersionPattern.MatchString(version) {
			return errors.New("metadata release version must be MAJOR.MINOR.PATCH")
		}
	default:
		return errors.New("metadata build kind must be snapshot or release")
	}
	return nil
}

func readGoreleaserVersion(path string) (string, error) {
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
		if key != "GORELEASER_VERSION" {
			continue
		}
		if version != "" {
			return "", errors.New("toolchain lock repeats GORELEASER_VERSION")
		}
		version = value
	}
	if !toolVersionPattern.MatchString(version) {
		return "", errors.New("toolchain lock has invalid GORELEASER_VERSION")
	}
	return version, nil
}

func readBoundedRegularFile(path string, maximum int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if maximum < 0 || !before.Mode().IsRegular() {
		return nil, errors.New("file type or size is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() < 0 || opened.Size() > maximum {
		return nil, errors.New("file type or size is invalid")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) != opened.Size() || int64(len(contents)) > maximum {
		return nil, errors.New("file changed or exceeded its size limit while reading")
	}
	return contents, nil
}

func writeBuildMetadata(path string, metadata buildMetadata) error {
	return writeExclusiveFile(path, "build metadata", func(writer io.Writer) error {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(metadata)
	})
}

func writeExclusiveFile(path, description string, write func(io.Writer) error) (result error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", description, err)
	}
	defer func() {
		if closeErr := file.Close(); result == nil && closeErr != nil {
			result = fmt.Errorf("close %s: %w", description, closeErr)
		}
		if result != nil {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o644); err != nil {
		return fmt.Errorf("set %s mode: %w", description, err)
	}
	if err := write(file); err != nil {
		return fmt.Errorf("write %s: %w", description, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", description, err)
	}
	return nil
}
