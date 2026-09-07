package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const (
	maxReceiptBytes = 256 * 1024
	maxControlBytes = 256 * 1024
)

type verifiedArtifact struct {
	Name         string `json:"name"`
	SHA256       string `json:"sha256"`
	OS           string `json:"os,omitempty"`
	Arch         string `json:"arch,omitempty"`
	Format       bool   `json:"format_checked"`
	BuildInfo    bool   `json:"build_info_checked"`
	FreshMatch   bool   `json:"fresh_binary_matched"`
	RuntimeCheck bool   `json:"runtime_observed"`
}

type namedDigest struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type buildReceipt struct {
	SchemaVersion       int                `json:"schema_version"`
	CreatedAt           string             `json:"created_at"`
	InvocationStartedAt string             `json:"invocation_started_at"`
	SourceDate          string             `json:"source_date"`
	SourceRepository    string             `json:"source_repository"`
	Environment         string             `json:"environment"`
	Version             string             `json:"version"`
	BuildKind           string             `json:"build_kind"`
	Commit              string             `json:"commit"`
	Tag                 string             `json:"tag,omitempty"`
	TagObject           string             `json:"tag_object,omitempty"`
	ServerURL           string             `json:"server_url"`
	GoreleaserVersion   string             `json:"goreleaser_version"`
	GoVersion           string             `json:"go_version"`
	BuilderImageID      string             `json:"builder_image_id"`
	BuilderPlatform     string             `json:"builder_platform"`
	ManifestSHA256      string             `json:"manifest_sha256"`
	ToolchainSHA256     string             `json:"toolchain_sha256"`
	ConfigurationSHA256 string             `json:"configuration_sha256"`
	BuildMetadataSHA256 string             `json:"build_metadata_sha256"`
	Artifacts           []verifiedArtifact `json:"artifacts"`
	Helpers             []namedDigest      `json:"helpers"`
	Controls            []namedDigest      `json:"controls"`
}

var retainedControls = []struct {
	name    string
	maximum int64
}{
	{name: "release/environments.json", maximum: maxEnvironmentManifestBytes},
	{name: "release/toolchain.env", maximum: maxToolchainLockBytes},
	{name: ".goreleaser.yaml", maximum: maxControlBytes},
	{name: "docker/release.Dockerfile", maximum: maxControlBytes},
	{name: "scripts/release.sh", maximum: maxControlBytes},
}

func writeArtifactReceipt(options artifactsOptions, metadata buildMetadata, artifacts []verifiedArtifact) error {
	imageID := os.Getenv("RELEASE_BUILDER_IMAGE_ID")
	platform := os.Getenv("RELEASE_BUILDER_PLATFORM")
	startedAt := os.Getenv("RELEASE_INVOCATION_STARTED_AT")
	if !validDockerImageID(imageID) {
		return errors.New("release builder image ID is missing or invalid")
	}
	if platform != "linux/amd64" && platform != "linux/arm64" {
		return errors.New("release builder platform is missing or invalid")
	}
	started, err := parseReceiptTime(startedAt)
	if err != nil {
		return errors.New("release invocation start time is missing or invalid")
	}
	tagObject := os.Getenv("RELEASE_TAG_OBJECT")
	if options.snapshot {
		if tagObject != "" {
			return errors.New("snapshot must not have a tag object")
		}
	} else if !commitPattern.MatchString(tagObject) {
		return errors.New("release tag object is missing or invalid")
	}
	manifest, err := loadEnvironmentManifest("release/environments.json")
	if err != nil {
		return err
	}
	if err := validateEnvironmentManifest(manifest); err != nil {
		return err
	}

	controls := make([]namedDigest, 0, len(retainedControls))
	for _, control := range retainedControls {
		contents, err := readBoundedRegularFile(control.name, control.maximum)
		if err != nil {
			return fmt.Errorf("read retained control %s: %w", control.name, err)
		}
		destination := filepath.Join(options.dist, "controls", filepath.FromSlash(control.name))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return errors.New("create retained control directory")
		}
		if err := writeBytesExclusive(destination, contents); err != nil {
			return fmt.Errorf("retain control %s: %w", control.name, err)
		}
		controls = append(controls, namedDigest{Name: control.name, SHA256: sha256Hex(contents)})
	}
	buildMetadataHash, err := hashBoundedFile(filepath.Join(options.dist, "build-info.json"), maxBuildMetadataBytes)
	if err != nil {
		return fmt.Errorf("hash retained build metadata: %w", err)
	}
	helpers := make([]namedDigest, 0, 2)
	for _, arch := range []string{"amd64", "arm64"} {
		name := "releasecheck-linux-" + arch
		hash, err := hashBoundedFile(filepath.Join(options.dist, "tools", name), defaultArtifactLimits.maxMemberBytes)
		if err != nil {
			return fmt.Errorf("hash retained helper %s: %w", arch, err)
		}
		helpers = append(helpers, namedDigest{Name: name, SHA256: hash})
	}
	createdAt := time.Now().UTC().Truncate(time.Second)
	if createdAt.Before(started) {
		return errors.New("release invocation start time is after receipt creation")
	}
	receipt := buildReceipt{
		SchemaVersion: 2, CreatedAt: createdAt.Format(time.RFC3339), InvocationStartedAt: startedAt,
		SourceDate: metadata.SourceDate, SourceRepository: manifest.Repository,
		Environment: metadata.Environment, Version: metadata.Version, BuildKind: metadata.BuildKind,
		Commit: metadata.Commit, Tag: options.tag, TagObject: tagObject, ServerURL: metadata.ServerURL,
		GoreleaserVersion: metadata.GoreleaserVersion, GoVersion: metadata.GoVersion,
		BuilderImageID: imageID, BuilderPlatform: platform,
		ManifestSHA256: controls[0].SHA256, ToolchainSHA256: controls[1].SHA256,
		ConfigurationSHA256: controls[2].SHA256, BuildMetadataSHA256: buildMetadataHash,
		Artifacts: artifacts, Helpers: helpers, Controls: controls,
	}
	return writeJSONExclusive(filepath.Join(options.dist, "receipt.json"), receipt)
}

func verifyReceipt(dist, environment string) error {
	if environment != "stage" && environment != "prod" {
		return errors.New("receipt environment must be stage or prod")
	}
	contents, err := readBoundedRegularFile(filepath.Join(dist, "receipt.json"), maxReceiptBytes)
	if err != nil {
		return fmt.Errorf("read receipt: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var receipt buildReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return errors.New("decode receipt")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("receipt must contain one JSON object")
	}
	if err := validateReceipt(receipt, environment); err != nil {
		return err
	}
	for _, artifact := range receipt.Artifacts {
		if err := verifyNamedDigest(filepath.Join(dist, "artifacts"), namedDigest{Name: artifact.Name, SHA256: artifact.SHA256}, defaultArtifactLimits.maxCompressedBytes); err != nil {
			return fmt.Errorf("verify retained asset: %w", err)
		}
	}
	for _, helper := range receipt.Helpers {
		if err := verifyNamedDigest(filepath.Join(dist, "tools"), helper, defaultArtifactLimits.maxMemberBytes); err != nil {
			return fmt.Errorf("verify retained helper: %w", err)
		}
	}
	for index, control := range receipt.Controls {
		path := filepath.Join(dist, "controls", filepath.FromSlash(control.Name))
		hash, err := hashBoundedFile(path, retainedControls[index].maximum)
		if err != nil || hash != control.SHA256 {
			return errors.New("retained control does not match receipt")
		}
	}
	metadataHash, err := hashBoundedFile(filepath.Join(dist, "build-info.json"), maxBuildMetadataBytes)
	if err != nil || metadataHash != receipt.BuildMetadataSHA256 {
		return errors.New("retained build metadata does not match receipt")
	}
	manifest, err := loadEnvironmentManifest(filepath.Join(dist, "controls", "release", "environments.json"))
	if err != nil {
		return errors.New("decode retained environment manifest")
	}
	if err := validateEnvironmentManifest(manifest); err != nil || manifest.Repository != receipt.SourceRepository {
		return errors.New("retained environment manifest does not match receipt")
	}
	if err := checkEnvironment(manifest, receipt.Environment, receipt.ServerURL); err != nil {
		return errors.New("retained environment identity does not match receipt")
	}
	metadataBytes, err := readBoundedRegularFile(filepath.Join(dist, "build-info.json"), maxBuildMetadataBytes)
	if err != nil {
		return errors.New("read retained build metadata")
	}
	metadata, err := decodeBuildMetadata(metadataBytes)
	if err != nil {
		return errors.New("decode retained build metadata")
	}
	if metadata.SchemaVersion != 1 || metadata.Environment != receipt.Environment || metadata.ServerURL != receipt.ServerURL ||
		metadata.Version != receipt.Version || metadata.Commit != receipt.Commit ||
		metadata.SourceDate != receipt.SourceDate || metadata.BuildKind != receipt.BuildKind ||
		metadata.GoreleaserVersion != receipt.GoreleaserVersion || metadata.GoVersion != receipt.GoVersion ||
		!reflect.DeepEqual(metadata.Targets, releaseTargets) {
		return errors.New("retained build metadata identity does not match receipt")
	}
	toolchainPath := filepath.Join(dist, "controls", "release", "toolchain.env")
	goreleaserVersion, err := readGoreleaserVersion(toolchainPath)
	if err != nil || goreleaserVersion != receipt.GoreleaserVersion {
		return errors.New("retained GoReleaser identity does not match receipt")
	}
	goVersion, err := readGoVersion(toolchainPath)
	if err != nil || "go"+goVersion != receipt.GoVersion {
		return errors.New("retained Go identity does not match receipt")
	}
	return nil
}

func validateReceipt(receipt buildReceipt, environment string) error {
	if receipt.SchemaVersion != 2 || receipt.Environment != environment {
		return errors.New("receipt does not match the requested release")
	}
	createdAt, err := parseReceiptTime(receipt.CreatedAt)
	if err != nil {
		return errors.New("receipt creation time is invalid")
	}
	startedAt, err := parseReceiptTime(receipt.InvocationStartedAt)
	if err != nil || createdAt.Before(startedAt) {
		return errors.New("receipt invocation time is invalid")
	}
	if _, err := time.Parse(time.RFC3339, receipt.SourceDate); err != nil {
		return errors.New("receipt source date is invalid")
	}
	if validateRepositoryName(receipt.SourceRepository) != nil || !validDockerImageID(receipt.BuilderImageID) || receipt.GoVersion == "" || (receipt.BuilderPlatform != "linux/amd64" && receipt.BuilderPlatform != "linux/arm64") {
		return errors.New("receipt source or builder identity is invalid")
	}
	if err := validateBuildIdentity(receipt.Environment, receipt.Version, receipt.Commit, receipt.SourceDate, receipt.BuildKind); err != nil {
		return err
	}
	if receipt.BuildKind == "snapshot" {
		if receipt.Tag != "" || receipt.TagObject != "" {
			return errors.New("snapshot receipt contains tag identity")
		}
	} else if receipt.Tag != "v"+receipt.Version || !commitPattern.MatchString(receipt.TagObject) {
		return errors.New("release receipt tag identity is invalid")
	}
	if !validDigest(receipt.ManifestSHA256) || !validDigest(receipt.ToolchainSHA256) || !validDigest(receipt.ConfigurationSHA256) || !validDigest(receipt.BuildMetadataSHA256) {
		return errors.New("receipt contains an invalid digest")
	}
	if err := validateReceiptNames(receipt); err != nil {
		return err
	}
	return nil
}

func validateReceiptNames(receipt buildReceipt) error {
	wantArtifacts := make(map[string]buildTarget, len(releaseTargets))
	for _, target := range releaseTargets {
		wantArtifacts[releaseArchiveName(receipt.Environment, receipt.Version, target)] = target
	}
	checksumName := "hookspot_" + receipt.Environment + "_" + receipt.Version + "_checksums.txt"
	if len(receipt.Artifacts) != len(wantArtifacts)+1 {
		return errors.New("receipt does not contain exactly seven release assets")
	}
	checksumSeen := false
	for _, artifact := range receipt.Artifacts {
		if !validDigest(artifact.SHA256) {
			return errors.New("receipt contains an unexpected release asset")
		}
		if artifact.Name == checksumName {
			if checksumSeen || artifact.OS != "" || artifact.Arch != "" || artifact.Format || artifact.BuildInfo || artifact.FreshMatch || artifact.RuntimeCheck {
				return errors.New("receipt checksum record is invalid")
			}
			checksumSeen = true
			continue
		}
		target, exists := wantArtifacts[artifact.Name]
		if !exists || artifact.OS != target.OS || artifact.Arch != target.Arch || !artifact.Format || !artifact.BuildInfo || !artifact.FreshMatch {
			return errors.New("receipt archive verification record is invalid")
		}
		delete(wantArtifacts, artifact.Name)
	}
	if len(wantArtifacts) != 0 || !checksumSeen {
		return errors.New("receipt release asset set is incomplete")
	}
	wantHelpers := []string{"releasecheck-linux-amd64", "releasecheck-linux-arm64"}
	if !exactDigestNames(receipt.Helpers, wantHelpers) {
		return errors.New("receipt helper set is invalid")
	}
	wantControls := make([]string, 0, len(retainedControls))
	for _, control := range retainedControls {
		wantControls = append(wantControls, control.name)
	}
	if !exactDigestNames(receipt.Controls, wantControls) {
		return errors.New("receipt control set is invalid")
	}
	if receipt.Controls[0].SHA256 != receipt.ManifestSHA256 || receipt.Controls[1].SHA256 != receipt.ToolchainSHA256 || receipt.Controls[2].SHA256 != receipt.ConfigurationSHA256 {
		return errors.New("receipt control digests disagree")
	}
	return nil
}

func exactDigestNames(records []namedDigest, names []string) bool {
	if len(records) != len(names) {
		return false
	}
	for index, name := range names {
		if records[index].Name != name || !validDigest(records[index].SHA256) {
			return false
		}
	}
	return true
}

func verifyNamedDigest(parent string, record namedDigest, maximum int64) error {
	if filepath.Base(record.Name) != record.Name || record.Name == "." || record.Name == ".." || !validDigest(record.SHA256) {
		return errors.New("receipt contains an unsafe file record")
	}
	hash, err := hashBoundedFile(filepath.Join(parent, record.Name), maximum)
	if err != nil {
		return err
	}
	if hash != record.SHA256 {
		return errors.New("file digest does not match receipt")
	}
	return nil
}

func validDigest(value string) bool {
	digest, err := hex.DecodeString(value)
	return err == nil && len(digest) == sha256.Size && value == strings.ToLower(value)
}

func validDockerImageID(value string) bool {
	return strings.HasPrefix(value, "sha256:") && validDigest(strings.TrimPrefix(value, "sha256:"))
}

func parseReceiptTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339) != value {
		return time.Time{}, errors.New("invalid UTC receipt time")
	}
	return parsed, nil
}

func hashBoundedFile(path string, maximum int64) (string, error) {
	contents, err := readBoundedRegularFile(path, maximum)
	if err != nil {
		return "", err
	}
	return sha256Hex(contents), nil
}

func writeJSONExclusive(path string, value any) error {
	return writeExclusiveFile(path, "receipt", func(writer io.Writer) error {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	})
}
