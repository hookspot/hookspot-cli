package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const (
	fixtureCommit     = "0123456789abcdef0123456789abcdef01234567"
	fixtureSourceDate = "2026-09-05T12:34:56Z"
	fixtureServerURL  = "https://prod.example.invalid/gateway/"
)

func TestRunMetadataWritesReleaseBuildInfoToRelativeOutput(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir(filepath.Join(dir, "out"), 0o700); err != nil {
		t.Fatal(err)
	}

	err := run(metadataArgs("1.2.3", "release", fixtureServerURL, filepath.Join("out", "build-info.json")))
	if err != nil {
		t.Fatal(err)
	}

	metadata := readBuildMetadata(t, filepath.Join(dir, "out", "build-info.json"))
	if metadata.Version != "1.2.3" || metadata.Environment != "prod" || metadata.ServerURL != "https://prod.example.invalid/gateway" {
		t.Fatalf("metadata release identity = %#v", metadata)
	}
	if metadata.Commit != fixtureCommit || metadata.SourceDate != fixtureSourceDate || metadata.BuildKind != "release" {
		t.Fatalf("metadata source identity = %#v", metadata)
	}
	if metadata.GoVersion == "" {
		t.Fatal("metadata Go version is empty")
	}
}

func TestRunMetadataAcceptsPreReleaseVersion(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "build-info.json")

	err := run(metadataArgs("1.2.3-rc.1", "release", fixtureServerURL, outputPath))
	if err != nil {
		t.Fatal(err)
	}

	metadata := readBuildMetadata(t, outputPath)
	if metadata.Version != "1.2.3-rc.1" || metadata.BuildKind != "release" {
		t.Fatalf("metadata pre-release identity = %#v", metadata)
	}
}

func TestRunMetadataWritesSnapshotBuildInfoToAbsoluteOutput(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "build-info.json")

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

func TestRunMetadataOverwritesExistingOutput(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "build-info.json")
	if err := os.WriteFile(outputPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := run(metadataArgs("1.2.3", "release", fixtureServerURL, outputPath))
	if err != nil {
		t.Fatal(err)
	}
	if metadata := readBuildMetadata(t, outputPath); metadata.Version != "1.2.3" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestRunMetadataReplacesSymlinkedOutputWithoutFollowingIt(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "target")
	if err := os.WriteFile(targetPath, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(dir, "build-info.json")
	if err := os.Symlink(targetPath, outputPath); err != nil {
		t.Fatal(err)
	}

	err := run(metadataArgs("1.2.3", "release", fixtureServerURL, outputPath))
	if err != nil {
		t.Fatal(err)
	}

	target, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(target) != "keep" {
		t.Fatalf("symlink target = %q, want untouched", target)
	}
	info, err := os.Lstat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("output mode = %v, want a regular file", info.Mode())
	}
	if metadata := readBuildMetadata(t, outputPath); metadata.Version != "1.2.3" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestRunMetadataRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name       string
		version    string
		commit     string
		sourceDate string
		kind       string
		serverURL  string
	}{
		{"tag-style release version", "v1.2.3", fixtureCommit, fixtureSourceDate, "release", fixtureServerURL},
		{"two-component release version", "1.2", fixtureCommit, fixtureSourceDate, "release", fixtureServerURL},
		{"release version with build metadata", "1.2.3+build", fixtureCommit, fixtureSourceDate, "release", fixtureServerURL},
		{"release version with empty pre-release", "1.2.3-", fixtureCommit, fixtureSourceDate, "release", fixtureServerURL},
		{"http server URL", "1.2.3", fixtureCommit, fixtureSourceDate, "release", "http://prod.example.invalid/gateway"},
		{"empty server URL", "1.2.3", fixtureCommit, fixtureSourceDate, "release", ""},
		{"unknown build kind", "1.2.3", fixtureCommit, fixtureSourceDate, "development", fixtureServerURL},
		{"short commit", "1.2.3", "0123456", fixtureSourceDate, "release", fixtureServerURL},
		{"uppercase commit", "1.2.3", "0123456789ABCDEF0123456789ABCDEF01234567", fixtureSourceDate, "release", fixtureServerURL},
		{"non-RFC3339 source date", "1.2.3", fixtureCommit, "2026-09-05 12:34:56", "release", fixtureServerURL},
		{"snapshot suffix from another commit", "0.0.0-snapshot.abcdef0", fixtureCommit, fixtureSourceDate, "snapshot", fixtureServerURL},
		{"release-style snapshot version", "1.2.3", fixtureCommit, fixtureSourceDate, "snapshot", fixtureServerURL},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outputPath := filepath.Join(t.TempDir(), "build-info.json")
			err := run([]string{
				"metadata",
				"--version", test.version,
				"--commit", test.commit,
				"--source-date", test.sourceDate,
				"--kind", test.kind,
				"--server-url", test.serverURL,
				"--output", outputPath,
			})
			if err == nil {
				t.Fatal("invalid metadata input was accepted")
			}
			if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
				t.Fatalf("output was written for invalid input: %v", statErr)
			}
		})
	}
}

func TestRunMetadataRejectsInvalidArguments(t *testing.T) {
	valid := metadataArgs("1.2.3", "release", fixtureServerURL, filepath.Join(t.TempDir(), "build-info.json"))
	for _, args := range [][]string{
		slices.Concat(valid, []string{"--bogus"}),
		slices.Concat(valid, []string{"positional"}),
	} {
		err := run(args)
		if err == nil || err.Error() != "invalid metadata arguments" {
			t.Fatalf("run(%q) = %v, want invalid arguments error", args, err)
		}
	}
}

func TestRunMetadataRejectsMissingOutputPath(t *testing.T) {
	err := run(metadataArgs("1.2.3", "release", fixtureServerURL, ""))
	if err == nil {
		t.Fatal("missing metadata output path was accepted")
	}
}

func TestRunMetadataReportsUnwritableOutput(t *testing.T) {
	err := run(metadataArgs("1.2.3", "release", fixtureServerURL, filepath.Join(t.TempDir(), "missing", "build-info.json")))
	if err == nil {
		t.Fatal("unwritable metadata output path was accepted")
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
