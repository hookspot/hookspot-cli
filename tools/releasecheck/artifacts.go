package main

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"
)

const (
	maxArtifactJSONBytes         = 8 * 1024 * 1024
	maxGoreleaserArtifactRecords = 64
	maxChecksumBytes             = 16 * 1024
	maxBuildMetadataBytes        = 256 * 1024
	maxVersionOutputBytes        = 64 * 1024
)

type artifactsOptions struct {
	environment string
	dist        string
	tag         string
	snapshot    bool
}

func runArtifacts(args []string) error {
	flags := flag.NewFlagSet("releasecheck artifacts", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := artifactsOptions{}
	flags.StringVar(&options.environment, "environment", "", "release environment")
	flags.StringVar(&options.dist, "dist", "", "retained output parent")
	flags.StringVar(&options.tag, "tag", "", "release tag")
	flags.BoolVar(&options.snapshot, "snapshot", false, "verify a snapshot")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid artifacts arguments")
	}
	if options.environment != "stage" && options.environment != "prod" {
		return errors.New("artifacts environment must be stage or prod")
	}
	if !filepath.IsAbs(options.dist) || options.snapshot == (options.tag != "") {
		return errors.New("artifacts requires an absolute --dist and exactly one of --snapshot or --tag")
	}
	return verifyArtifacts(options)
}

func verifyArtifacts(options artifactsOptions) error {
	parent, err := os.Lstat(options.dist)
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return errors.New("artifact output parent is not a directory")
	}
	artifactsDir := filepath.Join(options.dist, "artifacts")
	metadataBytes, err := readBoundedRegularFile("build-info.json", maxBuildMetadataBytes)
	if err != nil {
		return fmt.Errorf("read build metadata: %w", err)
	}
	metadata, err := decodeBuildMetadata(metadataBytes)
	if err != nil {
		return err
	}
	if err := validateArtifactMetadata(metadata, options); err != nil {
		return err
	}
	records, err := readGoreleaserArtifacts(filepath.Join(artifactsDir, "artifacts.json"))
	if err != nil {
		return err
	}
	fresh, err := selectFreshBinaries(records, artifactsDir, options.environment)
	if err != nil {
		return err
	}

	checks := make([]verifiedArtifact, 0, len(releaseTargets)+1)
	archiveHashes := make(map[string]string, len(releaseTargets))
	for _, target := range releaseTargets {
		archiveName := releaseArchiveName(options.environment, metadata.Version, target)
		archivePath := filepath.Join(artifactsDir, archiveName)
		binaryName := releaseBinaryName(options.environment, target.OS)
		archive, err := readReleaseArchive(archivePath, binaryName, defaultArtifactLimits)
		if err != nil {
			return err
		}
		if !bytes.Equal(archive.members["build-info.json"].data, metadataBytes) {
			return fmt.Errorf("archive %s has different build metadata", archiveName)
		}
		binary := archive.members[binaryName].data
		if err := inspectExecutableFormat(binary, target); err != nil {
			return fmt.Errorf("inspect executable in %s: %w", archiveName, err)
		}
		if err := validateGoBuildInfo(binary, target, metadata); err != nil {
			return fmt.Errorf("inspect build information in %s: %w", archiveName, err)
		}
		freshBytes, err := readBoundedRegularFile(fresh[target].Path, defaultArtifactLimits.maxMemberBytes)
		if err != nil {
			return fmt.Errorf("read fresh binary for %s/%s: %w", target.OS, target.Arch, err)
		}
		if sha256Hex(binary) != sha256Hex(freshBytes) {
			return fmt.Errorf("archive %s does not contain its fresh binary", archiveName)
		}
		runtimeChecked := false
		if target.OS == runtime.GOOS && target.Arch == runtime.GOARCH {
			if err := observeVersion(fresh[target].Path, metadata, target, options.dist); err != nil {
				return fmt.Errorf("observe version for %s/%s: %w", target.OS, target.Arch, err)
			}
			runtimeChecked = true
		}
		archiveHashes[archiveName] = archive.sha256
		checks = append(checks, verifiedArtifact{
			Name: archiveName, SHA256: archiveHashes[archiveName], OS: target.OS, Arch: target.Arch,
			Format: true, BuildInfo: true, FreshMatch: true, RuntimeCheck: runtimeChecked,
		})
	}
	checksumName := "hookspot_" + options.environment + "_" + metadata.Version + "_checksums.txt"
	checksumPath := filepath.Join(artifactsDir, checksumName)
	checksumHash, err := verifyChecksumFile(checksumPath, archiveHashes)
	if err != nil {
		return err
	}
	checks = append(checks, verifiedArtifact{Name: checksumName, SHA256: checksumHash})
	if err := validateReleaseAssetSet(artifactsDir, archiveHashes, checksumName); err != nil {
		return err
	}
	if err := writeBytesExclusive(filepath.Join(options.dist, "build-info.json"), metadataBytes); err != nil {
		return fmt.Errorf("retain build metadata: %w", err)
	}
	return writeArtifactReceipt(options, metadata, checks)
}

func decodeBuildMetadata(contents []byte) (buildMetadata, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var metadata buildMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return buildMetadata{}, errors.New("decode build metadata")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return buildMetadata{}, errors.New("build metadata must contain one JSON object")
	}
	return metadata, nil
}

func validateArtifactMetadata(metadata buildMetadata, options artifactsOptions) error {
	if metadata.SchemaVersion != 1 || metadata.Environment != options.environment || !reflect.DeepEqual(metadata.Targets, releaseTargets) {
		return errors.New("build metadata does not match the requested release")
	}
	if err := validateBuildIdentity(metadata.Environment, metadata.Version, metadata.Commit, metadata.SourceDate, metadata.BuildKind); err != nil {
		return err
	}
	if options.snapshot {
		if metadata.BuildKind != "snapshot" {
			return errors.New("build metadata is not a snapshot")
		}
	} else if metadata.BuildKind != "release" || options.tag != "v"+metadata.Version {
		return errors.New("build metadata does not match the requested tag")
	}
	manifest, err := loadEnvironmentManifest("release/environments.json")
	if err != nil {
		return err
	}
	if err := checkEnvironment(manifest, metadata.Environment, metadata.ServerURL); err != nil {
		return err
	}
	version, err := readGoreleaserVersion("release/toolchain.env")
	if err != nil {
		return err
	}
	if metadata.GoreleaserVersion != version || metadata.GoVersion != runtime.Version() {
		return errors.New("build metadata does not match the pinned toolchain")
	}
	return nil
}

func releaseArchiveName(environment, version string, target buildTarget) string {
	extension := ".tar.gz"
	if target.OS == "windows" {
		extension = ".zip"
	}
	return "hookspot_" + environment + "_" + version + "_" + target.OS + "_" + target.Arch + extension
}

func validateGoBuildInfo(data []byte, target buildTarget, metadata buildMetadata) error {
	info, err := buildinfo.Read(bytes.NewReader(data))
	if err != nil {
		return errors.New("executable has no readable go build information")
	}
	if info.GoVersion != metadata.GoVersion {
		return errors.New("go version does not match public metadata")
	}
	if info.Path != "hookspot" {
		return errors.New("go program path is not hookspot")
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		if _, exists := settings[setting.Key]; exists {
			return errors.New("go build information repeats a setting")
		}
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != target.OS || settings["GOARCH"] != target.Arch || settings["vcs.revision"] != metadata.Commit || settings["vcs.time"] != metadata.SourceDate || settings["vcs.modified"] != "false" {
		return errors.New("go build information does not match source or target")
	}
	if settings["CGO_ENABLED"] != "0" || settings["-trimpath"] != "true" || settings["-buildmode"] != "exe" {
		return errors.New("go build information does not match release build settings")
	}
	if target.Arch == "amd64" && settings["GOAMD64"] != "v1" {
		return errors.New("go build information has the wrong amd64 feature level")
	}
	if target.Arch == "arm64" && settings["GOARM64"] != "v8.0" {
		return errors.New("go build information has the wrong arm64 feature level")
	}
	return nil
}

type observedVersion struct {
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

func observeVersion(binaryPath string, metadata buildMetadata, target buildTarget, parent string) error {
	home, err := os.MkdirTemp(parent, ".releasecheck-home-")
	if err != nil {
		return errors.New("create isolated runtime home")
	}
	defer os.RemoveAll(home)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var stdout limitedBuffer
	stdout.maximum = maxVersionOutputBytes
	var stderr limitedBuffer
	stderr.maximum = maxVersionOutputBytes
	command := exec.CommandContext(ctx, binaryPath, "version", "--json")
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home}
	command.Stdin = strings.NewReader("")
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.WaitDelay = 500 * time.Millisecond
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return errors.New("version command timed out")
		}
		return errors.New("version command failed")
	}
	if stderr.Len() != 0 {
		return errors.New("version command wrote unexpected stderr")
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	var observed observedVersion
	if err := decoder.Decode(&observed); err != nil {
		return errors.New("version command returned invalid JSON")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("version command returned extra data")
	}
	want := observedVersion{
		Version: metadata.Version, Environment: metadata.Environment, Commit: metadata.Commit,
		SourceDate: metadata.SourceDate, BuildKind: metadata.BuildKind, GoVersion: metadata.GoVersion,
		OS: target.OS, Arch: target.Arch, ServerURL: metadata.ServerURL,
	}
	if observed != want {
		return errors.New("version command identity does not match build metadata")
	}
	return nil
}

type limitedBuffer struct {
	data    bytes.Buffer
	maximum int
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	if len(data) > buffer.maximum-buffer.data.Len() {
		return 0, errors.New("command output exceeds limit")
	}
	return buffer.data.Write(data)
}

func (buffer *limitedBuffer) Bytes() []byte {
	return buffer.data.Bytes()
}

func (buffer *limitedBuffer) Len() int {
	return buffer.data.Len()
}

func validateReleaseAssetSet(artifactsDir string, archiveHashes map[string]string, checksumName string) error {
	entries, err := os.ReadDir(artifactsDir)
	if err != nil {
		return fmt.Errorf("read artifact directory: %w", err)
	}
	allowed := make(map[string]bool, len(archiveHashes)+4)
	for name := range archiveHashes {
		allowed[name] = true
	}
	allowed[checksumName] = true
	allowed["artifacts.json"] = true
	allowed["config.yaml"] = true
	allowed["metadata.json"] = true
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("artifact directory contains a symbolic link")
		}
		if entry.IsDir() || allowed[entry.Name()] {
			continue
		}
		if strings.HasPrefix(entry.Name(), "hookspot_") || strings.HasSuffix(entry.Name(), ".tar.gz") || strings.HasSuffix(entry.Name(), ".zip") {
			return fmt.Errorf("artifact directory contains unexpected release file %q", safeArchiveName(entry.Name()))
		}
	}
	return nil
}

func writeBytesExclusive(path string, contents []byte) error {
	return writeExclusiveFile(path, "file", func(writer io.Writer) error {
		_, err := writer.Write(contents)
		return err
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
