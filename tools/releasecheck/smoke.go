package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxSmokeRecordBytes = 64 * 1024
	maxSmokeOutputBytes = 256 * 1024
)

var smokeRecordKeys = []string{
	"SCHEMA_VERSION", "REPORT_ID", "ENVIRONMENT", "TARGET_OS", "TARGET_ARCH",
	"COLLECTOR_OS", "COLLECTOR_ARCH", "HOST_OS", "HOST_ARCH", "HOST_KIND",
	"PROVENANCE", "OPERATOR", "PROCEDURE", "EXECUTION", "DAEMON_ARCH", "REQUESTED_PLATFORM", "COLLECTOR_TRANSLATED",
	"RECEIPT_SHA256", "ARCHIVE_NAME", "ARCHIVE_SHA256", "BINARY_NAME", "BINARY_SHA256",
	"VERSION_EXIT", "VERSION_TIMEOUT", "VERSION_CAPTURE_FAILED", "HELP_EXIT", "HELP_TIMEOUT", "HELP_CAPTURE_FAILED",
}

type smokeRecord struct {
	SchemaVersion        int
	ReportID             string
	Environment          string
	TargetOS             string
	TargetArch           string
	CollectorOS          string
	CollectorArch        string
	HostOS               string
	HostArch             string
	HostKind             string
	Provenance           string
	Operator             string
	Procedure            string
	Execution            string
	DaemonArch           string
	RequestedPlatform    string
	CollectorTranslated  string
	ReceiptSHA256        string
	ArchiveName          string
	ArchiveSHA256        string
	BinaryName           string
	BinarySHA256         string
	VersionExit          int
	VersionTimeout       bool
	VersionCaptureFailed bool
	HelpExit             int
	HelpTimeout          bool
	HelpCaptureFailed    bool
}

type observedBuildInfo struct {
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

type smokeEvidence struct {
	ReportID     string
	Target       buildTarget
	Execution    string
	ReportDir    string
	ArchiveName  string
	ArchiveSHA   string
	BinarySHA    string
	ReceiptSHA   string
	VersionValid bool
	HelpValid    bool
}

type smokeObservation struct {
	Evidence   smokeEvidence
	Successful bool
}

type acceptedSmokeReport struct {
	ReportID            string `json:"report_id"`
	Path                string `json:"path"`
	Execution           string `json:"execution"`
	CollectorOS         string `json:"collector_os"`
	CollectorArch       string `json:"collector_arch"`
	HostOS              string `json:"host_os"`
	HostArch            string `json:"host_arch"`
	HostKind            string `json:"host_kind"`
	Provenance          string `json:"provenance"`
	Operator            string `json:"operator"`
	Procedure           string `json:"procedure"`
	DaemonArch          string `json:"daemon_arch"`
	RequestedPlatform   string `json:"requested_platform"`
	CollectorTranslated string `json:"collector_translated"`
	ArchiveName         string `json:"archive_name"`
	ArchiveSHA256       string `json:"archive_sha256"`
	BinaryName          string `json:"binary_name"`
	BinarySHA256        string `json:"binary_sha256"`
	RecordSHA256        string `json:"record_sha256"`
	VersionStdoutSHA256 string `json:"version_stdout_sha256"`
	VersionStderrSHA256 string `json:"version_stderr_sha256"`
	HelpStdoutSHA256    string `json:"help_stdout_sha256"`
	HelpStderrSHA256    string `json:"help_stderr_sha256"`
}

type nativeEvidenceTarget struct {
	Target       buildTarget           `json:"target"`
	Execution    string                `json:"execution"`
	Report       *acceptedSmokeReport  `json:"report,omitempty"`
	ManualChecks []acceptedManualCheck `json:"manual_checks"`
}

type nativeEvidence struct {
	SchemaVersion int                    `json:"schema_version"`
	CreatedAt     string                 `json:"created_at"`
	Environment   string                 `json:"environment"`
	Commit        string                 `json:"commit"`
	ReceiptSHA256 string                 `json:"receipt_sha256"`
	Targets       []nativeEvidenceTarget `json:"targets"`
}

type nativeRequirementTarget struct {
	Target         buildTarget `json:"target"`
	Available      bool        `json:"available"`
	Affected       bool        `json:"affected"`
	Reason         string      `json:"reason"`
	RequiredChecks []string    `json:"required_checks"`
}

type nativeRequirements struct {
	SchemaVersion         int                       `json:"schema_version"`
	CreatedAt             string                    `json:"created_at"`
	Mode                  string                    `json:"mode"`
	SourceRepository      string                    `json:"source_repository"`
	Environment           string                    `json:"environment"`
	Commit                string                    `json:"commit"`
	ReceiptSHA256         string                    `json:"receipt_sha256"`
	BaselineRepository    string                    `json:"baseline_repository,omitempty"`
	BaselineEnvironment   string                    `json:"baseline_environment,omitempty"`
	BaselineVersion       string                    `json:"baseline_version,omitempty"`
	BaselineCommit        string                    `json:"baseline_commit,omitempty"`
	BaselineReceiptSHA256 string                    `json:"baseline_receipt_sha256,omitempty"`
	ReviewSHA256          string                    `json:"review_sha256,omitempty"`
	ReviewOperator        string                    `json:"review_operator,omitempty"`
	ReviewRationale       string                    `json:"review_rationale,omitempty"`
	Targets               []nativeRequirementTarget `json:"targets"`
}

type nativeRequirementsOptions struct {
	dist         string
	review       string
	baselineDist string
}

func runSmoke(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "verify" {
		return errors.New("usage: releasecheck smoke verify --environment ENV --dist PATH --report PATH")
	}
	flags := flag.NewFlagSet("releasecheck smoke verify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	environment := flags.String("environment", "", "release environment")
	dist := flags.String("dist", "", "retained output parent")
	report := flags.String("report", "", "native smoke report directory")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dist) || !filepath.IsAbs(*report) {
		return errors.New("smoke verify requires environment, absolute dist, and absolute report")
	}
	evidence, err := verifySmokeReport(*dist, *environment, *report)
	if err != nil {
		return err
	}
	suffix := ""
	if evidence.Execution != "native" {
		suffix = "; it does not satisfy native requirements"
	}
	_, err = fmt.Fprintf(out, "smoke report is valid for %s/%s (%s)%s\n", evidence.Target.OS, evidence.Target.Arch, evidence.Execution, suffix)
	return err
}

func runNativeRequirements(args []string) error {
	flags := flag.NewFlagSet("releasecheck native-requirements", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := nativeRequirementsOptions{}
	flags.StringVar(&options.dist, "dist", "", "retained output parent")
	flags.StringVar(&options.review, "review", "", "routine review record")
	flags.StringVar(&options.baselineDist, "baseline-dist", "", "prior retained output parent")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !filepath.IsAbs(options.dist) {
		return errors.New("native-requirements requires an absolute --dist")
	}
	if (options.review == "") != (options.baselineDist == "") || options.review != "" && (!filepath.IsAbs(options.review) || !filepath.IsAbs(options.baselineDist)) {
		return errors.New("routine requirements need absolute --review and --baseline-dist together")
	}
	return createNativeRequirements(options)
}

func verifySmokeReport(dist, environment, reportDir string) (smokeEvidence, error) {
	observation, err := inspectSmokeReport(dist, environment, reportDir)
	if err != nil {
		return smokeEvidence{}, err
	}
	if !observation.Successful {
		return smokeEvidence{}, errors.New("smoke command did not complete successfully")
	}
	return observation.Evidence, nil
}

func inspectSmokeReport(dist, environment, reportDir string) (smokeObservation, error) {
	if environment != "stage" && environment != "prod" {
		return smokeObservation{}, errors.New("smoke environment must be stage or prod")
	}
	receipt, receiptBytes, err := readEvidenceReceipt(dist)
	if err != nil || receipt.Environment != environment {
		return smokeObservation{}, errors.New("read matching retained receipt")
	}
	record, err := readSmokeRecord(filepath.Join(reportDir, "record.env"))
	if err != nil {
		return smokeObservation{}, err
	}
	target := buildTarget{OS: record.TargetOS, Arch: record.TargetArch}
	if record.SchemaVersion != 1 || record.ReportID != filepath.Base(reportDir) || record.Environment != environment || !isReleaseTarget(target) {
		return smokeObservation{}, errors.New("smoke record identity is invalid")
	}
	if err := validateExecutionClassification(record); err != nil {
		return smokeObservation{}, err
	}
	receiptHash := sha256Hex(receiptBytes)
	archiveName := releaseArchiveName(environment, receipt.Version, target)
	binaryName := releaseBinaryName(environment, target.OS)
	if record.ReceiptSHA256 != receiptHash || record.ArchiveName != archiveName || record.BinaryName != binaryName {
		return smokeObservation{}, errors.New("smoke record does not match retained release identity")
	}
	artifact, ok := receiptArtifact(receipt, target)
	if !ok || artifact.Name != archiveName || artifact.SHA256 != record.ArchiveSHA256 {
		return smokeObservation{}, errors.New("smoke archive record does not match receipt")
	}
	archivePath := filepath.Join(dist, "artifacts", archiveName)
	archiveHash, err := hashBoundedFile(archivePath, defaultArtifactLimits.maxCompressedBytes)
	if err != nil || archiveHash != record.ArchiveSHA256 {
		return smokeObservation{}, errors.New("smoke archive bytes do not match receipt")
	}
	archive, err := readReleaseArchive(archivePath, binaryName, defaultArtifactLimits)
	if err != nil {
		return smokeObservation{}, err
	}
	binaryHash := sha256Hex(archive.members[binaryName].data)
	if binaryHash != record.BinarySHA256 {
		return smokeObservation{}, errors.New("smoke binary digest does not match archive")
	}
	if record.VersionTimeout && record.VersionExit != 124 || record.HelpTimeout && record.HelpExit != 124 {
		return smokeObservation{}, errors.New("smoke timeout status is inconsistent")
	}
	versionValid := record.VersionExit == 0 && !record.VersionTimeout && !record.VersionCaptureFailed
	helpValid := record.HelpExit == 0 && !record.HelpTimeout && !record.HelpCaptureFailed
	versionOut, err := readSmokeOutput(filepath.Join(reportDir, "version.stdout"), versionValid, false)
	if err != nil {
		return smokeObservation{}, errors.New("version output is invalid")
	}
	versionErr, err := readSmokeOutput(filepath.Join(reportDir, "version.stderr"), versionValid, true)
	if err != nil {
		return smokeObservation{}, errors.New("version error output is invalid")
	}
	helpOut, err := readSmokeOutput(filepath.Join(reportDir, "help.stdout"), helpValid, false)
	if err != nil {
		return smokeObservation{}, errors.New("help output is invalid")
	}
	helpErr, err := readSmokeOutput(filepath.Join(reportDir, "help.stderr"), helpValid, true)
	if err != nil {
		return smokeObservation{}, errors.New("help error output is invalid")
	}
	if versionValid {
		observed, err := decodeObservedBuildInfo(versionOut)
		if err != nil || len(versionErr) != 0 || observed != (observedBuildInfo{
			Version: receipt.Version, Environment: receipt.Environment, Commit: receipt.Commit,
			SourceDate: receipt.SourceDate, BuildKind: receipt.BuildKind, GoVersion: receipt.GoVersion,
			OS: target.OS, Arch: target.Arch, ServerURL: receipt.ServerURL,
		}) {
			return smokeObservation{}, errors.New("version output does not match retained release")
		}
	}
	if helpValid && (len(helpOut) == 0 || len(helpErr) != 0) {
		return smokeObservation{}, errors.New("help output does not match successful command")
	}
	evidence := smokeEvidence{
		ReportID: record.ReportID, Target: target, Execution: record.Execution, ReportDir: reportDir,
		ArchiveName: archiveName, ArchiveSHA: archiveHash, BinarySHA: binaryHash, ReceiptSHA: receiptHash,
		VersionValid: versionValid, HelpValid: helpValid,
	}
	return smokeObservation{Evidence: evidence, Successful: versionValid && helpValid}, nil
}

func validateExecutionClassification(record smokeRecord) error {
	if !validFact(record.TargetOS, false) || !validFact(record.TargetArch, false) || !validFact(record.CollectorOS, false) || !validFact(record.CollectorArch, false) ||
		!validFact(record.HostOS, true) || !validFact(record.HostArch, true) || !validFact(record.DaemonArch, true) ||
		!containsString([]string{"physical", "vm", "unknown"}, record.HostKind) ||
		!containsString([]string{"operator", "docker-daemon", "standalone"}, record.Provenance) ||
		!validFact(record.Operator, true) || !validFact(record.Procedure, true) || record.RequestedPlatform != record.TargetOS+"/"+record.TargetArch {
		return errors.New("smoke platform facts are invalid")
	}
	if record.Execution != "native" && record.Execution != "emulated" && record.Execution != "unknown" {
		return errors.New("smoke execution classification is invalid")
	}
	if record.CollectorTranslated != "true" && record.CollectorTranslated != "false" && record.CollectorTranslated != "unknown" {
		return errors.New("smoke translation fact is invalid")
	}
	if record.Execution == "native" {
		if record.Provenance == "standalone" || record.HostOS == "unknown" || record.HostArch == "unknown" || record.HostKind == "unknown" ||
			record.CollectorOS != record.TargetOS || record.HostOS != record.TargetOS || record.HostArch != record.TargetArch {
			return errors.New("native smoke classification contradicts recorded platform facts")
		}
		switch record.Provenance {
		case "docker-daemon":
			if record.CollectorArch != record.TargetArch || record.DaemonArch == "unknown" || record.DaemonArch != record.TargetArch {
				return errors.New("native Docker smoke lacks matching daemon architecture")
			}
		case "operator":
			if record.Operator == "unknown" || record.Procedure == "unknown" || record.DaemonArch != "unknown" {
				return errors.New("native operator smoke lacks operator procedure identity")
			}
		}
	}
	return nil
}

func collectNativeEvidence(dist, environment string) (nativeEvidence, error) {
	receipt, receiptBytes, err := readEvidenceReceipt(dist)
	if err != nil || receipt.Environment != environment {
		return nativeEvidence{}, errors.New("read matching retained receipt")
	}
	result := nativeEvidence{
		SchemaVersion: 1,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		Environment:   environment,
		Commit:        receipt.Commit,
		ReceiptSHA256: sha256Hex(receiptBytes),
		Targets:       make([]nativeEvidenceTarget, len(releaseTargets)),
	}
	for index, target := range releaseTargets {
		result.Targets[index] = nativeEvidenceTarget{Target: target, Execution: "unverified", ManualChecks: []acceptedManualCheck{}}
	}

	reportsRoot := filepath.Join(dist, "native", "reports")
	entries, err := os.ReadDir(reportsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nativeEvidence{}, errors.New("read native smoke reports")
	}
	seenIDs := make(map[string]bool)
	for _, targetEntry := range entries {
		if !targetEntry.IsDir() || targetEntry.Type()&os.ModeSymlink != 0 {
			return nativeEvidence{}, errors.New("native smoke report layout is invalid")
		}
		target, targetIndex, ok := targetForDirectory(targetEntry.Name())
		if !ok {
			return nativeEvidence{}, errors.New("native smoke report target directory is invalid")
		}
		targetRoot := filepath.Join(reportsRoot, targetEntry.Name())
		reportEntries, err := os.ReadDir(targetRoot)
		if err != nil {
			return nativeEvidence{}, errors.New("read native smoke target reports")
		}
		for _, reportEntry := range reportEntries {
			if !reportEntry.IsDir() || reportEntry.Type()&os.ModeSymlink != 0 {
				return nativeEvidence{}, errors.New("native smoke attempt layout is invalid")
			}
			reportDir := filepath.Join(targetRoot, reportEntry.Name())
			if _, err := os.Lstat(filepath.Join(reportDir, "record.env")); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return nativeEvidence{}, errors.New("inspect native smoke record")
			}
			if seenIDs[reportEntry.Name()] {
				return nativeEvidence{}, errors.New("native smoke report identity is duplicated")
			}
			seenIDs[reportEntry.Name()] = true
			observation, err := inspectSmokeReport(dist, environment, reportDir)
			if err != nil {
				return nativeEvidence{}, err
			}
			if observation.Evidence.Target != target {
				return nativeEvidence{}, errors.New("native smoke report is stored under the wrong target")
			}
			if !observation.Successful {
				continue
			}
			candidate, err := acceptedReport(dist, reportDir, observation.Evidence)
			if err != nil {
				return nativeEvidence{}, err
			}
			if candidate.Procedure != "native_release_v1" {
				continue
			}
			current := result.Targets[targetIndex].Report
			if current == nil || reportPreference(candidate) > reportPreference(*current) ||
				reportPreference(candidate) == reportPreference(*current) && candidate.ReportID < current.ReportID {
				result.Targets[targetIndex] = nativeEvidenceTarget{Target: target, Execution: candidate.Execution, Report: &candidate, ManualChecks: []acceptedManualCheck{}}
			}
		}
	}
	return result, nil
}

func targetForDirectory(name string) (buildTarget, int, bool) {
	for index, target := range releaseTargets {
		if name == target.OS+"-"+target.Arch {
			return target, index, true
		}
	}
	return buildTarget{}, 0, false
}

func acceptedReport(dist, reportDir string, evidence smokeEvidence) (acceptedSmokeReport, error) {
	record, err := readSmokeRecord(filepath.Join(reportDir, "record.env"))
	if err != nil {
		return acceptedSmokeReport{}, err
	}
	relative, err := filepath.Rel(dist, reportDir)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return acceptedSmokeReport{}, errors.New("native smoke report path is invalid")
	}
	hashes := make([]string, 5)
	files := []struct {
		name    string
		maximum int64
	}{
		{name: "record.env", maximum: maxSmokeRecordBytes},
		{name: "version.stdout", maximum: maxSmokeOutputBytes},
		{name: "version.stderr", maximum: maxSmokeOutputBytes},
		{name: "help.stdout", maximum: maxSmokeOutputBytes},
		{name: "help.stderr", maximum: maxSmokeOutputBytes},
	}
	for index, file := range files {
		hashes[index], err = hashBoundedFile(filepath.Join(reportDir, file.name), file.maximum)
		if err != nil {
			return acceptedSmokeReport{}, errors.New("hash native smoke report file")
		}
	}
	return acceptedSmokeReport{
		ReportID: evidence.ReportID, Path: filepath.ToSlash(relative), Execution: evidence.Execution,
		CollectorOS: record.CollectorOS, CollectorArch: record.CollectorArch,
		HostOS: record.HostOS, HostArch: record.HostArch, HostKind: record.HostKind, Provenance: record.Provenance,
		Operator: record.Operator, Procedure: record.Procedure,
		DaemonArch: record.DaemonArch, RequestedPlatform: record.RequestedPlatform, CollectorTranslated: record.CollectorTranslated,
		ArchiveName: evidence.ArchiveName, ArchiveSHA256: evidence.ArchiveSHA,
		BinaryName: record.BinaryName, BinarySHA256: evidence.BinarySHA,
		RecordSHA256: hashes[0], VersionStdoutSHA256: hashes[1], VersionStderrSHA256: hashes[2],
		HelpStdoutSHA256: hashes[3], HelpStderrSHA256: hashes[4],
	}, nil
}

func reportPreference(report acceptedSmokeReport) int {
	switch report.Execution {
	case "native":
		return 3
	case "emulated":
		return 2
	case "unknown":
		return 1
	default:
		return 0
	}
}

func createNativeRequirements(options nativeRequirementsOptions) error {
	receipt, receiptBytes, err := readEvidenceReceipt(options.dist)
	if err != nil {
		return err
	}
	if err := verifyReceipt(options.dist, receipt.Environment); err != nil {
		return fmt.Errorf("verify retained release before native requirements: %w", err)
	}
	if options.review != "" {
		return createRoutineRequirements(options, receipt, receiptBytes)
	}
	targets := make([]nativeRequirementTarget, 0, len(releaseTargets))
	for _, target := range releaseTargets {
		targets = append(targets, nativeRequirementTarget{Target: target, Reason: "first-support", RequiredChecks: firstSupportChecks(receipt, target)})
	}
	requirements := nativeRequirements{
		SchemaVersion: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339), Mode: "first-support",
		SourceRepository: receipt.SourceRepository, Environment: receipt.Environment, Commit: receipt.Commit,
		ReceiptSHA256: sha256Hex(receiptBytes), Targets: targets,
	}
	return writeRequirements(options.dist, requirements)
}

func readEvidenceReceipt(dist string) (buildReceipt, []byte, error) {
	contents, err := readBoundedRegularFile(filepath.Join(dist, "receipt.json"), maxReceiptBytes)
	if err != nil {
		return buildReceipt{}, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var receipt buildReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return buildReceipt{}, nil, errors.New("decode retained receipt")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return buildReceipt{}, nil, errors.New("retained receipt must contain one JSON object")
	}
	if err := validateReceipt(receipt, receipt.Environment); err != nil {
		return buildReceipt{}, nil, err
	}
	return receipt, contents, nil
}

func receiptArtifact(receipt buildReceipt, target buildTarget) (verifiedArtifact, bool) {
	name := releaseArchiveName(receipt.Environment, receipt.Version, target)
	for _, artifact := range receipt.Artifacts {
		if artifact.Name == name {
			return artifact, true
		}
	}
	return verifiedArtifact{}, false
}

func decodeObservedBuildInfo(contents []byte) (observedBuildInfo, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var observed observedBuildInfo
	if err := decoder.Decode(&observed); err != nil {
		return observedBuildInfo{}, err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return observedBuildInfo{}, errors.New("version output must contain one JSON object")
	}
	return observed, nil
}

func readSmokeText(path string, allowEmpty bool) ([]byte, error) {
	contents, err := readBoundedRegularFile(path, maxSmokeOutputBytes)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(contents) || bytes.HasPrefix(contents, []byte{0xef, 0xbb, 0xbf}) || !allowEmpty && len(contents) == 0 {
		return nil, errors.New("smoke output must be UTF-8 without BOM")
	}
	if bytes.IndexByte(contents, 0) >= 0 {
		return nil, errors.New("smoke output must not contain NUL bytes")
	}
	return contents, nil
}

func readSmokeOutput(path string, validateText, allowEmpty bool) ([]byte, error) {
	if validateText {
		return readSmokeText(path, allowEmpty)
	}
	return readBoundedRegularFile(path, maxSmokeOutputBytes)
}

func readSmokeRecord(path string) (smokeRecord, error) {
	contents, err := readBoundedRegularFile(path, maxSmokeRecordBytes)
	if err != nil {
		return smokeRecord{}, errors.New("read smoke scalar record")
	}
	if len(contents) == 0 || !utf8.Valid(contents) || bytes.HasPrefix(contents, []byte{0xef, 0xbb, 0xbf}) || bytes.ContainsRune(contents, '\r') {
		return smokeRecord{}, errors.New("smoke scalar record encoding is invalid")
	}
	for _, value := range contents {
		if value > 0x7f {
			return smokeRecord{}, errors.New("smoke scalar record must be ASCII")
		}
	}
	values := make(map[string]string, len(smokeRecordKeys))
	for _, line := range strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found || key == "" || value == "" || values[key] != "" || !containsString(smokeRecordKeys, key) || !validScalar(value) {
			return smokeRecord{}, errors.New("smoke scalar record contains invalid data")
		}
		values[key] = value
	}
	if len(values) != len(smokeRecordKeys) {
		return smokeRecord{}, errors.New("smoke scalar record is incomplete")
	}
	schema, err := strconv.Atoi(values["SCHEMA_VERSION"])
	if err != nil {
		return smokeRecord{}, errors.New("smoke scalar record schema is invalid")
	}
	versionExit, err := parseExitCode(values["VERSION_EXIT"])
	if err != nil {
		return smokeRecord{}, err
	}
	helpExit, err := parseExitCode(values["HELP_EXIT"])
	if err != nil {
		return smokeRecord{}, err
	}
	versionTimeout, err := strconv.ParseBool(values["VERSION_TIMEOUT"])
	if err != nil {
		return smokeRecord{}, errors.New("smoke timeout field is invalid")
	}
	helpTimeout, err := strconv.ParseBool(values["HELP_TIMEOUT"])
	if err != nil {
		return smokeRecord{}, errors.New("smoke timeout field is invalid")
	}
	versionCaptureFailed, err := strconv.ParseBool(values["VERSION_CAPTURE_FAILED"])
	if err != nil {
		return smokeRecord{}, errors.New("smoke capture status is invalid")
	}
	helpCaptureFailed, err := strconv.ParseBool(values["HELP_CAPTURE_FAILED"])
	if err != nil {
		return smokeRecord{}, errors.New("smoke capture status is invalid")
	}
	return smokeRecord{
		SchemaVersion: schema, ReportID: values["REPORT_ID"], Environment: values["ENVIRONMENT"],
		TargetOS: values["TARGET_OS"], TargetArch: values["TARGET_ARCH"], CollectorOS: values["COLLECTOR_OS"], CollectorArch: values["COLLECTOR_ARCH"],
		HostOS: values["HOST_OS"], HostArch: values["HOST_ARCH"], HostKind: values["HOST_KIND"], Provenance: values["PROVENANCE"],
		Operator: values["OPERATOR"], Procedure: values["PROCEDURE"], Execution: values["EXECUTION"],
		DaemonArch: values["DAEMON_ARCH"], RequestedPlatform: values["REQUESTED_PLATFORM"], CollectorTranslated: values["COLLECTOR_TRANSLATED"],
		ReceiptSHA256: values["RECEIPT_SHA256"], ArchiveName: values["ARCHIVE_NAME"], ArchiveSHA256: values["ARCHIVE_SHA256"],
		BinaryName: values["BINARY_NAME"], BinarySHA256: values["BINARY_SHA256"], VersionExit: versionExit, VersionTimeout: versionTimeout,
		VersionCaptureFailed: versionCaptureFailed, HelpExit: helpExit, HelpTimeout: helpTimeout, HelpCaptureFailed: helpCaptureFailed,
	}, nil
}

func formatSmokeRecord(record smokeRecord) string {
	values := []string{
		strconv.Itoa(record.SchemaVersion), record.ReportID, record.Environment, record.TargetOS, record.TargetArch,
		record.CollectorOS, record.CollectorArch, record.HostOS, record.HostArch, record.HostKind,
		record.Provenance, record.Operator, record.Procedure, record.Execution, record.DaemonArch, record.RequestedPlatform, record.CollectorTranslated,
		record.ReceiptSHA256, record.ArchiveName, record.ArchiveSHA256, record.BinaryName, record.BinarySHA256,
		strconv.Itoa(record.VersionExit), strconv.FormatBool(record.VersionTimeout), strconv.FormatBool(record.VersionCaptureFailed),
		strconv.Itoa(record.HelpExit), strconv.FormatBool(record.HelpTimeout), strconv.FormatBool(record.HelpCaptureFailed),
	}
	var output strings.Builder
	for index, key := range smokeRecordKeys {
		fmt.Fprintf(&output, "%s=%s\n", key, values[index])
	}
	return output.String()
}

func parseExitCode(value string) (int, error) {
	code, err := strconv.ParseInt(value, 10, 32)
	if err != nil || strconv.FormatInt(code, 10) != value {
		return 0, errors.New("smoke exit code is invalid")
	}
	return int(code), nil
}

func validScalar(value string) bool {
	if len(value) > 512 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character <= 0x20 || character > 0x7e || character == '=' || character == '\\' {
			return false
		}
	}
	return true
}

func validFact(value string, allowUnknown bool) bool {
	if allowUnknown && value == "unknown" {
		return true
	}
	if value == "" || value == "unknown" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		letter := character >= 'a' && character <= 'z'
		digit := character >= '0' && character <= '9'
		if !letter && !digit && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func validReportID(value string) bool {
	if value == "" || len(value) > 128 || value == "." || value == ".." || strings.Contains(value, "..") {
		return false
	}
	first := value[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || (first >= '0' && first <= '9')) {
		return false
	}
	for _, character := range value {
		letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		digit := character >= '0' && character <= '9'
		if !letter && !digit && character != '.' && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func writeRequirements(dist string, requirements nativeRequirements) error {
	nativeDir := filepath.Join(dist, "native")
	if err := ensurePlainDirectory(nativeDir); err != nil {
		return err
	}
	return writeJSONExclusive(filepath.Join(nativeDir, "requirements.json"), requirements)
}

func ensurePlainDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o755); err != nil {
			return errors.New("create native evidence directory")
		}
		info, err = os.Lstat(path)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("native evidence directory is unsafe")
	}
	return nil
}
