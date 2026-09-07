package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRunMetadataWritesResolvedPublicBuildInfo(t *testing.T) {
	dir := metadataFixtureDirectory(t)
	outputPath := filepath.Join(dir, "out", "build-info.json")
	if err := os.Mkdir(filepath.Dir(outputPath), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RELEASE_ENV", "stage")
	t.Setenv("SERVER_URL", "https://stage.example.invalid/gateway")

	var output bytes.Buffer
	err := run([]string{
		"metadata",
		"--version", "0.0.0-snapshot.0123456",
		"--commit", "0123456789abcdef0123456789abcdef01234567",
		"--source-date", "2026-09-05T12:34:56Z",
		"--kind", "snapshot",
		"--output", outputPath,
	}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("metadata output = %q, want quiet success", output.String())
	}

	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var metadata buildMetadata
	if err := json.Unmarshal(contents, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.SchemaVersion != 1 || metadata.GoreleaserVersion != "v2.17.1" {
		t.Fatalf("metadata schema/GoReleaser = %d/%q", metadata.SchemaVersion, metadata.GoreleaserVersion)
	}
	if metadata.Version != "0.0.0-snapshot.0123456" || metadata.Environment != "stage" || metadata.ServerURL != "https://stage.example.invalid/gateway" {
		t.Fatalf("metadata release identity = %#v", metadata)
	}
	if metadata.Commit != "0123456789abcdef0123456789abcdef01234567" || metadata.SourceDate != "2026-09-05T12:34:56Z" || metadata.BuildKind != "snapshot" {
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
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("metadata mode = %04o, want 0644", got)
	}
}

func TestRunMetadataRejectsMismatchAndPreservesExistingOutput(t *testing.T) {
	dir := metadataFixtureDirectory(t)
	outputPath := filepath.Join(dir, "build-info.json")
	const preserved = "preserved"
	if err := os.WriteFile(outputPath, []byte(preserved), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RELEASE_ENV", "stage")
	t.Setenv("SERVER_URL", "https://prod.example.invalid/gateway")

	err := run([]string{
		"metadata",
		"--version", "0.0.0-snapshot.abcdef0",
		"--commit", "0123456789abcdef0123456789abcdef01234567",
		"--source-date", "2026-09-05T12:34:56Z",
		"--kind", "snapshot",
		"--output", outputPath,
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("mismatched metadata environment was accepted")
	}
	contents, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(contents) != preserved {
		t.Fatalf("existing output changed to %q", contents)
	}
}

func TestRunMetadataRejectsUnknownBuildKind(t *testing.T) {
	dir := metadataFixtureDirectory(t)
	t.Setenv("RELEASE_ENV", "prod")
	t.Setenv("SERVER_URL", "https://prod.example.invalid/gateway")
	err := run([]string{
		"metadata",
		"--version", "1.2.3",
		"--commit", "0123456789abcdef0123456789abcdef01234567",
		"--source-date", "2026-09-05T12:34:56Z",
		"--kind", "development",
		"--output", filepath.Join(dir, "build-info.json"),
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("unknown metadata build kind was accepted")
	}
}

func TestRunMetadataRejectsSnapshotSuffixFromAnotherCommit(t *testing.T) {
	dir := metadataFixtureDirectory(t)
	t.Setenv("RELEASE_ENV", "stage")
	t.Setenv("SERVER_URL", "https://stage.example.invalid/gateway")
	err := run([]string{
		"metadata",
		"--version", "0.0.0-snapshot.abcdef0",
		"--commit", "0123456789abcdef0123456789abcdef01234567",
		"--source-date", "2026-09-05T12:34:56Z",
		"--kind", "snapshot",
		"--output", filepath.Join(dir, "build-info.json"),
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("snapshot suffix from another commit was accepted")
	}
}

func metadataFixtureDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "release"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release", "environments.json"), []byte(validManifest), 0o600); err != nil {
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
