package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunSmokeDescribesValidityWithoutClaimingNativeExecution(t *testing.T) {
	for _, test := range []struct {
		execution string
		want      string
	}{
		{execution: "native", want: "smoke report is valid for darwin/arm64 (native)\n"},
		{execution: "emulated", want: "smoke report is valid for darwin/arm64 (emulated); it does not satisfy native requirements\n"},
		{execution: "unknown", want: "smoke report is valid for darwin/arm64 (unknown); it does not satisfy native requirements\n"},
	} {
		t.Run(test.execution, func(t *testing.T) {
			dist, reportDir := validSmokeFixture(t, buildTarget{OS: "darwin", Arch: "arm64"})
			record := readSmokeRecordFixture(t, reportDir)
			record.Execution = test.execution
			writeSmokeRecordFixture(t, reportDir, record)

			var output bytes.Buffer
			err := runSmoke([]string{
				"verify",
				"--environment", "stage",
				"--dist", dist,
				"--report", reportDir,
			}, &output)
			if err != nil {
				t.Fatal(err)
			}
			if output.String() != test.want {
				t.Fatalf("unexpected smoke verification message:\n got %q\nwant %q", output.String(), test.want)
			}
		})
	}
}

func TestVerifySmokeReportBindsReceiptArchiveBinaryAndVersion(t *testing.T) {
	dist, reportDir := validSmokeFixture(t, buildTarget{OS: "darwin", Arch: "arm64"})
	evidence, err := verifySmokeReport(dist, "stage", reportDir)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Execution != "native" || evidence.Target != (buildTarget{OS: "darwin", Arch: "arm64"}) {
		t.Fatalf("unexpected evidence: %+v", evidence)
	}
}

func TestVerifySmokeReportRejectsFalseOrMixedEvidence(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, string, *smokeRecord)
	}{
		{name: "receipt digest", change: func(_ *testing.T, _ string, record *smokeRecord) { record.ReceiptSHA256 = strings.Repeat("0", 64) }},
		{name: "archive digest", change: func(_ *testing.T, _ string, record *smokeRecord) { record.ArchiveSHA256 = strings.Repeat("0", 64) }},
		{name: "binary digest", change: func(_ *testing.T, _ string, record *smokeRecord) { record.BinarySHA256 = strings.Repeat("0", 64) }},
		{name: "nonzero version", change: func(_ *testing.T, _ string, record *smokeRecord) { record.VersionExit = 7 }},
		{name: "timeout", change: func(_ *testing.T, _ string, record *smokeRecord) { record.HelpTimeout = true }},
		{name: "native contradiction", change: func(_ *testing.T, _ string, record *smokeRecord) { record.HostArch = "amd64" }},
		{name: "UTF16 output", change: func(t *testing.T, reportDir string, _ *smokeRecord) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(reportDir, "version.stdout"), []byte{0xff, 0xfe, '{', 0}, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "mixed release", change: func(t *testing.T, reportDir string, _ *smokeRecord) {
			t.Helper()
			var observed observedBuildInfo
			path := filepath.Join(reportDir, "version.stdout")
			contents, err := os.ReadFile(path)
			if err != nil || json.Unmarshal(contents, &observed) != nil {
				t.Fatal("read observed version")
			}
			observed.Commit = strings.Repeat("a", 40)
			changed, _ := json.Marshal(observed)
			if err := os.WriteFile(path, changed, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dist, reportDir := validSmokeFixture(t, buildTarget{OS: "darwin", Arch: "arm64"})
			record := readSmokeRecordFixture(t, reportDir)
			test.change(t, reportDir, &record)
			writeSmokeRecordFixture(t, reportDir, record)
			if _, err := verifySmokeReport(dist, "stage", reportDir); err == nil {
				t.Fatal("invalid smoke evidence was accepted")
			}
		})
	}
}

func TestInspectSmokeReportRetainsWellFormedFailedAttempt(t *testing.T) {
	dist, reportDir := validSmokeFixture(t, buildTarget{OS: "darwin", Arch: "arm64"})
	record := readSmokeRecordFixture(t, reportDir)
	record.VersionExit = 7
	writeSmokeRecordFixture(t, reportDir, record)
	writeTestFile(t, filepath.Join(reportDir, "version.stdout"), "")
	writeTestFile(t, filepath.Join(reportDir, "version.stderr"), "command failed\n")

	observation, err := inspectSmokeReport(dist, "stage", reportDir)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Successful {
		t.Fatal("failed attempt was counted as successful")
	}
	if _, err := verifySmokeReport(dist, "stage", reportDir); err == nil {
		t.Fatal("failed attempt passed smoke verification")
	}
}

func TestInspectSmokeReportRejectsMalformedSuccessfulOutput(t *testing.T) {
	dist, reportDir := validSmokeFixture(t, buildTarget{OS: "darwin", Arch: "arm64"})
	writeTestFile(t, filepath.Join(reportDir, "version.stdout"), "not-json\n")
	if _, err := inspectSmokeReport(dist, "stage", reportDir); err == nil {
		t.Fatal("malformed successful attempt was accepted")
	}
}

func TestInspectSmokeReportPreservesCaptureFailureSeparateFromExit(t *testing.T) {
	for _, exitCode := range []int{0, 7} {
		t.Run(fmt.Sprintf("exit-%d", exitCode), func(t *testing.T) {
			dist, reportDir := validSmokeFixture(t, buildTarget{OS: "windows", Arch: "amd64"})
			record := readSmokeRecordFixture(t, reportDir)
			record.VersionExit = exitCode
			record.VersionCaptureFailed = true
			writeSmokeRecordFixture(t, reportDir, record)
			if err := os.WriteFile(filepath.Join(reportDir, "version.stdout"), []byte{0xff, 0x00, '{'}, 0o600); err != nil {
				t.Fatal(err)
			}
			observation, err := inspectSmokeReport(dist, "stage", reportDir)
			if err != nil {
				t.Fatal(err)
			}
			if observation.Successful || observation.Evidence.VersionValid {
				t.Fatal("capture failure was selected as valid evidence")
			}
			stored := readSmokeRecordFixture(t, reportDir)
			if stored.VersionExit != exitCode || stored.VersionTimeout || !stored.VersionCaptureFailed {
				t.Fatalf("capture outcome was conflated: %+v", stored)
			}
		})
	}
}

func TestCollectSmokeEvidenceKeepsFailureAndSelectsValidRetry(t *testing.T) {
	dist, failedDir := validSmokeFixture(t, buildTarget{OS: "darwin", Arch: "arm64"})
	failedRecord := readSmokeRecordFixture(t, failedDir)
	failedRecord.VersionExit = 7
	writeSmokeRecordFixture(t, failedDir, failedRecord)
	if err := os.WriteFile(filepath.Join(failedDir, "version.stdout"), []byte{0xff, 0x00, 0xfe}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(failedDir, "version.stderr"), []byte{0x80, 0x00}, 0o600); err != nil {
		t.Fatal(err)
	}

	retryDir := filepath.Join(filepath.Dir(failedDir), "report-2")
	copyTestDirectory(t, failedDir, retryDir)
	retryRecord := readSmokeRecordFixture(t, retryDir)
	retryRecord.ReportID = "report-2"
	retryRecord.VersionExit = 0
	writeSmokeRecordFixture(t, retryDir, retryRecord)
	receipt, _, err := readEvidenceReceipt(dist)
	if err != nil {
		t.Fatal(err)
	}
	version := observedBuildInfo{
		Version: receipt.Version, Environment: receipt.Environment, Commit: receipt.Commit,
		SourceDate: receipt.SourceDate, BuildKind: receipt.BuildKind, GoVersion: receipt.GoVersion,
		OS: "darwin", Arch: "arm64", ServerURL: receipt.ServerURL,
	}
	versionBytes, _ := json.Marshal(version)
	writeTestFile(t, filepath.Join(retryDir, "version.stdout"), string(versionBytes)+"\n")
	writeTestFile(t, filepath.Join(retryDir, "version.stderr"), "")

	incomplete := filepath.Join(filepath.Dir(failedDir), "report-incomplete")
	if err := os.Mkdir(incomplete, 0o700); err != nil {
		t.Fatal(err)
	}
	evidence, err := collectNativeEvidence(dist, "stage")
	if err != nil {
		t.Fatal(err)
	}
	row := evidence.Targets[1]
	if row.Target != (buildTarget{OS: "darwin", Arch: "arm64"}) || row.Execution != "native" || row.Report == nil || row.Report.ReportID != "report-2" {
		t.Fatalf("successful retry was not selected: %+v", row)
	}
	if _, err := os.Stat(filepath.Join(failedDir, "record.env")); err != nil {
		t.Fatalf("failed diagnostic was removed: %v", err)
	}
}

func TestCollectSmokeEvidenceRejectsDuplicateReportIdentity(t *testing.T) {
	dist, reportDir := validSmokeFixture(t, buildTarget{OS: "darwin", Arch: "arm64"})
	duplicate := filepath.Join(filepath.Dir(filepath.Dir(reportDir)), "linux-amd64", filepath.Base(reportDir))
	copyTestDirectory(t, reportDir, duplicate)
	if _, err := collectNativeEvidence(dist, "stage"); err == nil {
		t.Fatal("duplicate report identity was accepted")
	}
}

func TestAcceptedReportAllowsCollectorIDsButRejectsTraversal(t *testing.T) {
	dist, oldDir := validSmokeFixture(t, buildTarget{OS: "darwin", Arch: "arm64"})
	reportDir := filepath.Join(filepath.Dir(oldDir), "report.DqjRHF")
	if err := os.Rename(oldDir, reportDir); err != nil {
		t.Fatal(err)
	}
	record := readSmokeRecordFixture(t, reportDir)
	record.ReportID = filepath.Base(reportDir)
	writeSmokeRecordFixture(t, reportDir, record)
	evidence, err := verifySmokeReport(dist, "stage", reportDir)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := acceptedReport(dist, reportDir, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAcceptedReport(dist, "stage", evidence.Target, accepted); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"..", ".hidden", "report..next", "report/next", "report\\next"} {
		if validReportID(invalid) {
			t.Fatalf("unsafe report ID %q was accepted", invalid)
		}
	}
}

func TestSmokeShellCollectorFeedsTypedValidator(t *testing.T) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("Unix collector integration runs in the pinned Linux test container")
	}
	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join(packageDir, "..", "..", "scripts", "smoke.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dist, receipt, metadata := completeReceiptFixture(t)
	target := buildTarget{OS: runtime.GOOS, Arch: runtime.GOARCH}
	archiveName := releaseArchiveName("stage", metadata.Version, target)
	binaryName := releaseBinaryName("stage", target.OS)
	observed := observedBuildInfo{
		Version: receipt.Version, Environment: receipt.Environment, Commit: receipt.Commit,
		SourceDate: receipt.SourceDate, BuildKind: receipt.BuildKind, GoVersion: receipt.GoVersion,
		OS: target.OS, Arch: target.Arch, ServerURL: receipt.ServerURL,
	}
	versionBytes, err := json.Marshal(observed)
	if err != nil || strings.ContainsRune(string(versionBytes), '\'') {
		t.Fatal("build smoke fixture version")
	}
	binary := fmt.Sprintf("#!/bin/sh\n[ -z \"${HOOKSPOT_DEV_CLI_KEY-}\" ] || exit 40\nif IFS= read -r input; then exit 41; fi\ncase \"$*\" in\n  'version --json') printf '%%s\\n' '%s' ;;\n  '--help') printf '%%s\\n' 'fixture help' ;;\n  *) exit 42 ;;\nesac\n", versionBytes)
	entries := validArchiveEntries(binaryName)
	entries[0].body = []byte(binary)
	archivePath := filepath.Join(dist, "artifacts", archiveName)
	writeTestArchive(t, archivePath, "tar.gz", entries, nil)
	archiveHash, err := hashBoundedFile(archivePath, defaultArtifactLimits.maxCompressedBytes)
	if err != nil {
		t.Fatal(err)
	}
	for index := range receipt.Artifacts {
		if receipt.Artifacts[index].Name == archiveName {
			receipt.Artifacts[index].SHA256 = archiveHash
		}
	}
	writeTestJSON(t, filepath.Join(dist, "receipt.json"), receipt)
	command := exec.Command("/bin/bash", script, "--dist", dist, "--environment", "stage", "--target", target.OS+"/"+target.Arch)
	command.Env = append(os.Environ(), "HOOKSPOT_DEV_CLI_KEY=TOKEN_BOUNDARY_SENTINEL")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke collector failed: %v: %s", err, output)
	}
	reportEntries, err := os.ReadDir(filepath.Join(dist, "native", "reports", target.OS+"-"+target.Arch))
	if err != nil || len(reportEntries) != 1 {
		t.Fatalf("find smoke report: %v", err)
	}
	reportDir := filepath.Join(dist, "native", "reports", target.OS+"-"+target.Arch, reportEntries[0].Name())
	if _, err := verifySmokeReport(dist, "stage", reportDir); err != nil {
		t.Fatal(err)
	}
}

func TestCreateNativeEvidenceFreezesSelectedReportAndRequiredManualCheck(t *testing.T) {
	dist, reports := completeFirstSupportFixture(t)
	reportDir := reports["darwin/arm64"]
	requirements, err := readNativeRequirements(dist)
	if err != nil {
		t.Fatal(err)
	}
	requirements.Targets[1].RequiredChecks = []string{"version", "help", "config-private"}
	writeTestJSON(t, filepath.Join(dist, "native", "requirements.json"), requirements)
	createManualFixture(t, dist, buildTarget{OS: "darwin", Arch: "arm64"}, "config-private", "pass", "native-private-config-v1")

	if err := createNativeEvidence(dist); err != nil {
		t.Fatal(err)
	}
	evidence, err := readNativeEvidence(dist)
	if err != nil {
		t.Fatal(err)
	}
	row := evidence.Targets[1]
	if row.Report == nil || row.Report.ReportID != filepath.Base(reportDir) || row.Execution != "native" || len(row.ManualChecks) != 1 || row.ManualChecks[0].Check != "config-private" {
		t.Fatalf("unexpected frozen evidence: %+v", row)
	}
	if err := validateNativeEvidence(dist, requirements, evidence); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(reportDir, "help.stdout"), "changed\n")
	if err := validateNativeEvidence(dist, requirements, evidence); err == nil {
		t.Fatal("changed frozen smoke output was accepted")
	}
}

func TestCreateNativeEvidenceDoesNotWriteUntilRequirementsPass(t *testing.T) {
	dist, reports := completeFirstSupportFixture(t)
	reportDir := reports["darwin/arm64"]
	record := readSmokeRecordFixture(t, reportDir)
	record.VersionExit = 9
	writeSmokeRecordFixture(t, reportDir, record)
	writeTestFile(t, filepath.Join(reportDir, "version.stdout"), "")
	writeTestFile(t, filepath.Join(reportDir, "version.stderr"), "failed\n")
	if err := createNativeEvidence(dist); err == nil {
		t.Fatal("failed smoke attempt satisfied requirements")
	}
	if _, err := os.Stat(filepath.Join(dist, "native", "evidence.json")); !os.IsNotExist(err) {
		t.Fatal("evidence was written before requirements passed")
	}
}

func TestManualEvidenceFailedAttemptDoesNotBlockPassingRetry(t *testing.T) {
	dist, _ := completeFirstSupportFixture(t)
	requirements, err := readNativeRequirements(dist)
	if err != nil {
		t.Fatal(err)
	}
	requirements.Targets[1].RequiredChecks = []string{"version", "help", "signal-cancel"}
	writeTestJSON(t, filepath.Join(dist, "native", "requirements.json"), requirements)
	failed := createManualFixture(t, dist, buildTarget{OS: "darwin", Arch: "arm64"}, "signal-cancel", "fail", "native-signal-v1")
	passing := createManualFixture(t, dist, buildTarget{OS: "darwin", Arch: "arm64"}, "signal-cancel", "pass", "native-signal-v1")

	if err := createNativeEvidence(dist); err != nil {
		t.Fatal(err)
	}
	evidence, err := readNativeEvidence(dist)
	if err != nil {
		t.Fatal(err)
	}
	manual := evidence.Targets[1].ManualChecks
	if len(manual) != 1 || manual[0].Path != passing {
		t.Fatalf("passing retry was not selected: %+v", manual)
	}
	if _, err := os.Stat(filepath.Join(dist, filepath.FromSlash(failed))); err != nil {
		t.Fatalf("failed manual diagnostic was removed: %v", err)
	}
}

func TestManualEvidenceRejectsChangedArchiveWithSameBinary(t *testing.T) {
	target := buildTarget{OS: "darwin", Arch: "arm64"}
	dist, _ := validSmokeFixture(t, target)
	relative := createManualFixture(t, dist, target, "config-private", "pass", "native-private-config-v1")
	receipt, _, err := readEvidenceReceipt(dist)
	if err != nil {
		t.Fatal(err)
	}
	artifact, ok := receiptArtifact(receipt, target)
	if !ok {
		t.Fatal("missing fixture archive")
	}
	entries := validArchiveEntries(releaseBinaryName("stage", target.OS))
	entries[1].body = []byte("changed readme")
	writeTestArchive(t, filepath.Join(dist, "artifacts", artifact.Name), "tar.gz", entries, nil)
	if _, _, err := readManualCheckRecord(dist, relative, "stage", target); err == nil {
		t.Fatal("manual record accepted changed archive bytes")
	}
	if _, _, err := newManualCheckRecord(dist, target, "config-private", "pass", "fixture-operator", "native-private-config-v1"); err == nil {
		t.Fatal("manual producer accepted changed archive bytes")
	}
}

func TestAcceptedManualEvidenceRejectsNoncanonicalPath(t *testing.T) {
	target := buildTarget{OS: "darwin", Arch: "arm64"}
	dist, _ := validSmokeFixture(t, target)
	relative := createManualFixture(t, dist, target, "config-private", "pass", "native-private-config-v1")
	record, hash, err := readManualCheckRecord(dist, relative, "stage", target)
	if err != nil {
		t.Fatal(err)
	}
	accepted := acceptedManualCheck{
		ReportID: record.ReportID, Path: relative, RecordSHA256: hash,
		ArchiveName: record.ArchiveName, ArchiveSHA256: record.ArchiveSHA256,
		BinaryName: record.BinaryName, BinarySHA256: record.BinarySHA256,
		Check: record.Check, Procedure: record.Procedure, Result: record.Result, Operator: record.Operator,
	}
	accepted.Path = filepath.ToSlash(filepath.Join("native", "manual", "darwin-arm64", "..", "..", "manual-other", record.ReportID+".json"))
	if err := validateAcceptedManualCheck(dist, target, accepted); err == nil {
		t.Fatal("noncanonical selected manual path was accepted")
	}
}

func TestValidateExecutionClassification(t *testing.T) {
	base := smokeRecord{TargetOS: "linux", TargetArch: "arm64", CollectorOS: "linux", CollectorArch: "arm64", HostOS: "linux", HostArch: "arm64", HostKind: "vm", Provenance: "docker-daemon", Operator: "unknown", Procedure: "unknown", Execution: "native", DaemonArch: "arm64", RequestedPlatform: "linux/arm64", CollectorTranslated: "unknown"}
	if err := validateExecutionClassification(base); err != nil {
		t.Fatal(err)
	}
	qemu := base
	qemu.TargetArch = "amd64"
	qemu.CollectorArch = "amd64"
	qemu.RequestedPlatform = "linux/amd64"
	qemu.Execution = "emulated"
	if err := validateExecutionClassification(qemu); err != nil {
		t.Fatal(err)
	}
	qemu.Execution = "native"
	if err := validateExecutionClassification(qemu); err == nil {
		t.Fatal("QEMU-shaped evidence was accepted as native")
	}
	unknown := base
	unknown.Execution = "unknown"
	unknown.HostOS = "unknown"
	unknown.HostArch = "unknown"
	unknown.HostKind = "unknown"
	unknown.Provenance = "standalone"
	unknown.DaemonArch = "unknown"
	if err := validateExecutionClassification(unknown); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*smokeRecord){
		func(record *smokeRecord) { record.Provenance = "made-up" },
		func(record *smokeRecord) { record.HostKind = "made-up" },
		func(record *smokeRecord) { record.DaemonArch = "unknown" },
	} {
		invalid := base
		change(&invalid)
		if err := validateExecutionClassification(invalid); err == nil {
			t.Fatal("unsupported native provenance was accepted")
		}
	}
	operator := base
	operator.HostKind = "physical"
	operator.Provenance = "operator"
	operator.DaemonArch = "unknown"
	operator.Operator = "release-operator"
	operator.Procedure = "native-linux-arm64-v1"
	if err := validateExecutionClassification(operator); err != nil {
		t.Fatal(err)
	}
	operator.Procedure = "unknown"
	if err := validateExecutionClassification(operator); err == nil {
		t.Fatal("operator provenance without a procedure was accepted")
	}
	rosetta := base
	rosetta.TargetOS = "darwin"
	rosetta.CollectorOS = "darwin"
	rosetta.HostOS = "darwin"
	rosetta.HostKind = "physical"
	rosetta.Provenance = "operator"
	rosetta.Operator = "release-operator"
	rosetta.Procedure = "native-mac-arm64-v1"
	rosetta.DaemonArch = "unknown"
	rosetta.CollectorArch = "amd64"
	rosetta.RequestedPlatform = "darwin/arm64"
	rosetta.CollectorTranslated = "true"
	if err := validateExecutionClassification(rosetta); err != nil {
		t.Fatalf("translated collector launching native thin child was rejected: %v", err)
	}
}

func TestSmokeRecordAcceptsSignedWindowsExitAndRejectsNULText(t *testing.T) {
	dist, reportDir := validSmokeFixture(t, buildTarget{OS: "windows", Arch: "amd64"})
	record := readSmokeRecordFixture(t, reportDir)
	record.VersionExit = -1073741515
	writeSmokeRecordFixture(t, reportDir, record)
	writeTestFile(t, filepath.Join(reportDir, "version.stdout"), "")
	writeTestFile(t, filepath.Join(reportDir, "version.stderr"), "failed\n")
	if observation, err := inspectSmokeReport(dist, "stage", reportDir); err != nil || observation.Successful {
		t.Fatalf("signed Windows failure was not retained: %+v, %v", observation, err)
	}
	writeTestFile(t, filepath.Join(reportDir, "help.stdout"), "U\x00s\x00a\x00g\x00e\x00")
	if _, err := inspectSmokeReport(dist, "stage", reportDir); err == nil {
		t.Fatal("NUL-interleaved help output was accepted")
	}
}

func TestNativeRequirementsDefaultToAllSixFirstSupportTargets(t *testing.T) {
	dist, _, _ := completeReceiptFixture(t)
	if err := createNativeRequirements(nativeRequirementsOptions{dist: dist}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(dist, "native", "requirements.json"))
	if err != nil {
		t.Fatal(err)
	}
	var requirements nativeRequirements
	if err := json.Unmarshal(contents, &requirements); err != nil {
		t.Fatal(err)
	}
	if requirements.Mode != "first-support" || len(requirements.Targets) != len(releaseTargets) {
		t.Fatalf("unexpected requirements: %+v", requirements)
	}
	for index, target := range requirements.Targets {
		want := "version,help"
		if target.Target.OS == "windows" {
			want += ",config-private,password-input,signal-cancel"
		}
		if target.Target.OS+"/"+target.Target.Arch == requirementsFixtureBuilderPlatform(t, dist) {
			want += ",network"
		}
		if target.Target != releaseTargets[index] || strings.Join(target.RequiredChecks, ",") != want || target.Reason != "first-support" {
			t.Fatalf("unexpected target requirements: %+v", target)
		}
	}
}

func TestNativeReviewTemplateIsIncompleteAndIdentityBound(t *testing.T) {
	baselineDist, _ := completeFirstSupportFixture(t)
	if err := createNativeEvidence(baselineDist); err != nil {
		t.Fatal(err)
	}
	currentDist, current, _ := completeReceiptFixture(t)
	outputDir := t.TempDir()
	output := filepath.Join(outputDir, "routine-review.json")
	for _, name := range []string{"equal", "baseline-inside-current", "current-inside-baseline"} {
		t.Run("reject-retained-overlap-"+name, func(t *testing.T) {
			paths := [2]string{currentDist, currentDist}
			if name == "baseline-inside-current" {
				paths[0] = t.TempDir()
				paths[1] = filepath.Join(paths[0], "nested-baseline")
			} else if name == "current-inside-baseline" {
				paths[1] = t.TempDir()
				paths[0] = filepath.Join(paths[1], "nested-current")
			}
			if err := os.MkdirAll(paths[0], 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(paths[1], 0o700); err != nil {
				t.Fatal(err)
			}
			candidate := filepath.Join(outputDir, name+".json")
			err := runNativeReviewTemplate([]string{"--dist", paths[0], "--baseline-dist", paths[1], "--output", candidate}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "current and baseline retained trees must be disjoint") {
				t.Fatalf("retained overlap error = %v", err)
			}
			if _, err := os.Lstat(candidate); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected overlap created output: %v", err)
			}
		})
	}
	inputAlias := filepath.Join(t.TempDir(), "current-alias")
	if err := os.Symlink(currentDist, inputAlias); err != nil {
		t.Fatal(err)
	}
	if err := runNativeReviewTemplate([]string{"--dist", inputAlias, "--baseline-dist", baselineDist, "--output", output}, io.Discard); err == nil {
		t.Fatal("symlinked retained input was accepted")
	}
	for name, retained := range map[string]string{"current": currentDist, "baseline": baselineDist} {
		t.Run("reject-output-inside-"+name, func(t *testing.T) {
			inside := filepath.Join(retained, "native", "review-template.json")
			if err := runNativeReviewTemplate([]string{"--dist", currentDist, "--baseline-dist", baselineDist, "--output", inside}, io.Discard); err == nil {
				t.Fatal("output inside retained tree was accepted")
			}
		})
	}
	t.Run("reject-parent-traversal-output", func(t *testing.T) {
		rawOutput, resolvedOutput := parentTraversalTestPath(t, "review.json")
		err := runNativeReviewTemplate([]string{"--dist", currentDist, "--baseline-dist", baselineDist, "--output", rawOutput}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "parent traversal") {
			t.Fatalf("parent-traversal output error = %v", err)
		}
		if _, err := os.Lstat(resolvedOutput); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rejected parent-traversal output was created: %v", err)
		}
	})
	t.Run("reject-output-ancestor", func(t *testing.T) {
		ancestor := filepath.Join(filepath.Dir(currentDist), "review-template.json")
		if err := runNativeReviewTemplate([]string{"--dist", currentDist, "--baseline-dist", baselineDist, "--output", ancestor}, io.Discard); err == nil {
			t.Fatal("output ancestor of retained tree was accepted")
		}
	})
	var message bytes.Buffer
	if err := runNativeReviewTemplate([]string{"--dist", currentDist, "--baseline-dist", baselineDist, "--output", output}, &message); err != nil {
		t.Fatalf("create review template: %v", err)
	}
	if !strings.Contains(message.String(), "remains incomplete") {
		t.Fatalf("template output did not explain its incomplete state: %q", message.String())
	}
	contents, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var review nativeReview
	if err := json.Unmarshal(contents, &review); err != nil {
		t.Fatal(err)
	}
	if review.SourceRepository != current.SourceRepository || review.Environment != current.Environment ||
		review.BaselineCommit == "" || review.CurrentCommit != current.Commit || review.CreatedAt != "" || review.Operator != "" || review.Rationale != "" {
		t.Fatalf("template identity or incomplete fields are wrong: %+v", review)
	}
	if len(review.Targets) != len(releaseTargets) {
		t.Fatalf("template target count = %d, want %d", len(review.Targets), len(releaseTargets))
	}
	for _, row := range review.Targets {
		if row.Available || !row.Affected || row.Reason != "" || strings.Join(row.RequiredChecks, "\x00") != strings.Join(nativeCheckNames, "\x00") {
			t.Fatalf("template target is not conservative: %+v", row)
		}
	}
	if err := runNativeReviewTemplate([]string{"--dist", currentDist, "--baseline-dist", baselineDist, "--output", output}, &message); err == nil {
		t.Fatal("existing review template was replaced")
	}
	if err := createNativeRequirements(nativeRequirementsOptions{dist: currentDist, review: output, baselineDist: baselineDist}); err == nil {
		t.Fatal("unchanged incomplete template reached routine requirements")
	}
	var completed nativeReview
	if err := json.Unmarshal(contents, &completed); err != nil {
		t.Fatal(err)
	}
	completed.CreatedAt, completed.Operator, completed.Rationale = "2026-01-01T00:00:00Z", "operator", "reviewed"
	for i := range completed.Targets {
		completed.Targets[i].Available = true
		completed.Targets[i].Affected = false
		completed.Targets[i].Reason = "reviewed"
	}
	completedBytes, err := json.Marshal(completed)
	if err != nil {
		t.Fatal(err)
	}
	completedPath := filepath.Join(outputDir, "completed-review.json")
	if err := os.WriteFile(completedPath, completedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := createNativeRequirements(nativeRequirementsOptions{dist: currentDist, review: completedPath, baselineDist: baselineDist}); err != nil {
		t.Fatalf("completed template was rejected: %v", err)
	}
}

func TestNativeReviewTemplateRejectsInvalidBaselines(t *testing.T) {
	for _, mode := range []string{"malformed-mode", "nested", "incomplete-evidence", "missing-evidence"} {
		t.Run(mode, func(t *testing.T) {
			baseline, _ := completeFirstSupportFixture(t)
			if err := createNativeEvidence(baseline); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "malformed-mode":
				path := filepath.Join(baseline, "native", "requirements.json")
				requirements, err := readNativeRequirements(baseline)
				if err != nil {
					t.Fatal(err)
				}
				requirements.Mode = "routine"
				b, err := json.Marshal(requirements)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, b, 0o600); err != nil {
					t.Fatal(err)
				}
			case "nested":
				if err := os.MkdirAll(filepath.Join(baseline, "native", "baseline"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "incomplete-evidence":
				evidence, err := readNativeEvidence(baseline)
				if err != nil {
					t.Fatal(err)
				}
				evidence.Targets[0].Report = nil
				evidence.Targets[0].Execution = "unverified"
				b, err := json.Marshal(evidence)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(baseline, "native", "evidence.json"), b, 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing-evidence":
				if err := os.Remove(filepath.Join(baseline, "native", "evidence.json")); err != nil {
					t.Fatal(err)
				}
			}
			current, _, _ := completeReceiptFixture(t)
			if err := runNativeReviewTemplate([]string{"--dist", current, "--baseline-dist", baseline, "--output", filepath.Join(t.TempDir(), "review.json")}, io.Discard); err == nil {
				t.Fatal("invalid baseline was accepted")
			}
		})
	}
	t.Run("reject-symlink-output-parent", func(t *testing.T) {
		baseline, _ := completeFirstSupportFixture(t)
		if err := createNativeEvidence(baseline); err != nil {
			t.Fatal(err)
		}
		current, _, _ := completeReceiptFixture(t)
		realParent := t.TempDir()
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(realParent, alias); err != nil {
			t.Fatal(err)
		}
		if err := runNativeReviewTemplate([]string{"--dist", current, "--baseline-dist", baseline, "--output", filepath.Join(alias, "review.json")}, io.Discard); err == nil {
			t.Fatal("symlinked output parent was accepted")
		}
	})
}

func TestNativeRequirementsConsumerRejectsRemovedPolicyChecks(t *testing.T) {
	dist, _, _ := completeReceiptFixture(t)
	if err := createNativeRequirements(nativeRequirementsOptions{dist: dist}); err != nil {
		t.Fatal(err)
	}
	requirements, err := readNativeRequirements(dist)
	if err != nil {
		t.Fatal(err)
	}
	for index := range requirements.Targets {
		requirements.Targets[index].RequiredChecks = nil
	}
	if err := validateNativeRequirements(dist, requirements); err == nil {
		t.Fatal("first-support policy checks were removable")
	}
}

func TestNativeRequirementsConsumerRejectsRoutineWithoutReviewOrBaseline(t *testing.T) {
	dist, _, _ := completeReceiptFixture(t)
	if err := createNativeRequirements(nativeRequirementsOptions{dist: dist}); err != nil {
		t.Fatal(err)
	}
	requirements, err := readNativeRequirements(dist)
	if err != nil {
		t.Fatal(err)
	}
	requirements.Mode = "routine"
	if err := validateNativeRequirements(dist, requirements); err == nil {
		t.Fatal("routine requirements without concrete review and baseline were accepted")
	}
}

func TestNativeEvidenceRejectsLocalDiagnosticNetworkProcedure(t *testing.T) {
	dist, _ := completeFirstSupportFixture(t)
	requirements, err := readNativeRequirements(dist)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range requirements.Targets {
		for _, check := range row.RequiredChecks {
			if check != "network" {
				continue
			}
			manualRoot := filepath.Join(dist, "native", "manual", row.Target.OS+"-"+row.Target.Arch)
			entries, err := os.ReadDir(manualRoot)
			if err != nil || len(entries) != 1 {
				t.Fatalf("network manual records = %v, want one record", err)
			}
			path := filepath.Join(manualRoot, entries[0].Name())
			var record manualCheckRecord
			contents, err := os.ReadFile(path)
			if err != nil || json.Unmarshal(contents, &record) != nil {
				t.Fatal("read network manual record")
			}
			record.Procedure = "local-tls-phoenix-v1"
			writeTestJSON(t, path, record)
		}
	}
	if err := createNativeEvidence(dist); err == nil || !strings.Contains(err.Error(), "network_release_v1") {
		t.Fatalf("local diagnostic procedure error = %v, want canonical network procedure", err)
	}
}

func TestCanonicalManualProcedure(t *testing.T) {
	for _, test := range []struct {
		name      string
		target    buildTarget
		check     string
		procedure string
		required  bool
	}{
		{name: "network-linux", target: buildTarget{OS: "linux", Arch: "amd64"}, check: "network", procedure: "network_release_v1", required: true},
		{name: "network-linux-arm64", target: buildTarget{OS: "linux", Arch: "arm64"}, check: "network", procedure: "network_release_v1", required: true},
		{name: "network-darwin-amd64", target: buildTarget{OS: "darwin", Arch: "amd64"}, check: "network", procedure: "network_release_v1", required: true},
		{name: "network-darwin-arm64", target: buildTarget{OS: "darwin", Arch: "arm64"}, check: "network", procedure: "network_release_v1", required: true},
		{name: "network-windows-amd64", target: buildTarget{OS: "windows", Arch: "amd64"}, check: "network", procedure: "network_release_v1", required: true},
		{name: "network-windows-arm64", target: buildTarget{OS: "windows", Arch: "arm64"}, check: "network", procedure: "network_release_v1", required: true},
		{name: "config-windows-amd64", target: buildTarget{OS: "windows", Arch: "amd64"}, check: "config-private", procedure: "windows_config_private_v1", required: true},
		{name: "config-windows-arm64", target: buildTarget{OS: "windows", Arch: "arm64"}, check: "config-private", procedure: "windows_config_private_v1", required: true},
		{name: "password-windows-amd64", target: buildTarget{OS: "windows", Arch: "amd64"}, check: "password-input", procedure: "windows_password_input_v1", required: true},
		{name: "password-windows-arm64", target: buildTarget{OS: "windows", Arch: "arm64"}, check: "password-input", procedure: "windows_password_input_v1", required: true},
		{name: "signal-windows-amd64", target: buildTarget{OS: "windows", Arch: "amd64"}, check: "signal-cancel", procedure: "windows_signal_cancel_v1", required: true},
		{name: "signal-windows-arm64", target: buildTarget{OS: "windows", Arch: "arm64"}, check: "signal-cancel", procedure: "windows_signal_cancel_v1", required: true},
		{name: "config-darwin", target: buildTarget{OS: "darwin", Arch: "arm64"}, check: "config-private", procedure: "", required: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			procedure, required := canonicalManualProcedure(test.target, test.check)
			if procedure != test.procedure || required != test.required {
				t.Fatalf("canonicalManualProcedure() = %q, %v; want %q, %v", procedure, required, test.procedure, test.required)
			}
		})
	}
}

func TestNativeEvidenceSelectsCanonicalProcedureAfterDiagnostic(t *testing.T) {
	dist, _ := completeFirstSupportFixture(t)
	requirements, err := readNativeRequirements(dist)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range requirements.Targets {
		for _, check := range row.RequiredChecks {
			if check != "network" {
				continue
			}
			manualRoot := filepath.Join(dist, "native", "manual", row.Target.OS+"-"+row.Target.Arch)
			entries, err := os.ReadDir(manualRoot)
			if err != nil || len(entries) != 1 {
				t.Fatalf("network manual records = %v, want one record", err)
			}
			contents, err := os.ReadFile(filepath.Join(manualRoot, entries[0].Name()))
			if err != nil {
				t.Fatal(err)
			}
			var diagnostic manualCheckRecord
			if err := json.Unmarshal(contents, &diagnostic); err != nil {
				t.Fatal(err)
			}
			diagnostic.ReportID = "manual-0000"
			diagnostic.Procedure = "local-tls-phoenix-v1"
			writeTestJSON(t, filepath.Join(manualRoot, diagnostic.ReportID+".json"), diagnostic)
		}
	}
	if err := createNativeEvidence(dist); err != nil {
		t.Fatalf("canonical procedure was not selected: %v", err)
	}
	evidence, err := readNativeEvidence(dist)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range evidence.Targets {
		for _, manual := range row.ManualChecks {
			if manual.Check == "network" && manual.Procedure != "network_release_v1" {
				t.Fatalf("selected network procedure = %q", manual.Procedure)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dist, "native", "manual", "linux-amd64", "manual-0000.json")); err != nil {
		t.Fatalf("diagnostic network record was not retained: %v", err)
	}
	diagnostic, _, err := readManualCheckRecord(dist, filepath.ToSlash(filepath.Join("native", "manual", "linux-amd64", "manual-0000.json")), "stage", buildTarget{OS: "linux", Arch: "amd64"})
	if err != nil || diagnostic.Procedure != "local-tls-phoenix-v1" {
		t.Fatalf("diagnostic network procedure was not retained: %+v, %v", diagnostic, err)
	}
}

func TestNativeEvidenceRevalidatesSelectedProcedure(t *testing.T) {
	dist, _ := completeFirstSupportFixture(t)
	if err := createNativeEvidence(dist); err != nil {
		t.Fatal(err)
	}
	requirements, err := readNativeRequirements(dist)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := readNativeEvidence(dist)
	if err != nil {
		t.Fatal(err)
	}
	for index := range evidence.Targets {
		for manualIndex := range evidence.Targets[index].ManualChecks {
			if evidence.Targets[index].ManualChecks[manualIndex].Check == "network" {
				evidence.Targets[index].ManualChecks[manualIndex].Procedure = "local-tls-phoenix-v1"
			}
		}
	}
	if err := validateNativeEvidence(dist, requirements, evidence); err == nil || !strings.Contains(err.Error(), "network_release_v1") {
		t.Fatalf("changed selected procedure error = %v, want canonical network procedure", err)
	}
}

func TestNativeEvidenceRejectsWrongSmokeProcedureThenAcceptsCanonical(t *testing.T) {
	dist, reports := completeFirstSupportFixture(t)
	reportDir := reports["darwin/arm64"]
	record := readSmokeRecordFixture(t, reportDir)
	record.Procedure = "local-tls-phoenix-v1"
	writeSmokeRecordFixture(t, reportDir, record)
	observation, err := inspectSmokeReport(dist, "stage", reportDir)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := acceptedReport(dist, reportDir, observation.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAcceptedReport(dist, "stage", buildTarget{OS: "darwin", Arch: "arm64"}, accepted); err == nil || !strings.Contains(err.Error(), "native_release_v1") {
		t.Fatalf("wrong smoke procedure revalidation error = %v, want canonical smoke procedure", err)
	}
	evidence, err := collectNativeEvidence(dist, "stage")
	if err != nil {
		t.Fatal(err)
	}
	requirements, err := readNativeRequirements(dist)
	if err != nil {
		t.Fatal(err)
	}
	manual, err := collectManualEvidence(dist, "stage")
	if err != nil {
		t.Fatal(err)
	}
	for index := range evidence.Targets {
		evidence.Targets[index].ManualChecks = manual[index]
	}
	if err := validateNativeEvidence(dist, requirements, evidence); err == nil || !strings.Contains(err.Error(), "native_release_v1") {
		t.Fatalf("wrong smoke procedure error = %v, want canonical smoke procedure", err)
	}
	canonicalDir := filepath.Join(filepath.Dir(reportDir), "report-canonical")
	copyTestDirectory(t, reportDir, canonicalDir)
	canonicalRecord := readSmokeRecordFixture(t, canonicalDir)
	canonicalRecord.ReportID = "report-canonical"
	canonicalRecord.Procedure = "native_release_v1"
	writeSmokeRecordFixture(t, canonicalDir, canonicalRecord)
	evidence, err = collectNativeEvidence(dist, "stage")
	if err != nil {
		t.Fatal(err)
	}
	for index := range evidence.Targets {
		evidence.Targets[index].ManualChecks = manual[index]
	}
	if err := validateNativeEvidence(dist, requirements, evidence); err != nil {
		t.Fatalf("canonical smoke procedure was rejected: %v", err)
	}
	if selected := evidence.Targets[1].Report; selected == nil || selected.ReportID != "report-canonical" {
		t.Fatalf("canonical smoke retry was not selected: %+v", selected)
	}
	if retained := readSmokeRecordFixture(t, reportDir); retained.Procedure != "local-tls-phoenix-v1" {
		t.Fatalf("wrong smoke diagnostic changed procedure to %q", retained.Procedure)
	}
}

func TestRoutineRequirementsRetainAndValidateConcreteBaseline(t *testing.T) {
	baselineDist, _ := completeFirstSupportFixture(t)
	if err := createNativeEvidence(baselineDist); err != nil {
		t.Fatal(err)
	}
	currentDist, current, _ := completeReceiptFixture(t)
	reviewPath := writeRoutineReviewFixture(t, currentDist, current, baselineDist)
	if err := createNativeRequirements(nativeRequirementsOptions{dist: currentDist, review: reviewPath, baselineDist: baselineDist}); err != nil {
		t.Fatal(err)
	}
	requirements, err := readNativeRequirements(currentDist)
	if err != nil {
		t.Fatal(err)
	}
	if requirements.Mode != "routine" || requirements.BaselineVersion == "" || requirements.ReviewSHA256 == "" {
		t.Fatalf("routine identity was not retained: %+v", requirements)
	}
	for _, path := range []string{
		filepath.Join("native", "review.json"),
		filepath.Join("native", "baseline", "receipt.json"),
		filepath.Join("native", "baseline", "native", "requirements.json"),
		filepath.Join("native", "baseline", "native", "evidence.json"),
	} {
		if _, err := os.Stat(filepath.Join(currentDist, path)); err != nil {
			t.Fatalf("missing retained routine input %s: %v", path, err)
		}
	}
	writeTestFile(t, filepath.Join(currentDist, "native", "review.json"), "{}\n")
	if _, err := readNativeRequirements(currentDist); err == nil {
		t.Fatal("changed retained review was accepted")
	}
}

func TestRoutineRequirementsSummaryMatchesRetainedReview(t *testing.T) {
	baselineDist, _ := completeFirstSupportFixture(t)
	if err := createNativeEvidence(baselineDist); err != nil {
		t.Fatal(err)
	}
	currentDist, current, _ := completeReceiptFixture(t)
	reviewPath := writeRoutineReviewFixture(t, currentDist, current, baselineDist)
	if err := createNativeRequirements(nativeRequirementsOptions{
		dist: currentDist, review: reviewPath, baselineDist: baselineDist,
	}); err != nil {
		t.Fatal(err)
	}
	requirements, err := readNativeRequirements(currentDist)
	if err != nil {
		t.Fatalf("positive control: %v", err)
	}
	for _, field := range []string{"operator", "rationale"} {
		t.Run(field, func(t *testing.T) {
			changed := requirements
			if field == "operator" {
				changed.ReviewOperator = "different-operator"
			} else {
				changed.ReviewRationale = "different cumulative review rationale"
			}
			if err := validateNativeRequirements(currentDist, changed); err == nil {
				t.Fatal("routine summary accepted a different " + field + " from the retained review")
			}
		})
	}
}

func TestRoutineRequirementsRejectNestedOrWrongScopeBaseline(t *testing.T) {
	baselineDist, _ := completeFirstSupportFixture(t)
	if err := createNativeEvidence(baselineDist); err != nil {
		t.Fatal(err)
	}
	currentDist, current, _ := completeReceiptFixture(t)
	baseline, _, err := readEvidenceReceipt(baselineDist)
	if err != nil {
		t.Fatal(err)
	}
	wrongScope := baseline
	wrongScope.SourceRepository = "other/repository"
	if sameNativeBaselineScope(current, wrongScope) {
		t.Fatal("different repository was accepted as a routine baseline")
	}
	wrongScope = baseline
	wrongScope.Environment = "prod"
	if sameNativeBaselineScope(current, wrongScope) {
		t.Fatal("different environment was accepted as a routine baseline")
	}
	if err := os.MkdirAll(filepath.Join(baselineDist, "native", "baseline"), 0o700); err != nil {
		t.Fatal(err)
	}
	reviewPath := writeRoutineReviewFixture(t, currentDist, current, baselineDist)
	if err := createNativeRequirements(nativeRequirementsOptions{dist: currentDist, review: reviewPath, baselineDist: baselineDist}); err == nil {
		t.Fatal("nested native baseline was accepted")
	}
}

func TestRoutineRequirementsRejectUnsafeInputsBeforeWriting(t *testing.T) {
	newInputs := func(t *testing.T) (nativeRequirementsOptions, buildReceipt, []byte) {
		t.Helper()
		baselineDist, _ := completeFirstSupportFixture(t)
		if err := createNativeEvidence(baselineDist); err != nil {
			t.Fatal(err)
		}
		currentDist, current, _ := completeReceiptFixture(t)
		_, currentBytes, err := readEvidenceReceipt(currentDist)
		if err != nil {
			t.Fatal(err)
		}
		review := writeRoutineReviewFixture(t, currentDist, current, baselineDist)
		return nativeRequirementsOptions{dist: currentDist, review: review, baselineDist: baselineDist}, current, currentBytes
	}

	for _, name := range []string{"equal", "baseline-inside-current", "current-inside-baseline", "review-inside-current", "symlink-review-parent", "parent-traversal-review"} {
		t.Run(name, func(t *testing.T) {
			options, current, currentBytes := newInputs(t)
			switch name {
			case "equal":
				options.baselineDist = options.dist
			case "baseline-inside-current":
				options.baselineDist = filepath.Join(options.dist, "nested-baseline")
				if err := os.Mkdir(options.baselineDist, 0o700); err != nil {
					t.Fatal(err)
				}
			case "current-inside-baseline":
				options.dist = filepath.Join(options.baselineDist, "nested-current")
				if err := os.Mkdir(options.dist, 0o700); err != nil {
					t.Fatal(err)
				}
			case "review-inside-current":
				contents, err := os.ReadFile(options.review)
				if err != nil {
					t.Fatal(err)
				}
				options.review = filepath.Join(options.dist, "routine-review.json")
				if err := os.WriteFile(options.review, contents, 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink-review-parent":
				realParent := filepath.Dir(options.review)
				alias := filepath.Join(t.TempDir(), "review-parent")
				if err := os.Symlink(realParent, alias); err != nil {
					t.Fatal(err)
				}
				options.review = filepath.Join(alias, filepath.Base(options.review))
			case "parent-traversal-review":
				rawReview, resolvedReview := parentTraversalTestPath(t, "review.json")
				contents, err := os.ReadFile(options.review)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(resolvedReview, contents, 0o600); err != nil {
					t.Fatal(err)
				}
				options.review = rawReview
			}
			currentBefore := snapshotTestTree(t, options.dist)
			baselineBefore := snapshotTestTree(t, options.baselineDist)
			err := createRoutineRequirements(options, current, currentBytes)
			want := "routine inputs must not expose the current retained tree as writable"
			if name == "symlink-review-parent" {
				want = "native review parent is unsafe"
			} else if name == "parent-traversal-review" {
				want = "native review path is unsafe: path must not contain parent traversal"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("unsafe routine input error = %v, want %q", err, want)
			}
			assertTestTreeUnchanged(t, options.dist, currentBefore)
			assertTestTreeUnchanged(t, options.baselineDist, baselineBefore)
		})
	}
}

func TestValidateExistingDirectoryPathRejectsRawParentTraversal(t *testing.T) {
	raw, resolved := parentTraversalTestPath(t, "directory")
	if err := os.Mkdir(resolved, 0o700); err != nil {
		t.Fatal(err)
	}
	err := validateExistingDirectoryPath(raw)
	if err == nil || !strings.Contains(err.Error(), "parent traversal") {
		t.Fatalf("raw directory path error = %v", err)
	}
}

func TestNativeReviewAndRoutineRequirementsRejectFilesystemRoots(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	baselineDist, _ := completeFirstSupportFixture(t)
	if err := createNativeEvidence(baselineDist); err != nil {
		t.Fatal(err)
	}
	currentDist, current, _ := completeReceiptFixture(t)
	output := filepath.Join(t.TempDir(), "review.json")
	for name, args := range map[string][]string{
		"current":  {"--dist", root, "--baseline-dist", baselineDist, "--output", output},
		"baseline": {"--dist", currentDist, "--baseline-dist", root, "--output", output},
	} {
		t.Run("review-"+name, func(t *testing.T) {
			err := runNativeReviewTemplate(args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "filesystem root") {
				t.Fatalf("native review root error = %v", err)
			}
		})
	}

	_, currentBytes, err := readEvidenceReceipt(currentDist)
	if err != nil {
		t.Fatal(err)
	}
	review := writeRoutineReviewFixture(t, currentDist, current, baselineDist)
	for name, options := range map[string]nativeRequirementsOptions{
		"current":  {dist: root, review: review, baselineDist: baselineDist},
		"baseline": {dist: currentDist, review: review, baselineDist: root},
	} {
		t.Run("routine-"+name, func(t *testing.T) {
			err := createRoutineRequirements(options, current, currentBytes)
			if err == nil || !strings.Contains(err.Error(), "filesystem root") {
				t.Fatalf("routine requirements root error = %v", err)
			}
		})
	}
}

func TestRoutineRequirementsExistingDestinationsDoNotPartiallyWrite(t *testing.T) {
	for _, relative := range []string{
		filepath.Join("native", "baseline"),
		filepath.Join("native", "review.json"),
		filepath.Join("native", "requirements.json"),
	} {
		t.Run(filepath.Base(relative), func(t *testing.T) {
			baselineDist, _ := completeFirstSupportFixture(t)
			if err := createNativeEvidence(baselineDist); err != nil {
				t.Fatal(err)
			}
			currentDist, current, _ := completeReceiptFixture(t)
			_, currentBytes, err := readEvidenceReceipt(currentDist)
			if err != nil {
				t.Fatal(err)
			}
			review := writeRoutineReviewFixture(t, currentDist, current, baselineDist)
			destination := filepath.Join(currentDist, relative)
			if filepath.Base(relative) == "baseline" {
				if err := os.MkdirAll(destination, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(destination, []byte("existing\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			currentBefore := snapshotTestTree(t, currentDist)
			baselineBefore := snapshotTestTree(t, baselineDist)
			options := nativeRequirementsOptions{dist: currentDist, review: review, baselineDist: baselineDist}
			err = createRoutineRequirements(options, current, currentBytes)
			if err == nil || !strings.Contains(err.Error(), "routine native destination already exists") {
				t.Fatalf("existing routine destination error = %v", err)
			}
			assertTestTreeUnchanged(t, currentDist, currentBefore)
			assertTestTreeUnchanged(t, baselineDist, baselineBefore)
		})
	}
}

func validSmokeFixture(t *testing.T, target buildTarget) (string, string) {
	t.Helper()
	dist, receipt, metadata := completeReceiptFixture(t)
	archiveName := releaseArchiveName("stage", metadata.Version, target)
	archivePath := filepath.Join(dist, "artifacts", archiveName)
	format := "tar.gz"
	if target.OS == "windows" {
		format = "zip"
	}
	binaryName := releaseBinaryName("stage", target.OS)
	writeTestArchive(t, archivePath, format, validArchiveEntries(binaryName), nil)
	archiveHash, err := hashBoundedFile(archivePath, defaultArtifactLimits.maxCompressedBytes)
	if err != nil {
		t.Fatal(err)
	}
	for index := range receipt.Artifacts {
		if receipt.Artifacts[index].Name == archiveName {
			receipt.Artifacts[index].SHA256 = archiveHash
		}
	}
	receiptBytes, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "receipt.json"), receiptBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	receiptDigest := sha256.Sum256(receiptBytes)
	binaryDigest := sha256.Sum256([]byte("executable"))
	reportDir := filepath.Join(dist, "native", "reports", target.OS+"-"+target.Arch, "report-1")
	if err := os.MkdirAll(reportDir, 0o700); err != nil {
		t.Fatal(err)
	}
	record := smokeRecord{
		SchemaVersion: 1, ReportID: "report-1", Environment: "stage",
		TargetOS: target.OS, TargetArch: target.Arch, CollectorOS: target.OS, CollectorArch: target.Arch,
		HostOS: target.OS, HostArch: target.Arch, HostKind: "physical", Provenance: "operator", Operator: "fixture-operator", Procedure: "native_release_v1", Execution: "native",
		DaemonArch: "unknown", RequestedPlatform: target.OS + "/" + target.Arch, CollectorTranslated: "false",
		ReceiptSHA256: hexDigest(receiptDigest), ArchiveName: archiveName, ArchiveSHA256: archiveHash,
		BinaryName: binaryName, BinarySHA256: hexDigest(binaryDigest), VersionExit: 0, HelpExit: 0,
	}
	writeSmokeRecordFixture(t, reportDir, record)
	observed := observedBuildInfo{Version: receipt.Version, Environment: receipt.Environment, Commit: receipt.Commit, SourceDate: receipt.SourceDate, BuildKind: receipt.BuildKind, GoVersion: receipt.GoVersion, OS: target.OS, Arch: target.Arch, ServerURL: receipt.ServerURL}
	versionBytes, _ := json.Marshal(observed)
	writeTestFile(t, filepath.Join(reportDir, "version.stdout"), string(versionBytes)+"\n")
	writeTestFile(t, filepath.Join(reportDir, "version.stderr"), "")
	writeTestFile(t, filepath.Join(reportDir, "help.stdout"), "Usage: hookspot\n")
	writeTestFile(t, filepath.Join(reportDir, "help.stderr"), "")
	return dist, reportDir
}

func completeFirstSupportFixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	dist, receipt, metadata := completeReceiptFixture(t)
	for _, target := range releaseTargets {
		archiveName := releaseArchiveName("stage", metadata.Version, target)
		format := "tar.gz"
		if target.OS == "windows" {
			format = "zip"
		}
		binaryName := releaseBinaryName("stage", target.OS)
		archivePath := filepath.Join(dist, "artifacts", archiveName)
		writeTestArchive(t, archivePath, format, validArchiveEntries(binaryName), nil)
		archiveHash, err := hashBoundedFile(archivePath, defaultArtifactLimits.maxCompressedBytes)
		if err != nil {
			t.Fatal(err)
		}
		for index := range receipt.Artifacts {
			if receipt.Artifacts[index].Name == archiveName {
				receipt.Artifacts[index].SHA256 = archiveHash
			}
		}
	}
	writeTestJSON(t, filepath.Join(dist, "receipt.json"), receipt)
	receiptBytes, err := os.ReadFile(filepath.Join(dist, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	reports := make(map[string]string)
	for _, target := range releaseTargets {
		reports[target.OS+"/"+target.Arch] = writeSmokeReportForDist(t, dist, receipt, receiptBytes, target)
	}
	if err := createNativeRequirements(nativeRequirementsOptions{dist: dist}); err != nil {
		t.Fatal(err)
	}
	requirements, err := readNativeRequirements(dist)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range requirements.Targets {
		for _, check := range row.RequiredChecks {
			if isManualCheck(check) {
				procedure, required := canonicalManualProcedure(row.Target, check)
				if !required {
					procedure = "fixture-" + check + "-v1"
				}
				createManualFixture(t, dist, row.Target, check, "pass", procedure)
			}
		}
	}
	return dist, reports
}

func writeSmokeReportForDist(t *testing.T, dist string, receipt buildReceipt, receiptBytes []byte, target buildTarget) string {
	t.Helper()
	archiveName := releaseArchiveName(receipt.Environment, receipt.Version, target)
	artifact, ok := receiptArtifact(receipt, target)
	if !ok {
		t.Fatal("missing fixture artifact")
	}
	binaryName := releaseBinaryName(receipt.Environment, target.OS)
	archive, err := readReleaseArchive(filepath.Join(dist, "artifacts", archiveName), binaryName, defaultArtifactLimits)
	if err != nil {
		t.Fatal(err)
	}
	reportID := "report-" + target.OS + "-" + target.Arch
	reportDir := filepath.Join(dist, "native", "reports", target.OS+"-"+target.Arch, reportID)
	if err := os.MkdirAll(reportDir, 0o700); err != nil {
		t.Fatal(err)
	}
	record := smokeRecord{
		SchemaVersion: 1, ReportID: reportID, Environment: receipt.Environment,
		TargetOS: target.OS, TargetArch: target.Arch, CollectorOS: target.OS, CollectorArch: target.Arch,
		HostOS: target.OS, HostArch: target.Arch, HostKind: "physical", Provenance: "operator",
		Operator: "fixture-operator", Procedure: "native_release_v1", Execution: "native", DaemonArch: "unknown",
		RequestedPlatform: target.OS + "/" + target.Arch, CollectorTranslated: "unknown",
		ReceiptSHA256: sha256Hex(receiptBytes), ArchiveName: archiveName, ArchiveSHA256: artifact.SHA256,
		BinaryName: binaryName, BinarySHA256: sha256Hex(archive.members[binaryName].data), VersionExit: 0, HelpExit: 0,
	}
	writeSmokeRecordFixture(t, reportDir, record)
	observed := observedBuildInfo{
		Version: receipt.Version, Environment: receipt.Environment, Commit: receipt.Commit,
		SourceDate: receipt.SourceDate, BuildKind: receipt.BuildKind, GoVersion: receipt.GoVersion,
		OS: target.OS, Arch: target.Arch, ServerURL: receipt.ServerURL,
	}
	versionBytes, _ := json.Marshal(observed)
	writeTestFile(t, filepath.Join(reportDir, "version.stdout"), string(versionBytes)+"\n")
	writeTestFile(t, filepath.Join(reportDir, "version.stderr"), "")
	writeTestFile(t, filepath.Join(reportDir, "help.stdout"), "Usage: hookspot\n")
	writeTestFile(t, filepath.Join(reportDir, "help.stderr"), "")
	return reportDir
}

func writeRoutineReviewFixture(t *testing.T, currentDist string, current buildReceipt, baselineDist string) string {
	t.Helper()
	baseline, _, err := readEvidenceReceipt(baselineDist)
	if err != nil {
		t.Fatal(err)
	}
	review := nativeReview{
		SchemaVersion: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339), Operator: "fixture-operator",
		SourceRepository: current.SourceRepository, Environment: current.Environment,
		BaselineCommit: baseline.Commit, CurrentCommit: current.Commit, Rationale: "fixture-cumulative-review",
		Targets: make([]nativeReviewTarget, len(releaseTargets)),
	}
	for index, target := range releaseTargets {
		checks := []string(nil)
		reason := "unchanged-unavailable"
		available := false
		if target.OS+"/"+target.Arch == current.BuilderPlatform {
			checks = []string{"version", "help", "network"}
			reason = "builder-network"
			available = true
		}
		review.Targets[index] = nativeReviewTarget{
			Target: target, Available: available, Reason: reason, RequiredChecks: checks,
		}
	}
	path := filepath.Join(t.TempDir(), "review.json")
	writeTestJSON(t, path, review)
	return path
}

func requirementsFixtureBuilderPlatform(t *testing.T, dist string) string {
	t.Helper()
	receipt, _, err := readEvidenceReceipt(dist)
	if err != nil {
		t.Fatal(err)
	}
	return receipt.BuilderPlatform
}

func hexDigest(value [sha256.Size]byte) string { return fmt.Sprintf("%x", value[:]) }

func writeSmokeRecordFixture(t *testing.T, reportDir string, record smokeRecord) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(reportDir, "record.env"), []byte(formatSmokeRecord(record)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readSmokeRecordFixture(t *testing.T, reportDir string) smokeRecord {
	t.Helper()
	record, err := readSmokeRecord(filepath.Join(reportDir, "record.env"))
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func copyTestDirectory(t *testing.T, source, destination string) {
	t.Helper()
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(destination, entry.Name()), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func snapshotTestTree(t *testing.T, root string) []string {
	t.Helper()
	var snapshot []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		record := relative + "\x00" + info.Mode().String()
		if info.Mode().IsRegular() {
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			record += "\x00" + sha256Hex(contents)
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			record += "\x00" + target
		}
		snapshot = append(snapshot, record)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertTestTreeUnchanged(t *testing.T, root string, before []string) {
	t.Helper()
	after := snapshotTestTree(t, root)
	if strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("retained tree changed:\nbefore: %q\nafter:  %q", before, after)
	}
}

func parentTraversalTestPath(t *testing.T, name string) (string, string) {
	t.Helper()
	root := t.TempDir()
	safe := filepath.Join(root, "safe")
	resolvedParent := filepath.Join(root, "outside")
	linkTarget := filepath.Join(resolvedParent, "nested")
	if err := os.MkdirAll(safe, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(linkTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linkTarget, filepath.Join(safe, "link")); err != nil {
		t.Fatal(err)
	}
	raw := filepath.Join(safe, "link") + string(filepath.Separator) + ".." + string(filepath.Separator) + name
	hasParentTraversal := false
	for _, component := range strings.Split(filepath.ToSlash(raw), "/") {
		hasParentTraversal = hasParentTraversal || component == ".."
	}
	if !hasParentTraversal {
		t.Fatalf("raw path lost its parent-traversal component: %q", raw)
	}
	return raw, filepath.Join(resolvedParent, name)
}

func createManualFixture(t *testing.T, dist string, target buildTarget, check, result, procedure string) string {
	t.Helper()
	record, relative, err := newManualCheckRecord(dist, target, check, result, "fixture-operator", procedure)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dist, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, path, record)
	return relative
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	contents, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	contents = append(contents, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}
