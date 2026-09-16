package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const (
	fixtureCommit     = "0123456789abcdef0123456789abcdef01234567"
	fixtureSourceDate = "2026-09-05T12:34:56Z"
	fixtureServerURL  = "https://prod.example.invalid/gateway/"
)

func TestRunMetadataWritesReleaseBuildInfoToRelativeOutput(t *testing.T) {
	dir := metadataFixtureDirectory(t)
	if err := os.Mkdir(filepath.Join(dir, "out"), 0o700); err != nil {
		t.Fatal(err)
	}

	err := run(metadataArgs("1.2.3", "release", fixtureServerURL, filepath.Join("out", "build-info.json")))
	if err != nil {
		t.Fatal(err)
	}

	metadata := readBuildMetadata(t, filepath.Join(dir, "out", "build-info.json"))
	if metadata.SchemaVersion != 1 || metadata.GoreleaserVersion != "v2.17.1" {
		t.Fatalf("metadata schema/GoReleaser = %d/%q", metadata.SchemaVersion, metadata.GoreleaserVersion)
	}
	if metadata.Version != "1.2.3" || metadata.Environment != "prod" || metadata.ServerURL != "https://prod.example.invalid/gateway" {
		t.Fatalf("metadata release identity = %#v", metadata)
	}
	if metadata.Commit != fixtureCommit || metadata.SourceDate != fixtureSourceDate || metadata.BuildKind != "release" {
		t.Fatalf("metadata source identity = %#v", metadata)
	}
	if metadata.GoVersion == "" {
		t.Fatal("metadata Go version is empty")
	}
	wantTargets := []buildTarget{
		{OS: "darwin", Arch: "amd64"},
		{OS: "darwin", Arch: "arm64"},
		{OS: "linux", Arch: "amd64"},
		{OS: "linux", Arch: "arm64"},
		{OS: "windows", Arch: "amd64"},
		{OS: "windows", Arch: "arm64"},
	}
	if !reflect.DeepEqual(metadata.Targets, wantTargets) {
		t.Fatalf("metadata targets = %#v, want %#v", metadata.Targets, wantTargets)
	}
}

func TestRunMetadataWritesSnapshotBuildInfoToAbsoluteOutput(t *testing.T) {
	dir := metadataFixtureDirectory(t)
	outputPath := filepath.Join(dir, "build-info.json")

	err := run(metadataArgs("0.0.0-snapshot.0123456", "snapshot", fixtureServerURL, outputPath))
	if err != nil {
		t.Fatal(err)
	}

	metadata := readBuildMetadata(t, outputPath)
	if metadata.Version != "0.0.0-snapshot.0123456" || metadata.BuildKind != "snapshot" || metadata.Environment != "prod" {
		t.Fatalf("metadata snapshot identity = %#v", metadata)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("metadata mode = %04o, want 0644", got)
	}
}

func TestRunMetadataRejectsInvalidInputs(t *testing.T) {
	cases := []struct {
		name      string
		version   string
		kind      string
		serverURL string
	}{
		{"stage-style release version", "1.2.3-stage.1", "release", fixtureServerURL},
		{"http server URL", "1.2.3", "release", "http://prod.example.invalid/gateway"},
		{"empty server URL", "1.2.3", "release", ""},
		{"unknown build kind", "1.2.3", "development", fixtureServerURL},
		{"snapshot suffix from another commit", "0.0.0-snapshot.abcdef0", "snapshot", fixtureServerURL},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := metadataFixtureDirectory(t)
			outputPath := filepath.Join(dir, "build-info.json")
			err := run(metadataArgs(testCase.version, testCase.kind, testCase.serverURL, outputPath))
			if err == nil {
				t.Fatal("invalid metadata input was accepted")
			}
			if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
				t.Fatalf("output was written for invalid input: %v", statErr)
			}
		})
	}
}

func TestRunMetadataRejectsMissingOutputPath(t *testing.T) {
	metadataFixtureDirectory(t)
	err := run(metadataArgs("1.2.3", "release", fixtureServerURL, ""))
	if err == nil {
		t.Fatal("missing metadata output path was accepted")
	}
}

func TestRunMetadataPreservesExistingOutput(t *testing.T) {
	dir := metadataFixtureDirectory(t)
	outputPath := filepath.Join(dir, "build-info.json")
	const preserved = "preserved"
	if err := os.WriteFile(outputPath, []byte(preserved), 0o600); err != nil {
		t.Fatal(err)
	}

	err := run(metadataArgs("1.2.3", "release", fixtureServerURL, outputPath))
	if err == nil {
		t.Fatal("existing metadata output was overwritten")
	}
	contents, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(contents) != preserved {
		t.Fatalf("existing output changed to %q", contents)
	}
}

func metadataArgs(version, kind, serverURL, output string) []string {
	return []string{
		"metadata",
		"--version", version,
		"--commit", fixtureCommit,
		"--source-date", fixtureSourceDate,
		"--kind", kind,
		"--server-url", serverURL,
		"--output", output,
	}
}

func readBuildMetadata(t *testing.T, path string) buildMetadata {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var metadata buildMetadata
	if err := json.Unmarshal(contents, &metadata); err != nil {
		t.Fatal(err)
	}
	return metadata
}

func metadataFixtureDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "release"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release", "toolchain.env"), []byte("GORELEASER_VERSION=v2.17.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return dir
}
