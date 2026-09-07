package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSelectFreshBinariesUsesInternalTypeAndExactTargetPaths(t *testing.T) {
	artifactsDir := filepath.Join(t.TempDir(), "artifacts")
	records := make([]goreleaserArtifact, 0, 8)
	for index := len(releaseTargets) - 1; index >= 0; index-- {
		target := releaseTargets[index]
		cpu := "v1"
		targetName := target.OS + "_" + target.Arch + "_v1"
		if target.Arch == "arm64" {
			cpu = "v8.0"
			targetName = target.OS + "_" + target.Arch + "_v8.0"
		}
		binaryName := "hookspot-stage"
		if target.OS == "windows" {
			binaryName += ".exe"
		}
		record := goreleaserArtifact{
			Path:         filepath.Join(artifactsDir, "hookspot_"+targetName, binaryName),
			Name:         binaryName,
			Type:         "Binary",
			InternalType: 4,
			GOOS:         target.OS,
			GOARCH:       target.Arch,
			Target:       targetName,
		}
		record.Extra.ID = "hookspot"
		if target.Arch == "amd64" {
			record.GOAMD64 = cpu
		} else {
			record.GOARM64 = cpu
		}
		records = append(records, record)
	}
	lookalike := records[0]
	lookalike.InternalType = 5
	records = append(records, lookalike)

	selected, err := selectFreshBinaries(records, artifactsDir, "stage")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != len(releaseTargets) {
		t.Fatalf("selected %d binaries", len(selected))
	}

	bad := append([]goreleaserArtifact(nil), records...)
	bad[0].Path = filepath.Join(artifactsDir, "hookspot_"+bad[0].Target, "elsewhere")
	if _, err := selectFreshBinaries(bad, artifactsDir, "stage"); err == nil {
		t.Fatal("unexpected fresh binary path was accepted")
	}
	bad = append([]goreleaserArtifact(nil), records...)
	bad[0].Path = artifactsDir + "/link/../hookspot_" + bad[0].Target + "/" + bad[0].Name
	if _, err := selectFreshBinaries(bad, artifactsDir, "stage"); err == nil {
		t.Fatal("noncanonical fresh binary path was accepted")
	}
}

func TestValidDockerImageIDUsesSHA256Width(t *testing.T) {
	const actualBuilderID = "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56"
	if !validDockerImageID(actualBuilderID) {
		t.Fatal("actual inspected builder image ID was rejected")
	}
	if validDockerImageID("sha256:7152d237029be94791d87bbd67eec0ea9695ac1e") {
		t.Fatal("40-hex Git object was accepted as a Docker image ID")
	}
}

func TestWriteArtifactReceiptIsExclusiveAndRecordsUnobservedTargets(t *testing.T) {
	dir, options, metadata := receiptFixture(t)
	t.Setenv("RELEASE_BUILDER_IMAGE_ID", "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56")
	t.Setenv("RELEASE_TAG_OBJECT", "")
	checks := []verifiedArtifact{{
		Name:   "hookspot_stage_0.0.0-snapshot.0123456_darwin_amd64.tar.gz",
		SHA256: strings.Repeat("a", 64), OS: "darwin", Arch: "amd64",
		Format: true, BuildInfo: true, FreshMatch: true, RuntimeCheck: false,
	}}
	if err := writeArtifactReceipt(options, metadata, checks); err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(dir, "receipt.json")
	contents, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(contents, []byte(`"runtime_observed": false`)) {
		t.Fatalf("receipt does not explicitly record unobserved target: %s", contents)
	}
	info, err := os.Stat(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("receipt mode = %04o", info.Mode().Perm())
	}
	if err := writeArtifactReceipt(options, metadata, checks); err == nil {
		t.Fatal("existing receipt was overwritten")
	}
	after, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, contents) {
		t.Fatal("existing receipt changed after rejected rewrite")
	}
}

func TestWriteArtifactReceiptRejectsMissingPrerequisites(t *testing.T) {
	t.Run("invalid image ID", func(t *testing.T) {
		_, options, metadata := receiptFixture(t)
		t.Setenv("RELEASE_BUILDER_IMAGE_ID", "sha256:0123456")
		if err := writeArtifactReceipt(options, metadata, nil); err == nil {
			t.Fatal("invalid builder image ID was accepted")
		}
	})
	t.Run("missing release tag object", func(t *testing.T) {
		_, options, metadata := receiptFixture(t)
		options.snapshot = false
		options.tag = "v0.1.0-stage.1"
		metadata.Version = "0.1.0-stage.1"
		metadata.BuildKind = "release"
		t.Setenv("RELEASE_BUILDER_IMAGE_ID", "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56")
		t.Setenv("RELEASE_TAG_OBJECT", "")
		if err := writeArtifactReceipt(options, metadata, nil); err == nil {
			t.Fatal("missing release tag object was accepted")
		}
	})
	t.Run("missing helper", func(t *testing.T) {
		dir, options, metadata := receiptFixture(t)
		if err := os.Rename(filepath.Join(dir, "tools", "releasecheck-linux-arm64"), filepath.Join(dir, "tools", "missing-helper")); err != nil {
			t.Fatal(err)
		}
		t.Setenv("RELEASE_BUILDER_IMAGE_ID", "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56")
		t.Setenv("RELEASE_TAG_OBJECT", "")
		if err := writeArtifactReceipt(options, metadata, nil); err == nil {
			t.Fatal("missing retained helper was accepted")
		}
	})
}

func receiptFixture(t *testing.T) (string, artifactsOptions, buildMetadata) {
	t.Helper()
	t.Setenv("RELEASE_BUILDER_PLATFORM", "linux/amd64")
	t.Setenv("RELEASE_INVOCATION_STARTED_AT", "2026-09-06T10:00:00Z")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "release"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "out", "tools"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"release/environments.json":          validManifest,
		"release/toolchain.env":              "GO_VERSION=" + strings.TrimPrefix(runtime.Version(), "go") + "\nGORELEASER_VERSION=v2.17.1\n",
		".goreleaser.yaml":                   "version: 2\n",
		"docker/release.Dockerfile":          "FROM scratch\n",
		"scripts/release.sh":                 "#!/bin/bash\n",
		"out/build-info.json":                "{\"fixture\":true}\n",
		"out/tools/releasecheck-linux-amd64": "amd64 helper",
		"out/tools/releasecheck-linux-arm64": "arm64 helper",
	}
	for name, contents := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if fixtureTools := os.Getenv("HOOKSPOT_SHELL_FIXTURE_TOOLS"); fixtureTools != "" {
		for _, arch := range []string{"amd64", "arm64"} {
			name := "releasecheck-linux-" + arch
			contents, err := os.ReadFile(filepath.Join(fixtureTools, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "out", "tools", name), contents, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(dir, "out", "tools", name), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	metadata := buildMetadata{
		SchemaVersion: 1, GoreleaserVersion: "v2.17.1", Version: "0.0.0-snapshot.0123456",
		Environment: "stage", ServerURL: "https://stage.example.invalid/gateway",
		Commit: "0123456789abcdef0123456789abcdef01234567", SourceDate: "2026-09-05T12:34:56Z",
		BuildKind: "snapshot", GoVersion: runtime.Version(), Targets: append([]buildTarget(nil), releaseTargets...),
	}
	return filepath.Join(dir, "out"), artifactsOptions{environment: "stage", dist: filepath.Join(dir, "out"), snapshot: true}, metadata
}

func TestObserveVersionBoundsActualChildOutput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("isolated shell fixture requires Linux")
	}
	metadata, target, output := observedVersionFixture(t)
	for _, test := range []struct {
		name   string
		script string
	}{
		{name: "stdout", script: "printf '%s\\n' '" + output + "'\nprintf '%200000s' ''\n"},
		{name: "stderr", script: "printf '%s\\n' '" + output + "'\nprintf '%200000s' '' >&2\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "version-fixture")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+test.script), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := observeVersion(binary, metadata, target, t.TempDir()); err == nil {
				t.Fatal("oversize child output was accepted")
			}
		})
	}
}

func TestObserveVersionBoundsInheritedPipeWait(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("isolated shell fixture requires Linux")
	}
	metadata, target, output := observedVersionFixture(t)
	binary := filepath.Join(t.TempDir(), "version-fixture")
	script := "#!/bin/sh\n/bin/sleep 2 &\nprintf '%s\\n' '" + output + "'\nexit 0\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := observeVersion(binary, metadata, target, t.TempDir())
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("inherited child pipe delayed return for %s", elapsed)
	}
	if err == nil {
		t.Fatal("incompletely drained child output was accepted")
	}
}

func observedVersionFixture(t *testing.T) (buildMetadata, buildTarget, string) {
	t.Helper()
	metadata := buildMetadata{
		Version: "1.2.3", Environment: "prod", Commit: strings.Repeat("a", 40),
		SourceDate: "2026-09-05T00:00:00Z", BuildKind: "release", GoVersion: runtime.Version(),
		ServerURL: "https://prod.example.invalid",
	}
	target := buildTarget{OS: runtime.GOOS, Arch: runtime.GOARCH}
	want := observedVersion{
		Version: metadata.Version, Environment: metadata.Environment, Commit: metadata.Commit,
		SourceDate: metadata.SourceDate, BuildKind: metadata.BuildKind, GoVersion: metadata.GoVersion,
		OS: target.OS, Arch: target.Arch, ServerURL: metadata.ServerURL,
	}
	contents, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	return metadata, target, string(contents)
}

func TestReadGoreleaserArtifactsAcceptsUnorderedArrayAndBoundsRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifacts.json")
	records := []goreleaserArtifact{{InternalType: 12, Name: "checksums"}, {InternalType: 4, Name: "hookspot"}}
	contents, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readGoreleaserArtifacts(path)
	if err != nil || len(got) != 2 || got[0].InternalType != 12 {
		t.Fatalf("read records = %#v, %v", got, err)
	}
	tooMany := make([]goreleaserArtifact, maxGoreleaserArtifactRecords+1)
	contents, err = json.Marshal(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readGoreleaserArtifacts(path); err == nil {
		t.Fatal("excess artifact records were accepted")
	}
}

func TestVerifyChecksumFileRequiresExactSixArchives(t *testing.T) {
	dir := t.TempDir()
	want := make(map[string]string)
	var lines strings.Builder
	for _, target := range releaseTargets {
		name := "hookspot_stage_0.0.0-snapshot.0123456_" + target.OS + "_" + target.Arch
		if target.OS == "windows" {
			name += ".zip"
		} else {
			name += ".tar.gz"
		}
		body := []byte(target.OS + "/" + target.Arch)
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
		hash := sha256Hex(body)
		want[name] = hash
		lines.WriteString(hash + "  " + name + "\n")
	}
	checksumPath := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(checksumPath, []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyChecksumFile(checksumPath, want); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checksumPath, []byte(lines.String()+strings.Repeat("0", 64)+"  extra.zip\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyChecksumFile(checksumPath, want); err == nil {
		t.Fatal("extra checksum entry was accepted")
	}
}

type testArchiveEntry struct {
	name     string
	body     []byte
	mode     os.FileMode
	typeflag byte
	linkname string
}

func validArchiveEntries(binaryName string) []testArchiveEntry {
	return []testArchiveEntry{
		{name: binaryName, body: []byte("executable"), mode: 0o755},
		{name: "README.md", body: []byte("readme"), mode: 0o644},
		{name: "INSTALL.md", body: []byte("install"), mode: 0o644},
		{name: "THIRD_PARTY_NOTICES.txt", body: []byte("notices"), mode: 0o644},
		{name: "build-info.json", body: []byte(`{"schema_version":1}`), mode: 0o644},
	}
}

func TestReadReleaseArchiveAcceptsExactRootMembers(t *testing.T) {
	for _, format := range []string{"tar.gz", "zip"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "archive."+format)
			writeTestArchive(t, path, format, validArchiveEntries("hookspot"), nil)
			archive, err := readReleaseArchive(path, "hookspot", defaultArtifactLimits)
			if err != nil {
				t.Fatal(err)
			}
			if len(archive.members) != 5 || string(archive.members["hookspot"].data) != "executable" || archive.sha256 == "" {
				t.Fatalf("archive = %#v", archive)
			}
		})
	}
}

func TestReadReleaseArchiveRejectsUnsafeMemberSets(t *testing.T) {
	tests := []struct {
		name    string
		entries []testArchiveEntry
	}{
		{name: "missing", entries: validArchiveEntries("hookspot")[:4]},
		{name: "extra", entries: append(validArchiveEntries("hookspot"), testArchiveEntry{name: "extra", body: []byte("x"), mode: 0o644})},
		{name: "traversal", entries: append(validArchiveEntries("hookspot")[:4], testArchiveEntry{name: "../build-info.json", body: []byte("x"), mode: 0o644})},
		{name: "nested", entries: append(validArchiveEntries("hookspot")[:4], testArchiveEntry{name: "docs/build-info.json", body: []byte("x"), mode: 0o644})},
		{name: "duplicate", entries: append(validArchiveEntries("hookspot"), testArchiveEntry{name: "README.md", body: []byte("again"), mode: 0o644})},
		{name: "wrong mode", entries: append(validArchiveEntries("hookspot")[:4], testArchiveEntry{name: "build-info.json", body: []byte("x"), mode: 0o600})},
		{name: "link", entries: append(validArchiveEntries("hookspot")[:4], testArchiveEntry{name: "build-info.json", mode: 0o644, typeflag: tar.TypeSymlink, linkname: "README.md"})},
	}
	for _, format := range []string{"tar.gz", "zip"} {
		for _, test := range tests {
			t.Run(format+"/"+test.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "archive."+format)
				writeTestArchive(t, path, format, test.entries, nil)
				if _, err := readReleaseArchive(path, "hookspot", defaultArtifactLimits); err == nil {
					t.Fatal("unsafe archive was accepted")
				}
			})
		}
	}
}

func TestReadReleaseArchiveRejectsSpecialPermissionBits(t *testing.T) {
	for _, format := range []string{"tar.gz", "zip"} {
		for _, special := range []struct {
			name string
			mode os.FileMode
		}{
			{name: "setuid", mode: os.ModeSetuid},
			{name: "setgid", mode: os.ModeSetgid},
			{name: "sticky", mode: os.ModeSticky},
		} {
			t.Run(format+"/"+special.name, func(t *testing.T) {
				entries := validArchiveEntries("hookspot")
				entries[0].mode |= special.mode
				path := filepath.Join(t.TempDir(), "archive."+format)
				writeTestArchive(t, path, format, entries, nil)
				if _, err := readReleaseArchive(path, "hookspot", defaultArtifactLimits); err == nil {
					t.Fatal("archive with a special executable permission bit was accepted")
				}
			})
		}
	}
}

func TestReadReleaseArchiveRejectsBoundsAndTrailingData(t *testing.T) {
	t.Run("ZIP local name differs from central directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "archive.zip")
		writeTestArchive(t, path, "zip", validArchiveEntries("hookspot"), nil)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(contents[30:38]) != "hookspot" {
			t.Fatal("unexpected ZIP fixture layout")
		}
		copy(contents[30:38], "../evilx")
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readReleaseArchive(path, "hookspot", defaultArtifactLimits); err == nil {
			t.Fatal("mismatched local ZIP traversal name was accepted")
		}
	})

	t.Run("ZIP local compression method differs from central directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "archive.zip")
		writeTestArchive(t, path, "zip", validArchiveEntries("hookspot"), nil)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(contents[30:38]) != "hookspot" {
			t.Fatal("unexpected ZIP fixture layout")
		}
		if binary.LittleEndian.Uint16(contents[8:10]) != zip.Store {
			t.Fatal("unexpected ZIP fixture compression method")
		}
		// The local header says Deflate while the central directory and payload remain Store.
		binary.LittleEndian.PutUint16(contents[8:10], zip.Deflate)
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readReleaseArchive(path, "hookspot", defaultArtifactLimits); err == nil {
			t.Fatal("mismatched local ZIP compression method was accepted")
		}
	})

	t.Run("expanded data", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "archive.tar.gz")
		entries := validArchiveEntries("hookspot")
		entries[0].body = bytes.Repeat([]byte("x"), 1024)
		writeTestArchive(t, path, "tar.gz", entries, nil)
		limits := defaultArtifactLimits
		limits.maxExpandedBytes = 512
		if _, err := readReleaseArchive(path, "hookspot", limits); err == nil {
			t.Fatal("expanded archive limit was ignored")
		}
	})

	t.Run("member count", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "archive.zip")
		writeTestArchive(t, path, "zip", validArchiveEntries("hookspot"), nil)
		limits := defaultArtifactLimits
		limits.maxMembers = 4
		if _, err := readReleaseArchive(path, "hookspot", limits); err == nil {
			t.Fatal("archive member limit was ignored")
		}
	})

	t.Run("nonpadding tar trailer", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "archive.tar.gz")
		writeTestArchive(t, path, "tar.gz", validArchiveEntries("hookspot"), []byte("unexpected trailing data"))
		if _, err := readReleaseArchive(path, "hookspot", defaultArtifactLimits); err == nil {
			t.Fatal("appended decompressed tar data was accepted")
		}
	})

	t.Run("bad gzip trailer", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "archive.tar.gz")
		writeTestArchive(t, path, "tar.gz", validArchiveEntries("hookspot"), nil)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		contents[len(contents)-1] ^= 0xff
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readReleaseArchive(path, "hookspot", defaultArtifactLimits); err == nil {
			t.Fatal("damaged gzip trailer was accepted")
		}
	})

	t.Run("bad zip CRC", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "archive.zip")
		entries := validArchiveEntries("hookspot")
		entries[1].body = []byte("crc-body-sentinel")
		writeTestArchive(t, path, "zip", entries, nil)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		index := bytes.Index(contents, []byte("crc-body-sentinel"))
		if index < 0 {
			t.Fatal("stored ZIP fixture body not found")
		}
		contents[index] ^= 0xff
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readReleaseArchive(path, "hookspot", defaultArtifactLimits); err == nil {
			t.Fatal("damaged ZIP member was accepted")
		}
	})
}

func TestInspectExecutableFormatRejectsWrongKindAndTruncatedExtents(t *testing.T) {
	t.Run("wrong kind", func(t *testing.T) {
		if err := inspectExecutableFormat(minimalPE64(t, "amd64", false), buildTarget{OS: "linux", Arch: "amd64"}); err == nil {
			t.Fatal("PE executable was accepted for Linux")
		}
	})
	t.Run("bare COFF", func(t *testing.T) {
		data := minimalPE64(t, "amd64", false)
		peOffset := int(binary.LittleEndian.Uint32(data[0x3c:0x40]))
		if err := inspectExecutableFormat(data[peOffset+4:], buildTarget{OS: "windows", Arch: "amd64"}); err == nil {
			t.Fatal("bare COFF object was accepted as a PE image")
		}
	})

	tests := []struct {
		name   string
		data   []byte
		target buildTarget
	}{
		{name: "ELF", data: truncatedELF64(t), target: buildTarget{OS: "linux", Arch: runtime.GOARCH}},
		{name: "Mach-O", data: minimalMachO64("amd64", true), target: buildTarget{OS: "darwin", Arch: "amd64"}},
		{name: "PE", data: minimalPE64(t, "amd64", true), target: buildTarget{OS: "windows", Arch: "amd64"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := inspectExecutableFormat(test.data, test.target)
			if err == nil || !strings.Contains(err.Error(), "file extent") {
				t.Fatalf("inspect error = %v, want file extent rejection", err)
			}
		})
	}
}

func TestInspectExecutableFormatRequiresExecutableKind(t *testing.T) {
	elfData := executableELF64(t)
	elfTarget := buildTarget{OS: "linux", Arch: runtime.GOARCH}
	if err := inspectExecutableFormat(elfData, elfTarget); err != nil {
		t.Fatalf("valid ELF fixture rejected: %v", err)
	}
	var elfOrder binary.ByteOrder = binary.LittleEndian
	if elfData[5] == 2 {
		elfOrder = binary.BigEndian
	}
	elfOrder.PutUint16(elfData[16:18], 1)
	if err := inspectExecutableFormat(elfData, elfTarget); err == nil {
		t.Fatal("ELF relocatable object was accepted")
	}

	machData := minimalMachO64("amd64", false)
	machTarget := buildTarget{OS: "darwin", Arch: "amd64"}
	if err := inspectExecutableFormat(machData, machTarget); err != nil {
		t.Fatalf("valid Mach-O fixture rejected: %v", err)
	}
	binary.LittleEndian.PutUint32(machData[12:16], 1)
	if err := inspectExecutableFormat(machData, machTarget); err == nil {
		t.Fatal("Mach-O object file was accepted")
	}

	for _, test := range []struct {
		name   string
		mutate func(uint16) uint16
	}{
		{name: "DLL", mutate: func(flags uint16) uint16 { return flags | 0x2000 }},
		{name: "not executable", mutate: func(flags uint16) uint16 { return flags &^ 0x0002 }},
	} {
		t.Run("PE/"+test.name, func(t *testing.T) {
			data := minimalPE64(t, "amd64", false)
			target := buildTarget{OS: "windows", Arch: "amd64"}
			if err := inspectExecutableFormat(data, target); err != nil {
				t.Fatalf("valid PE fixture rejected: %v", err)
			}
			offset := int(binary.LittleEndian.Uint32(data[0x3c:0x40])) + 4 + 18
			flags := binary.LittleEndian.Uint16(data[offset : offset+2])
			binary.LittleEndian.PutUint16(data[offset:offset+2], test.mutate(flags))
			if err := inspectExecutableFormat(data, target); err == nil {
				t.Fatal("non-executable PE kind was accepted")
			}
		})
	}
}

func writeTestArchive(t *testing.T, path, format string, entries []testArchiveEntry, trailing []byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	switch format {
	case "tar.gz":
		gz := gzip.NewWriter(file)
		tw := tar.NewWriter(gz)
		for _, entry := range entries {
			typeflag := entry.typeflag
			if typeflag == 0 {
				typeflag = tar.TypeReg
			}
			mode := int64(entry.mode.Perm())
			if entry.mode&os.ModeSetuid != 0 {
				mode |= 0o4000
			}
			if entry.mode&os.ModeSetgid != 0 {
				mode |= 0o2000
			}
			if entry.mode&os.ModeSticky != 0 {
				mode |= 0o1000
			}
			header := &tar.Header{Name: entry.name, Mode: mode, Size: int64(len(entry.body)), Typeflag: typeflag, Linkname: entry.linkname}
			if typeflag != tar.TypeReg {
				header.Size = 0
			}
			if err := tw.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if typeflag == tar.TypeReg {
				if _, err := tw.Write(entry.body); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if len(trailing) > 0 {
			if _, err := gz.Write(trailing); err != nil {
				t.Fatal(err)
			}
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	case "zip":
		zw := zip.NewWriter(file)
		for _, entry := range entries {
			header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
			mode := entry.mode
			if entry.typeflag == tar.TypeSymlink {
				mode |= os.ModeSymlink
				entry.body = []byte(entry.linkname)
			}
			header.SetMode(mode)
			writer, err := zw.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(entry.body); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown test archive format %q", format)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func truncatedELF64(t *testing.T) []byte {
	copyData := executableELF64(t)
	var order binary.ByteOrder = binary.LittleEndian
	if copyData[5] == 2 {
		order = binary.BigEndian
	}
	programOffset := order.Uint64(copyData[32:40])
	header := programOffset
	order.PutUint64(copyData[header+8:header+16], uint64(len(copyData)-1))
	order.PutUint64(copyData[header+32:header+40], 16)
	return copyData
}

func executableELF64(t *testing.T) []byte {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 64 || !bytes.Equal(data[:4], []byte{0x7f, 'E', 'L', 'F'}) || data[4] != 2 {
		t.Skip("test executable is not ELF64")
	}
	var order binary.ByteOrder = binary.LittleEndian
	if data[5] == 2 {
		order = binary.BigEndian
	}
	programOffset := order.Uint64(data[32:40])
	programSize := uint64(order.Uint16(data[54:56]))
	programCount := uint64(order.Uint16(data[56:58]))
	if programCount == 0 || programSize < 40 || programOffset+programSize*programCount > uint64(len(data)) {
		t.Fatal("ELF fixture has no mutable program header")
	}
	return append([]byte(nil), data...)
}

func minimalMachO64(arch string, truncated bool) []byte {
	data := make([]byte, 32+72)
	order := binary.LittleEndian
	order.PutUint32(data[0:4], 0xfeedfacf)
	cpu := uint32(0x01000007)
	if arch == "arm64" {
		cpu = 0x0100000c
	}
	order.PutUint32(data[4:8], cpu)
	order.PutUint32(data[8:12], 3)
	order.PutUint32(data[12:16], 2)
	order.PutUint32(data[16:20], 1)
	order.PutUint32(data[20:24], 72)
	order.PutUint32(data[32:36], 0x19)
	order.PutUint32(data[36:40], 72)
	if truncated {
		order.PutUint64(data[72:80], uint64(len(data)-1))
		order.PutUint64(data[80:88], 16)
	}
	return data
}

func minimalPE64(t *testing.T, arch string, truncated bool) []byte {
	t.Helper()
	const peOffset = 0x80
	const optionalHeaderSize = 240
	data := make([]byte, peOffset+4+20+optionalHeaderSize+40)
	copy(data, "MZ")
	binary.LittleEndian.PutUint32(data[0x3c:0x40], peOffset)
	copy(data[peOffset:], "PE\x00\x00")
	fileHeader := data[peOffset+4 : peOffset+24]
	machine := uint16(0x8664)
	if arch == "arm64" {
		machine = 0xaa64
	}
	binary.LittleEndian.PutUint16(fileHeader[0:2], machine)
	binary.LittleEndian.PutUint16(fileHeader[2:4], 1)
	binary.LittleEndian.PutUint16(fileHeader[16:18], optionalHeaderSize)
	binary.LittleEndian.PutUint16(fileHeader[18:20], 0x0002)
	optionalHeader := data[peOffset+24 : peOffset+24+optionalHeaderSize]
	binary.LittleEndian.PutUint16(optionalHeader[0:2], 0x20b)
	binary.LittleEndian.PutUint32(optionalHeader[108:112], 16)
	section := data[peOffset+24+optionalHeaderSize:]
	copy(section[:8], ".text")
	if truncated {
		binary.LittleEndian.PutUint32(section[16:20], 16)
		binary.LittleEndian.PutUint32(section[20:24], uint32(len(data)-1))
	}
	return data
}
