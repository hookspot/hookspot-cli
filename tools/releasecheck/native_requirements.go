package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type nativeReview struct {
	SchemaVersion    int                  `json:"schema_version"`
	CreatedAt        string               `json:"created_at"`
	Operator         string               `json:"operator"`
	SourceRepository string               `json:"source_repository"`
	Environment      string               `json:"environment"`
	BaselineCommit   string               `json:"baseline_commit"`
	CurrentCommit    string               `json:"current_commit"`
	Rationale        string               `json:"rationale"`
	Targets          []nativeReviewTarget `json:"targets"`
}

type nativeReviewTarget struct {
	Target         buildTarget `json:"target"`
	Available      bool        `json:"available"`
	Affected       bool        `json:"affected"`
	Reason         string      `json:"reason"`
	RequiredChecks []string    `json:"required_checks"`
}

func runNativeReviewTemplate(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("releasecheck native-review-template", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	currentDist := flags.String("dist", "", "current retained output parent")
	baselineDist := flags.String("baseline-dist", "", "first-support retained output parent")
	output := flags.String("output", "", "new routine review template")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*currentDist) ||
		!filepath.IsAbs(*baselineDist) || !filepath.IsAbs(*output) {
		return errors.New("native-review-template requires absolute --dist, --baseline-dist, and --output")
	}
	if err := validateNoParentTraversal(*output); err != nil {
		return fmt.Errorf("review template output path is unsafe: %w", err)
	}
	if err := validateExistingDirectoryPath(filepath.Dir(*output)); err != nil {
		return fmt.Errorf("review template output parent is unsafe: %w", err)
	}
	if err := validateExistingDirectoryPath(*currentDist); err != nil {
		return fmt.Errorf("current retained output parent is unsafe: %w", err)
	}
	if err := validateExistingDirectoryPath(*baselineDist); err != nil {
		return fmt.Errorf("baseline retained output parent is unsafe: %w", err)
	}
	if isFilesystemRoot(*currentDist) || isFilesystemRoot(*baselineDist) {
		return errors.New("review template retained parent cannot be filesystem root")
	}
	if !disjointPath(*currentDist, *baselineDist) {
		return errors.New("current and baseline retained trees must be disjoint")
	}
	if !disjointPath(filepath.Dir(*output), *currentDist) || !disjointPath(filepath.Dir(*output), *baselineDist) {
		return errors.New("review template output must be outside retained release trees")
	}
	if _, err := os.Lstat(*output); err == nil {
		return errors.New("review template output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect review template output")
	}
	current, _, err := readEvidenceReceipt(*currentDist)
	if err != nil {
		return errors.New("read current retained receipt")
	}
	if err := verifyReceipt(*currentDist, current.Environment); err != nil {
		return fmt.Errorf("verify current retained release: %w", err)
	}
	baseline, _, _, err := validateFirstSupportBaseline(*baselineDist)
	if err != nil {
		return err
	}
	if !sameNativeBaselineScope(current, baseline) {
		return errors.New("native baseline repository and environment must match current release")
	}
	review := nativeReview{
		SchemaVersion: 1, SourceRepository: current.SourceRepository, Environment: current.Environment,
		BaselineCommit: baseline.Commit, CurrentCommit: current.Commit,
		Targets: make([]nativeReviewTarget, len(releaseTargets)),
	}
	for index, target := range releaseTargets {
		review.Targets[index] = nativeReviewTarget{
			Target: target, Affected: true, RequiredChecks: append([]string(nil), nativeCheckNames...),
		}
	}
	if err := writeJSONExclusive(*output, review); err != nil {
		return errors.New("write review template")
	}
	_, err = fmt.Fprintf(out, "native review template created and remains incomplete until every blank field is reviewed: %s\n", *output)
	return err
}

func validateFirstSupportBaseline(dist string) (buildReceipt, []byte, nativeEvidence, error) {
	receipt, receiptBytes, err := readEvidenceReceipt(dist)
	if err != nil {
		return buildReceipt{}, nil, nativeEvidence{}, errors.New("read native baseline receipt")
	}
	if err := verifyReceipt(dist, receipt.Environment); err != nil {
		return buildReceipt{}, nil, nativeEvidence{}, fmt.Errorf("verify native baseline release: %w", err)
	}
	if _, err := os.Lstat(filepath.Join(dist, "native", "baseline")); err == nil || !errors.Is(err, os.ErrNotExist) {
		return buildReceipt{}, nil, nativeEvidence{}, errors.New("nested native baseline is not allowed")
	}
	requirements, err := readNativeRequirements(dist)
	if err != nil || requirements.Mode != "first-support" {
		return buildReceipt{}, nil, nativeEvidence{}, errors.New("native baseline requirements must be first-support")
	}
	evidence, err := readNativeEvidence(dist)
	if err != nil {
		return buildReceipt{}, nil, nativeEvidence{}, errors.New("read native baseline evidence")
	}
	if err := validateNativeEvidence(dist, requirements, evidence); err != nil {
		return buildReceipt{}, nil, nativeEvidence{}, fmt.Errorf("validate native baseline evidence: %w", err)
	}
	if len(evidence.Targets) != len(releaseTargets) {
		return buildReceipt{}, nil, nativeEvidence{}, errors.New("native baseline must contain all six targets")
	}
	for _, row := range evidence.Targets {
		if row.Report == nil || row.Execution != "native" {
			return buildReceipt{}, nil, nativeEvidence{}, errors.New("native baseline must contain native version and help evidence for all targets")
		}
	}
	return receipt, receiptBytes, evidence, nil
}

func createRoutineRequirements(options nativeRequirementsOptions, current buildReceipt, currentBytes []byte) error {
	if err := validateExistingDirectoryPath(options.dist); err != nil {
		return errors.New("current retained output parent is unsafe")
	}
	if err := validateExistingDirectoryPath(options.baselineDist); err != nil {
		return errors.New("native baseline parent is unsafe")
	}
	if err := validateNoParentTraversal(options.review); err != nil {
		return fmt.Errorf("native review path is unsafe: %w", err)
	}
	if err := validateExistingDirectoryPath(filepath.Dir(options.review)); err != nil {
		return errors.New("native review parent is unsafe")
	}
	if isFilesystemRoot(options.dist) || isFilesystemRoot(options.baselineDist) {
		return errors.New("routine retained parent cannot be filesystem root")
	}
	if !disjointPath(options.dist, options.baselineDist) || pathContains(filepath.Clean(options.dist), filepath.Clean(options.review)) {
		return errors.New("routine inputs must not expose the current retained tree as writable")
	}
	for _, destination := range []string{
		filepath.Join(options.dist, "native", "baseline"),
		filepath.Join(options.dist, "native", "review.json"),
		filepath.Join(options.dist, "native", "requirements.json"),
	} {
		if _, err := os.Lstat(destination); err == nil || !errors.Is(err, os.ErrNotExist) {
			return errors.New("routine native destination already exists")
		}
	}
	reviewBytes, err := readBoundedRegularFile(options.review, maxNativeRecordBytes)
	if err != nil {
		return errors.New("read native review")
	}
	var review nativeReview
	if err := decodeStrictJSON(reviewBytes, &review, "native review"); err != nil {
		return err
	}
	baseline, baselineBytes, baselineEvidence, err := validateFirstSupportBaseline(options.baselineDist)
	if err != nil {
		return err
	}
	if !sameNativeBaselineScope(current, baseline) {
		return errors.New("native baseline repository and environment must match current release")
	}
	if err := validateNativeReview(review, current, baseline); err != nil {
		return err
	}

	targets := make([]nativeRequirementTarget, len(releaseTargets))
	for index, reviewed := range review.Targets {
		targets[index] = nativeRequirementTarget{
			Target: reviewed.Target, Available: reviewed.Available, Affected: reviewed.Affected,
			Reason: reviewed.Reason, RequiredChecks: sortedChecks(reviewed.RequiredChecks),
		}
	}
	requirements := nativeRequirements{
		SchemaVersion: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339), Mode: "routine",
		SourceRepository: current.SourceRepository, Environment: current.Environment, Commit: current.Commit,
		ReceiptSHA256: sha256Hex(currentBytes), BaselineRepository: baseline.SourceRepository,
		BaselineEnvironment: baseline.Environment, BaselineVersion: baseline.Version, BaselineCommit: baseline.Commit,
		BaselineReceiptSHA256: sha256Hex(baselineBytes), ReviewSHA256: sha256Hex(reviewBytes),
		ReviewOperator: review.Operator, ReviewRationale: review.Rationale, Targets: targets,
	}
	if err := importNativeBaseline(options.dist, options.baselineDist, baseline, baselineEvidence); err != nil {
		return err
	}
	if err := writeBytesExclusive(filepath.Join(options.dist, "native", "review.json"), reviewBytes); err != nil {
		return errors.New("retain native review")
	}
	if err := writeRequirements(options.dist, requirements); err != nil {
		return err
	}
	_, err = readNativeRequirements(options.dist)
	return err
}

func sameNativeBaselineScope(current, baseline buildReceipt) bool {
	return current.SourceRepository == baseline.SourceRepository && current.Environment == baseline.Environment
}

func validateRetainedRoutineInputs(dist string, requirements nativeRequirements, current buildReceipt) error {
	reviewBytes, err := readBoundedRegularFile(filepath.Join(dist, "native", "review.json"), maxNativeRecordBytes)
	if err != nil || sha256Hex(reviewBytes) != requirements.ReviewSHA256 {
		return errors.New("routine native review is missing or changed")
	}
	var review nativeReview
	if err := decodeStrictJSON(reviewBytes, &review, "native review"); err != nil {
		return err
	}
	baselineDist := filepath.Join(dist, "native", "baseline")
	if _, err := os.Lstat(filepath.Join(baselineDist, "native", "baseline")); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("nested native baseline is not allowed")
	}
	baseline, baselineBytes, err := readEvidenceReceipt(baselineDist)
	if err != nil {
		return errors.New("read retained native baseline")
	}
	if baseline.SourceRepository != current.SourceRepository || baseline.Environment != current.Environment ||
		baseline.SourceRepository != requirements.BaselineRepository || baseline.Environment != requirements.BaselineEnvironment ||
		baseline.Version != requirements.BaselineVersion || baseline.Commit != requirements.BaselineCommit ||
		sha256Hex(baselineBytes) != requirements.BaselineReceiptSHA256 {
		return errors.New("retained native baseline identity is invalid")
	}
	for _, artifact := range baseline.Artifacts {
		hash, err := hashBoundedFile(filepath.Join(baselineDist, "artifacts", artifact.Name), defaultArtifactLimits.maxCompressedBytes)
		if err != nil || hash != artifact.SHA256 {
			return errors.New("retained native baseline asset changed")
		}
	}
	baselineRequirements, err := readNativeRequirements(baselineDist)
	if err != nil || baselineRequirements.Mode != "first-support" {
		return errors.New("retained native baseline requirements are invalid")
	}
	baselineEvidence, err := readNativeEvidence(baselineDist)
	if err != nil {
		return errors.New("read retained native baseline evidence")
	}
	if err := validateNativeEvidence(baselineDist, baselineRequirements, baselineEvidence); err != nil {
		return errors.New("retained native baseline evidence is invalid")
	}
	for _, row := range baselineEvidence.Targets {
		if row.Report == nil || row.Execution != "native" {
			return errors.New("retained baseline lacks full native smoke coverage")
		}
	}
	if err := validateNativeReview(review, current, baseline); err != nil {
		return err
	}
	if requirements.ReviewOperator != review.Operator || requirements.ReviewRationale != review.Rationale {
		return errors.New("routine native requirements differ from retained review")
	}
	for index, row := range requirements.Targets {
		reviewed := review.Targets[index]
		if row.Target != reviewed.Target || row.Available != reviewed.Available || row.Affected != reviewed.Affected ||
			row.Reason != reviewed.Reason || strings.Join(row.RequiredChecks, "\x00") != strings.Join(sortedChecks(reviewed.RequiredChecks), "\x00") {
			return errors.New("routine native requirements differ from retained review")
		}
	}
	return nil
}

func validateNativeReview(review nativeReview, current, baseline buildReceipt) error {
	if review.SchemaVersion != 1 || review.SourceRepository != current.SourceRepository || review.Environment != current.Environment ||
		review.BaselineCommit != baseline.Commit || review.CurrentCommit != current.Commit ||
		!validSingleLine(review.Operator, 128) || !validSingleLine(review.Rationale, 512) || len(review.Targets) != len(releaseTargets) {
		return errors.New("native review identity is invalid")
	}
	if _, err := time.Parse(time.RFC3339, review.CreatedAt); err != nil {
		return errors.New("native review creation time is invalid")
	}
	for index, row := range review.Targets {
		if row.Target != releaseTargets[index] || !validSingleLine(row.Reason, 256) {
			return errors.New("native review target is invalid")
		}
		seen := make(map[string]bool)
		for _, check := range row.RequiredChecks {
			if !containsString(nativeCheckNames, check) || seen[check] {
				return errors.New("native review check is invalid")
			}
			seen[check] = true
		}
		if (row.Available || row.Affected) && (!seen["version"] || !seen["help"]) {
			return errors.New("available or affected native target must require version and help")
		}
		builderTarget := row.Target.OS+"/"+row.Target.Arch == current.BuilderPlatform
		if builderTarget {
			if !seen["version"] || !seen["help"] || !seen["network"] {
				return errors.New("routine native builder target must retain the network requirement")
			}
		} else if !row.Available && !row.Affected && len(row.RequiredChecks) != 0 {
			return errors.New("unchanged unavailable native target must have no current checks")
		}
	}
	return nil
}

func importNativeBaseline(currentDist, baselineDist string, receipt buildReceipt, evidence nativeEvidence) error {
	nativeDir := filepath.Join(currentDist, "native")
	if err := ensurePlainDirectory(nativeDir); err != nil {
		return err
	}
	destination := filepath.Join(nativeDir, "baseline")
	if _, err := os.Lstat(destination); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("native baseline destination already exists")
	}
	temporary, err := os.MkdirTemp(nativeDir, ".baseline-")
	if err != nil {
		return errors.New("create native baseline staging directory")
	}
	defer os.RemoveAll(temporary)
	files := []struct {
		path    string
		maximum int64
	}{
		{path: "receipt.json", maximum: maxReceiptBytes},
		{path: filepath.Join("native", "requirements.json"), maximum: maxNativeRecordBytes},
		{path: filepath.Join("native", "evidence.json"), maximum: maxNativeRecordBytes},
	}
	for _, artifact := range receipt.Artifacts {
		files = append(files, struct {
			path    string
			maximum int64
		}{path: filepath.Join("artifacts", artifact.Name), maximum: defaultArtifactLimits.maxCompressedBytes})
	}
	for _, row := range evidence.Targets {
		if row.Report != nil {
			for _, name := range []string{"record.env", "version.stdout", "version.stderr", "help.stdout", "help.stderr"} {
				maximum := int64(maxSmokeOutputBytes)
				if name == "record.env" {
					maximum = maxSmokeRecordBytes
				}
				files = append(files, struct {
					path    string
					maximum int64
				}{path: filepath.Join(filepath.FromSlash(row.Report.Path), name), maximum: maximum})
			}
		}
		for _, manual := range row.ManualChecks {
			files = append(files, struct {
				path    string
				maximum int64
			}{path: filepath.FromSlash(manual.Path), maximum: maxNativeRecordBytes})
		}
	}
	seen := make(map[string]bool)
	for _, file := range files {
		clean := filepath.Clean(file.path)
		if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || seen[clean] {
			return errors.New("native baseline file set is invalid")
		}
		seen[clean] = true
		contents, err := readBoundedRegularFile(filepath.Join(baselineDist, clean), file.maximum)
		if err != nil {
			return errors.New("read native baseline file")
		}
		target := filepath.Join(temporary, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return errors.New("create native baseline directory")
		}
		if err := writeBytesExclusive(target, contents); err != nil {
			return errors.New("copy native baseline file")
		}
	}
	if err := os.Rename(temporary, destination); err != nil {
		return errors.New("publish native baseline copy")
	}
	return nil
}
