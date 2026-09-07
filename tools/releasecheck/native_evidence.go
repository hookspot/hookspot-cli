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
	"sort"
	"strings"
	"time"
)

const maxNativeRecordBytes = 256 * 1024

var nativeCheckNames = []string{
	"version",
	"help",
	"network",
	"config-private",
	"password-input",
	"signal-cancel",
}

type manualCheckRecord struct {
	SchemaVersion int         `json:"schema_version"`
	ReportID      string      `json:"report_id"`
	CreatedAt     string      `json:"created_at"`
	Environment   string      `json:"environment"`
	Commit        string      `json:"commit"`
	ReceiptSHA256 string      `json:"receipt_sha256"`
	Target        buildTarget `json:"target"`
	ArchiveName   string      `json:"archive_name"`
	ArchiveSHA256 string      `json:"archive_sha256"`
	BinaryName    string      `json:"binary_name"`
	BinarySHA256  string      `json:"binary_sha256"`
	Check         string      `json:"check"`
	Procedure     string      `json:"procedure"`
	Result        string      `json:"result"`
	Operator      string      `json:"operator"`
}

type acceptedManualCheck struct {
	ReportID      string `json:"report_id"`
	Path          string `json:"path"`
	RecordSHA256  string `json:"record_sha256"`
	ArchiveName   string `json:"archive_name"`
	ArchiveSHA256 string `json:"archive_sha256"`
	BinaryName    string `json:"binary_name"`
	BinarySHA256  string `json:"binary_sha256"`
	Check         string `json:"check"`
	Procedure     string `json:"procedure"`
	Result        string `json:"result"`
	Operator      string `json:"operator"`
}

func runNativeEvidence(args []string) error {
	flags := flag.NewFlagSet("releasecheck native-evidence", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dist := flags.String("dist", "", "retained output parent")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dist) {
		return errors.New("native-evidence requires an absolute --dist")
	}
	return createNativeEvidence(*dist)
}

func runNativeManual(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("releasecheck native-manual", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dist := flags.String("dist", "", "retained output parent")
	targetValue := flags.String("target", "", "release target OS/ARCH")
	check := flags.String("check", "", "behavior check name")
	result := flags.String("result", "", "pass or fail")
	operator := flags.String("operator", "", "operator identity")
	procedure := flags.String("procedure", "", "exact procedure identity")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dist) {
		return errors.New("native-manual requires absolute --dist, --target, --check, --result, --operator, and --procedure")
	}
	target, err := parseBuildTarget(*targetValue)
	if err != nil {
		return err
	}
	record, relative, err := newManualCheckRecord(*dist, target, *check, *result, *operator, *procedure)
	if err != nil {
		return err
	}
	nativeDir := filepath.Join(*dist, "native")
	manualDir := filepath.Join(nativeDir, "manual")
	targetDir := filepath.Join(manualDir, target.OS+"-"+target.Arch)
	for _, directory := range []string{nativeDir, manualDir, targetDir} {
		if err := ensurePlainDirectory(directory); err != nil {
			return err
		}
	}
	if err := writeJSONExclusive(filepath.Join(*dist, filepath.FromSlash(relative)), record); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "native manual check retained: %s\n", relative)
	return err
}

func parseBuildTarget(value string) (buildTarget, error) {
	osName, arch, found := strings.Cut(value, "/")
	target := buildTarget{OS: osName, Arch: arch}
	if !found || !isReleaseTarget(target) {
		return buildTarget{}, errors.New("native target must be a supported OS/ARCH")
	}
	return target, nil
}

func createNativeEvidence(dist string) error {
	requirements, err := readNativeRequirements(dist)
	if err != nil {
		return err
	}
	evidence, err := collectNativeEvidence(dist, requirements.Environment)
	if err != nil {
		return err
	}
	manual, err := collectManualEvidence(dist, requirements.Environment)
	if err != nil {
		return err
	}
	for index := range evidence.Targets {
		evidence.Targets[index].ManualChecks = manual[index]
	}
	if err := validateNativeEvidence(dist, requirements, evidence); err != nil {
		return err
	}
	return writeJSONExclusive(filepath.Join(dist, "native", "evidence.json"), evidence)
}

func readNativeRequirements(dist string) (nativeRequirements, error) {
	contents, err := readBoundedRegularFile(filepath.Join(dist, "native", "requirements.json"), maxNativeRecordBytes)
	if err != nil {
		return nativeRequirements{}, errors.New("read native requirements")
	}
	var requirements nativeRequirements
	if err := decodeStrictJSON(contents, &requirements, "native requirements"); err != nil {
		return nativeRequirements{}, err
	}
	if err := validateNativeRequirements(dist, requirements); err != nil {
		return nativeRequirements{}, err
	}
	return requirements, nil
}

func readNativeEvidence(dist string) (nativeEvidence, error) {
	contents, err := readBoundedRegularFile(filepath.Join(dist, "native", "evidence.json"), maxNativeRecordBytes)
	if err != nil {
		return nativeEvidence{}, errors.New("read native evidence")
	}
	var evidence nativeEvidence
	if err := decodeStrictJSON(contents, &evidence, "native evidence"); err != nil {
		return nativeEvidence{}, err
	}
	return evidence, nil
}

func validateNativeRequirements(dist string, requirements nativeRequirements) error {
	receipt, receiptBytes, err := readEvidenceReceipt(dist)
	if err != nil {
		return err
	}
	if requirements.SchemaVersion != 1 || requirements.Environment != receipt.Environment ||
		requirements.SourceRepository != receipt.SourceRepository || requirements.Commit != receipt.Commit || requirements.ReceiptSHA256 != sha256Hex(receiptBytes) ||
		(requirements.Mode != "first-support" && requirements.Mode != "routine") || len(requirements.Targets) != len(releaseTargets) {
		return errors.New("native requirements do not match retained release")
	}
	if requirements.Mode == "first-support" {
		if requirements.BaselineRepository != "" || requirements.BaselineEnvironment != "" || requirements.BaselineVersion != "" ||
			requirements.BaselineCommit != "" || requirements.BaselineReceiptSHA256 != "" || requirements.ReviewSHA256 != "" ||
			requirements.ReviewOperator != "" || requirements.ReviewRationale != "" {
			return errors.New("first-support requirements contain a routine baseline")
		}
	} else if requirements.BaselineRepository != receipt.SourceRepository || requirements.BaselineEnvironment != receipt.Environment ||
		requirements.BaselineVersion == "" || !commitPattern.MatchString(requirements.BaselineCommit) ||
		!validDigest(requirements.BaselineReceiptSHA256) || !validDigest(requirements.ReviewSHA256) ||
		!validSingleLine(requirements.ReviewOperator, 128) || !validSingleLine(requirements.ReviewRationale, 512) {
		return errors.New("routine native requirements baseline is invalid")
	}
	if _, err := time.Parse(time.RFC3339, requirements.CreatedAt); err != nil {
		return errors.New("native requirements creation time is invalid")
	}
	for index, row := range requirements.Targets {
		if row.Target != releaseTargets[index] || !validSingleLine(row.Reason, 256) {
			return errors.New("native requirement target is invalid")
		}
		seen := make(map[string]bool)
		for _, check := range row.RequiredChecks {
			if !containsString(nativeCheckNames, check) || seen[check] {
				return errors.New("native requirement check is invalid")
			}
			seen[check] = true
		}
		if len(row.RequiredChecks) > 0 && (!seen["version"] || !seen["help"]) {
			return errors.New("native requirement must include version and help")
		}
		if requirements.Mode == "first-support" {
			for _, required := range firstSupportChecks(receipt, row.Target) {
				if !seen[required] {
					return errors.New("first-support native requirements omit a mandatory check")
				}
			}
		} else {
			if (row.Available || row.Affected) && (!seen["version"] || !seen["help"]) {
				return errors.New("available or affected routine target must require version and help")
			}
			builderTarget := row.Target.OS+"/"+row.Target.Arch == receipt.BuilderPlatform
			if builderTarget && (!seen["version"] || !seen["help"] || !seen["network"]) {
				return errors.New("routine native requirements omit the builder-target network check")
			}
			if !builderTarget && !row.Available && !row.Affected && len(row.RequiredChecks) != 0 {
				return errors.New("unchanged unavailable routine target contains checks")
			}
		}
	}
	if requirements.Mode == "routine" {
		return validateRetainedRoutineInputs(dist, requirements, receipt)
	}
	return nil
}

func firstSupportChecks(receipt buildReceipt, target buildTarget) []string {
	checks := []string{"version", "help"}
	if target.OS == "windows" {
		checks = append(checks, "config-private", "password-input", "signal-cancel")
	}
	if target.OS+"/"+target.Arch == receipt.BuilderPlatform {
		checks = append(checks, "network")
	}
	return checks
}

func validateNativeEvidence(dist string, requirements nativeRequirements, evidence nativeEvidence) error {
	if err := validateNativeRequirements(dist, requirements); err != nil {
		return err
	}
	if evidence.SchemaVersion != 1 || evidence.Environment != requirements.Environment || evidence.Commit != requirements.Commit ||
		evidence.ReceiptSHA256 != requirements.ReceiptSHA256 || len(evidence.Targets) != len(releaseTargets) {
		return errors.New("native evidence does not match requirements")
	}
	if _, err := time.Parse(time.RFC3339, evidence.CreatedAt); err != nil {
		return errors.New("native evidence creation time is invalid")
	}
	for index, row := range evidence.Targets {
		requirement := requirements.Targets[index]
		if row.Target != releaseTargets[index] || requirement.Target != row.Target {
			return errors.New("native evidence target is invalid")
		}
		if row.Report == nil {
			if row.Execution != "unverified" {
				return errors.New("unverified native evidence contains a report status")
			}
		} else {
			if row.Execution != row.Report.Execution || row.Execution == "unverified" {
				return errors.New("native smoke evidence status is invalid")
			}
			if err := validateAcceptedReport(dist, requirements.Environment, row.Target, *row.Report); err != nil {
				return err
			}
		}
		manualByCheck := make(map[string]bool)
		for _, manual := range row.ManualChecks {
			if manualByCheck[manual.Check] {
				return errors.New("native manual evidence repeats a check")
			}
			manualByCheck[manual.Check] = true
			if err := validateAcceptedManualCheck(dist, row.Target, manual); err != nil {
				return err
			}
		}
		for _, check := range requirement.RequiredChecks {
			switch check {
			case "version", "help":
				if row.Report == nil || row.Execution != "native" {
					return fmt.Errorf("required native %s evidence is missing for %s/%s (procedure native_release_v1)", check, row.Target.OS, row.Target.Arch)
				}
			default:
				if !manualByCheck[check] {
					procedure, required := canonicalManualProcedure(row.Target, check)
					if required {
						return fmt.Errorf("required native %s evidence is missing for %s/%s (procedure %s)", check, row.Target.OS, row.Target.Arch, procedure)
					}
					return fmt.Errorf("required native %s evidence is missing for %s/%s", check, row.Target.OS, row.Target.Arch)
				}
			}
		}
	}
	return nil
}

func validateAcceptedReport(dist, environment string, target buildTarget, accepted acceptedSmokeReport) error {
	expectedPath := filepath.ToSlash(filepath.Join("native", "reports", target.OS+"-"+target.Arch, accepted.ReportID))
	if accepted.Path != expectedPath || !validReportID(accepted.ReportID) {
		return errors.New("accepted smoke report path is invalid")
	}
	if accepted.Procedure != "native_release_v1" {
		return errors.New("accepted smoke report requires procedure native_release_v1")
	}
	reportDir := filepath.Join(dist, filepath.FromSlash(accepted.Path))
	observation, err := inspectSmokeReport(dist, environment, reportDir)
	if err != nil || !observation.Successful || observation.Evidence.Target != target {
		return errors.New("accepted smoke report is no longer valid")
	}
	current, err := acceptedReport(dist, reportDir, observation.Evidence)
	if err != nil || current != accepted {
		return errors.New("accepted smoke report files changed")
	}
	return nil
}

func retainedEnvironment(dist string) string {
	receipt, _, err := readEvidenceReceipt(dist)
	if err != nil {
		return ""
	}
	return receipt.Environment
}

func newManualCheckRecord(dist string, target buildTarget, check, result, operator, procedure string) (manualCheckRecord, string, error) {
	if !isReleaseTarget(target) || !isManualCheck(check) || (result != "pass" && result != "fail") ||
		!validSingleLine(operator, 128) || !validSingleLine(procedure, 256) {
		return manualCheckRecord{}, "", errors.New("manual native check input is invalid")
	}
	receipt, receiptBytes, err := readEvidenceReceipt(dist)
	if err != nil {
		return manualCheckRecord{}, "", err
	}
	artifact, ok := receiptArtifact(receipt, target)
	if !ok {
		return manualCheckRecord{}, "", errors.New("manual native check target is absent from receipt")
	}
	archivePath := filepath.Join(dist, "artifacts", artifact.Name)
	archive, err := readReleaseArchive(archivePath, releaseBinaryName(receipt.Environment, target.OS), defaultArtifactLimits)
	if err != nil {
		return manualCheckRecord{}, "", err
	}
	if archive.sha256 != artifact.SHA256 {
		return manualCheckRecord{}, "", errors.New("manual native check archive does not match receipt")
	}
	binaryName := releaseBinaryName(receipt.Environment, target.OS)
	reportID := fmt.Sprintf("manual-%d", time.Now().UTC().UnixNano())
	record := manualCheckRecord{
		SchemaVersion: 1, ReportID: reportID, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Environment: receipt.Environment, Commit: receipt.Commit, ReceiptSHA256: sha256Hex(receiptBytes), Target: target,
		ArchiveName: artifact.Name, ArchiveSHA256: artifact.SHA256, BinaryName: binaryName,
		BinarySHA256: sha256Hex(archive.members[binaryName].data), Check: check, Procedure: procedure, Result: result, Operator: operator,
	}
	relative := filepath.ToSlash(filepath.Join("native", "manual", target.OS+"-"+target.Arch, reportID+".json"))
	return record, relative, nil
}

func collectManualEvidence(dist, environment string) ([][]acceptedManualCheck, error) {
	result := make([][]acceptedManualCheck, len(releaseTargets))
	for index := range result {
		result[index] = []acceptedManualCheck{}
	}
	root := filepath.Join(dist, "native", "manual")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, errors.New("read native manual evidence")
	}
	seenIDs := make(map[string]bool)
	for _, targetEntry := range entries {
		if !targetEntry.IsDir() || targetEntry.Type()&os.ModeSymlink != 0 {
			return nil, errors.New("native manual evidence layout is invalid")
		}
		target, targetIndex, ok := targetForDirectory(targetEntry.Name())
		if !ok {
			return nil, errors.New("native manual target directory is invalid")
		}
		files, err := os.ReadDir(filepath.Join(root, targetEntry.Name()))
		if err != nil {
			return nil, errors.New("read native manual target evidence")
		}
		selected := make(map[string]acceptedManualCheck)
		for _, file := range files {
			if file.IsDir() || file.Type()&os.ModeSymlink != 0 || filepath.Ext(file.Name()) != ".json" {
				return nil, errors.New("native manual evidence file is invalid")
			}
			relative := filepath.ToSlash(filepath.Join("native", "manual", targetEntry.Name(), file.Name()))
			record, hash, err := readManualCheckRecord(dist, relative, environment, target)
			if err != nil {
				return nil, err
			}
			if seenIDs[record.ReportID] {
				return nil, errors.New("native manual report identity is duplicated")
			}
			seenIDs[record.ReportID] = true
			if record.Result != "pass" {
				continue
			}
			if procedure, required := canonicalManualProcedure(target, record.Check); required && record.Procedure != procedure {
				continue
			}
			candidate := acceptedManualCheck{
				ReportID: record.ReportID, Path: relative, RecordSHA256: hash,
				ArchiveName: record.ArchiveName, ArchiveSHA256: record.ArchiveSHA256,
				BinaryName: record.BinaryName, BinarySHA256: record.BinarySHA256,
				Check: record.Check, Procedure: record.Procedure, Result: record.Result, Operator: record.Operator,
			}
			current, exists := selected[record.Check]
			if !exists || candidate.ReportID < current.ReportID {
				selected[record.Check] = candidate
			}
		}
		for _, check := range nativeCheckNames {
			if accepted, ok := selected[check]; ok {
				result[targetIndex] = append(result[targetIndex], accepted)
			}
		}
	}
	return result, nil
}

func readManualCheckRecord(dist, relative, environment string, target buildTarget) (manualCheckRecord, string, error) {
	path := filepath.Join(dist, filepath.FromSlash(relative))
	contents, err := readBoundedRegularFile(path, maxNativeRecordBytes)
	if err != nil {
		return manualCheckRecord{}, "", errors.New("read native manual check")
	}
	var record manualCheckRecord
	if err := decodeStrictJSON(contents, &record, "native manual check"); err != nil {
		return manualCheckRecord{}, "", err
	}
	receipt, receiptBytes, err := readEvidenceReceipt(dist)
	if err != nil {
		return manualCheckRecord{}, "", err
	}
	artifact, ok := receiptArtifact(receipt, target)
	if !ok {
		return manualCheckRecord{}, "", errors.New("native manual check target is absent")
	}
	binaryName := releaseBinaryName(environment, target.OS)
	archive, err := readReleaseArchive(filepath.Join(dist, "artifacts", artifact.Name), binaryName, defaultArtifactLimits)
	if err != nil {
		return manualCheckRecord{}, "", err
	}
	if archive.sha256 != artifact.SHA256 {
		return manualCheckRecord{}, "", errors.New("native manual check archive does not match receipt")
	}
	expectedBinaryHash := sha256Hex(archive.members[binaryName].data)
	if record.SchemaVersion != 1 || record.Environment != environment || record.Commit != receipt.Commit ||
		record.ReceiptSHA256 != sha256Hex(receiptBytes) || record.Target != target ||
		record.ArchiveName != artifact.Name || record.ArchiveSHA256 != artifact.SHA256 ||
		record.BinaryName != binaryName || record.BinarySHA256 != expectedBinaryHash ||
		!validFact(record.ReportID, false) || filepath.Base(relative) != record.ReportID+".json" ||
		!isManualCheck(record.Check) || (record.Result != "pass" && record.Result != "fail") ||
		!validSingleLine(record.Operator, 128) || !validSingleLine(record.Procedure, 256) {
		return manualCheckRecord{}, "", errors.New("native manual check identity is invalid")
	}
	if _, err := time.Parse(time.RFC3339, record.CreatedAt); err != nil {
		return manualCheckRecord{}, "", errors.New("native manual check time is invalid")
	}
	return record, sha256Hex(contents), nil
}

func validateAcceptedManualCheck(dist string, target buildTarget, accepted acceptedManualCheck) error {
	expectedPath := filepath.ToSlash(filepath.Join("native", "manual", target.OS+"-"+target.Arch, accepted.ReportID+".json"))
	if accepted.Path != expectedPath || !validReportID(accepted.ReportID) || accepted.Result != "pass" {
		return errors.New("accepted native manual evidence path is invalid")
	}
	if procedure, required := canonicalManualProcedure(target, accepted.Check); required && accepted.Procedure != procedure {
		return fmt.Errorf("native manual check %q requires procedure %s", accepted.Check, procedure)
	}
	record, hash, err := readManualCheckRecord(dist, accepted.Path, retainedEnvironment(dist), target)
	if err != nil {
		return err
	}
	if procedure, required := canonicalManualProcedure(target, record.Check); required && record.Procedure != procedure {
		return fmt.Errorf("native manual check %q requires procedure %s", record.Check, procedure)
	}
	current := acceptedManualCheck{
		ReportID: record.ReportID, Path: accepted.Path, RecordSHA256: hash,
		ArchiveName: record.ArchiveName, ArchiveSHA256: record.ArchiveSHA256,
		BinaryName: record.BinaryName, BinarySHA256: record.BinarySHA256,
		Check: record.Check, Procedure: record.Procedure, Result: record.Result, Operator: record.Operator,
	}
	if current != accepted {
		return errors.New("accepted native manual evidence changed")
	}
	return nil
}

func canonicalManualProcedure(target buildTarget, check string) (string, bool) {
	switch check {
	case "network":
		return "network_release_v1", true
	case "config-private":
		if target.OS == "windows" {
			return "windows_config_private_v1", true
		}
	case "password-input":
		if target.OS == "windows" {
			return "windows_password_input_v1", true
		}
	case "signal-cancel":
		if target.OS == "windows" {
			return "windows_signal_cancel_v1", true
		}
	}
	return "", false
}

func isManualCheck(check string) bool {
	return check != "version" && check != "help" && containsString(nativeCheckNames, check)
}

func validSingleLine(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func decodeStrictJSON(contents []byte, destination any, name string) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode %s", name)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s must contain one JSON value", name)
	}
	return nil
}

func sortedChecks(checks []string) []string {
	result := append(make([]string, 0, len(checks)), checks...)
	sort.Slice(result, func(i, j int) bool {
		return nativeCheckIndex(result[i]) < nativeCheckIndex(result[j])
	})
	return result
}

func nativeCheckIndex(check string) int {
	for index, candidate := range nativeCheckNames {
		if check == candidate {
			return index
		}
	}
	return len(nativeCheckNames)
}
