package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type goreleaserArtifact struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	Type         string `json:"type"`
	InternalType int    `json:"internal_type"`
	GOOS         string `json:"goos"`
	GOARCH       string `json:"goarch"`
	GOAMD64      string `json:"goamd64"`
	GOARM64      string `json:"goarm64"`
	Target       string `json:"target"`
	Extra        struct {
		ID string `json:"ID"`
	} `json:"extra"`
}

func readGoreleaserArtifacts(path string) ([]goreleaserArtifact, error) {
	contents, err := readBoundedRegularFile(path, maxArtifactJSONBytes)
	if err != nil {
		return nil, fmt.Errorf("read GoReleaser artifact manifest: %w", err)
	}
	var records []goreleaserArtifact
	if err := json.Unmarshal(contents, &records); err != nil {
		return nil, errors.New("decode GoReleaser artifact manifest")
	}
	if len(records) == 0 || len(records) > maxGoreleaserArtifactRecords {
		return nil, errors.New("GoReleaser artifact manifest has invalid record count")
	}
	return records, nil
}

func selectFreshBinaries(records []goreleaserArtifact, artifactsDir, environment string) (map[buildTarget]goreleaserArtifact, error) {
	selected := make(map[buildTarget]goreleaserArtifact, len(releaseTargets))
	for _, record := range records {
		if record.InternalType != 4 || record.Extra.ID != "hookspot" {
			continue
		}
		target := buildTarget{OS: record.GOOS, Arch: record.GOARCH}
		if !isReleaseTarget(target) {
			return nil, errors.New("GoReleaser reported an unexpected fresh binary target")
		}
		if _, exists := selected[target]; exists {
			return nil, errors.New("GoReleaser reported a duplicate fresh binary target")
		}
		binaryName := releaseBinaryName(environment, target.OS)
		cpu := "v1"
		if target.Arch == "amd64" {
			if record.GOAMD64 != cpu || record.GOARM64 != "" {
				return nil, errors.New("GoReleaser reported an unexpected amd64 feature level")
			}
		} else {
			cpu = "v8.0"
			if record.GOARM64 != cpu || record.GOAMD64 != "" {
				return nil, errors.New("GoReleaser reported an unexpected arm64 feature level")
			}
		}
		targetName := target.OS + "_" + target.Arch + "_" + cpu
		wantPath := filepath.Join(artifactsDir, "hookspot_"+targetName, binaryName)
		if record.Type != "Binary" || record.Name != binaryName || record.Target != targetName || !filepath.IsAbs(record.Path) || record.Path != wantPath {
			return nil, errors.New("GoReleaser fresh binary identity is inconsistent")
		}
		selected[target] = record
	}
	if len(selected) != len(releaseTargets) {
		return nil, fmt.Errorf("GoReleaser reported %d fresh binary targets, want %d", len(selected), len(releaseTargets))
	}
	return selected, nil
}

func isReleaseTarget(target buildTarget) bool {
	for _, expected := range releaseTargets {
		if target == expected {
			return true
		}
	}
	return false
}

func releaseBinaryName(environment, goos string) string {
	name := "hookspot"
	if environment == "stage" {
		name += "-stage"
	}
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func verifyChecksumFile(path string, artifactHashes map[string]string) (string, error) {
	contents, err := readBoundedRegularFile(path, maxChecksumBytes)
	if err != nil {
		return "", fmt.Errorf("read checksum file: %w", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
	if len(lines) != len(artifactHashes) {
		return "", fmt.Errorf("checksum file has %d entries, want %d", len(lines), len(artifactHashes))
	}
	seen := make(map[string]bool, len(lines))
	for _, line := range lines {
		parts := strings.Split(line, "  ")
		if len(parts) != 2 || len(parts[0]) != sha256.Size*2 || strings.ContainsAny(parts[1], `/\\`) {
			return "", errors.New("checksum file contains invalid data")
		}
		if _, err := hex.DecodeString(parts[0]); err != nil {
			return "", errors.New("checksum file contains an invalid digest")
		}
		want, ok := artifactHashes[parts[1]]
		if !ok || seen[parts[1]] || parts[0] != want {
			return "", errors.New("checksum file does not match release archives")
		}
		seen[parts[1]] = true
	}
	return sha256Hex(contents), nil
}

func sha256Hex(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
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
