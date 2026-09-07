package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyReceiptRejectsInconsistentCompleteClaims(t *testing.T) {
	tests := []struct {
		name   string
		change func(*buildReceipt, *buildMetadata)
	}{
		{name: "metadata schema", change: func(_ *buildReceipt, metadata *buildMetadata) { metadata.SchemaVersion = 99 }},
		{name: "archive OS", change: func(receipt *buildReceipt, _ *buildMetadata) { receipt.Artifacts[0].OS = "linux" }},
		{name: "archive architecture", change: func(receipt *buildReceipt, _ *buildMetadata) { receipt.Artifacts[0].Arch = "arm64" }},
		{name: "archive format unchecked", change: func(receipt *buildReceipt, _ *buildMetadata) { receipt.Artifacts[0].Format = false }},
		{name: "archive build info unchecked", change: func(receipt *buildReceipt, _ *buildMetadata) { receipt.Artifacts[0].BuildInfo = false }},
		{name: "archive fresh match unchecked", change: func(receipt *buildReceipt, _ *buildMetadata) { receipt.Artifacts[0].FreshMatch = false }},
		{name: "checksum claims target", change: func(receipt *buildReceipt, _ *buildMetadata) { receipt.Artifacts[6].OS = "darwin" }},
		{name: "Go differs from lock", change: func(receipt *buildReceipt, metadata *buildMetadata) {
			receipt.GoVersion = "go1.26.9"
			metadata.GoVersion = receipt.GoVersion
		}},
		{name: "GoReleaser differs from lock", change: func(receipt *buildReceipt, metadata *buildMetadata) {
			receipt.GoreleaserVersion = "v2.17.2"
			metadata.GoreleaserVersion = receipt.GoreleaserVersion
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir, receipt, metadata := completeReceiptFixture(t)
			test.change(&receipt, &metadata)
			metadataBytes, err := json.Marshal(metadata)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "build-info.json"), metadataBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			receipt.BuildMetadataSHA256 = fmt.Sprintf("%x", sha256.Sum256(metadataBytes))
			receiptBytes, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "receipt.json"), receiptBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := verifyReceipt(dir, "stage"); err == nil {
				t.Fatal("inconsistent retained claim was accepted")
			}
		})
	}
}

func completeReceiptFixture(t *testing.T) (string, buildReceipt, buildMetadata) {
	t.Helper()
	dir, options, metadata := receiptFixture(t)
	writeReceiptMetadata(t, dir, metadata)
	t.Setenv("RELEASE_BUILDER_IMAGE_ID", "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56")
	t.Setenv("RELEASE_TAG_OBJECT", "")
	artifacts := make([]verifiedArtifact, 0, len(releaseTargets)+1)
	for _, target := range releaseTargets {
		name := releaseArchiveName("stage", metadata.Version, target)
		contents := []byte("retained bytes for " + name)
		writeTestFile(t, filepath.Join(dir, "artifacts", name), string(contents))
		artifacts = append(artifacts, verifiedArtifact{Name: name, SHA256: fmt.Sprintf("%x", sha256.Sum256(contents)), OS: target.OS, Arch: target.Arch, Format: true, BuildInfo: true, FreshMatch: true})
	}
	name := "hookspot_stage_" + metadata.Version + "_checksums.txt"
	contents := []byte("retained checksum fixture")
	writeTestFile(t, filepath.Join(dir, "artifacts", name), string(contents))
	artifacts = append(artifacts, verifiedArtifact{Name: name, SHA256: fmt.Sprintf("%x", sha256.Sum256(contents))})
	if err := writeArtifactReceipt(options, metadata, artifacts); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt buildReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	return dir, receipt, metadata
}

func TestVerifyReceiptChecksEveryRetainedByteWithoutWriting(t *testing.T) {
	dir, options, metadata := receiptFixture(t)
	writeReceiptMetadata(t, dir, metadata)
	t.Setenv("RELEASE_BUILDER_IMAGE_ID", "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56")
	t.Setenv("RELEASE_BUILDER_PLATFORM", "linux/arm64")
	t.Setenv("RELEASE_INVOCATION_STARTED_AT", "2026-09-06T10:00:00Z")
	t.Setenv("RELEASE_TAG_OBJECT", "")

	checks := make([]verifiedArtifact, 0, len(releaseTargets)+1)
	for _, target := range releaseTargets {
		assetName := releaseArchiveName("stage", metadata.Version, target)
		writeTestFile(t, filepath.Join(dir, "artifacts", assetName), target.OS+target.Arch)
		assetHash, err := hashBoundedFile(filepath.Join(dir, "artifacts", assetName), defaultArtifactLimits.maxCompressedBytes)
		if err != nil {
			t.Fatal(err)
		}
		checks = append(checks, verifiedArtifact{Name: assetName, SHA256: assetHash, OS: target.OS, Arch: target.Arch, Format: true, BuildInfo: true, FreshMatch: true})
	}
	assetName := "hookspot_stage_" + metadata.Version + "_checksums.txt"
	writeTestFile(t, filepath.Join(dir, "artifacts", assetName), "checksums")
	assetHash, err := hashBoundedFile(filepath.Join(dir, "artifacts", assetName), maxChecksumBytes)
	if err != nil {
		t.Fatal(err)
	}
	checks = append(checks, verifiedArtifact{Name: assetName, SHA256: assetHash})
	if err := writeArtifactReceipt(options, metadata, checks); err != nil {
		t.Fatal(err)
	}

	receiptPath := filepath.Join(dir, "receipt.json")
	before, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyReceipt(dir, "stage"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("read-only verification changed the receipt")
	}
	var changedReceipt buildReceipt
	if err := json.Unmarshal(before, &changedReceipt); err != nil {
		t.Fatal(err)
	}
	changedReceipt.SourceRepository = "other/repository"
	changed, err := json.Marshal(changedReceipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyReceipt(dir, "stage"); err == nil {
		t.Fatal("receipt identity inconsistent with retained controls was accepted")
	}
	if err := os.WriteFile(receiptPath, before, 0o600); err != nil {
		t.Fatal(err)
	}

	writeTestFile(t, filepath.Join(dir, "artifacts", assetName), "changed")
	if err := verifyReceipt(dir, "stage"); err == nil {
		t.Fatal("changed retained asset was accepted")
	}
}

func TestReceiptRecordsSourceAndBuilderIdentity(t *testing.T) {
	dir, options, metadata := receiptFixture(t)
	writeReceiptMetadata(t, dir, metadata)
	t.Setenv("RELEASE_BUILDER_IMAGE_ID", "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56")
	t.Setenv("RELEASE_BUILDER_PLATFORM", "linux/arm64")
	t.Setenv("RELEASE_INVOCATION_STARTED_AT", "2026-09-06T10:00:00Z")
	t.Setenv("RELEASE_TAG_OBJECT", "")
	if err := writeArtifactReceipt(options, metadata, nil); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(dir, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"source_repository": "example/hookspot-cli"`,
		`"builder_platform": "linux/arm64"`,
		`"invocation_started_at": "2026-09-06T10:00:00Z"`,
		`"source_date": "2026-09-05T12:34:56Z"`,
		`"controls"`,
		`"build_metadata_sha256"`,
	} {
		if !bytes.Contains(contents, []byte(want)) {
			t.Fatalf("receipt is missing %s: %s", want, contents)
		}
	}
}

func TestVerifyReceiptRejectsUnsafeOrMalformedRecords(t *testing.T) {
	dir := t.TempDir()
	fixtures := []string{
		`{"schema_version":2}`,
		`{"schema_version":1,"environment":"stage","artifacts":[{"name":"../asset","sha256":"` + strings.Repeat("a", 64) + `"}]}`,
		`{"schema_version":1,"environment":"stage","unknown":true}`,
	}
	for _, fixture := range fixtures {
		if err := os.WriteFile(filepath.Join(dir, "receipt.json"), []byte(fixture), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := verifyReceipt(dir, "stage"); err == nil {
			t.Fatalf("invalid receipt was accepted: %s", fixture)
		}
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeReceiptMetadata(t *testing.T, dir string, metadata buildMetadata) {
	t.Helper()
	contents, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build-info.json"), append(contents, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseReceiptTimeRequiresUTCSecondPrecision(t *testing.T) {
	if _, err := parseReceiptTime("2026-09-06T10:00:00Z"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "2026-09-06T10:00:00.123Z", "2026-09-06T12:00:00+02:00"} {
		if _, err := parseReceiptTime(value); err == nil {
			t.Fatalf("invalid receipt time %q was accepted", value)
		}
	}
}
