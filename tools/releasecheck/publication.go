package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxPublicationBytes  = 512 * 1024
	maxReleaseNotesBytes = 1024 * 1024
)

type publicationOptions struct {
	dist                string
	environment         string
	notesPath           string
	publisherImageID    string
	publisherPlatform   string
	stageAcceptancePath string
	fromStageTag        string
}

type publicationFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type publicationAsset struct {
	Name          string `json:"name"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
	RemoteAssetID int64  `json:"remote_asset_id,omitempty"`
}

type publicationEligibility struct {
	ReceiptVerified        bool `json:"receipt_verified"`
	NativeEvidenceVerified bool `json:"native_evidence_verified"`
	RepositoryURLsVerified bool `json:"repository_urls_verified"`
	SourcePolicyVerified   bool `json:"source_policy_verified"`
	RemoteAccessVerified   bool `json:"remote_access_verified"`
}

type publicationRecord struct {
	SchemaVersion     int                    `json:"schema_version"`
	CreatedAt         string                 `json:"created_at"`
	Repository        string                 `json:"repository"`
	Environment       string                 `json:"environment"`
	Version           string                 `json:"version"`
	Tag               string                 `json:"tag"`
	TagObject         string                 `json:"tag_object"`
	TagCommit         string                 `json:"tag_commit"`
	ReceiptSHA256     string                 `json:"receipt_sha256"`
	Notes             publicationFile        `json:"notes"`
	ReleaseBody       publicationFile        `json:"release_body"`
	Marker            string                 `json:"marker"`
	EligibilityInputs string                 `json:"eligibility_inputs_sha256"`
	PublisherImageID  string                 `json:"publisher_image_id"`
	PublisherPlatform string                 `json:"publisher_platform"`
	Eligibility       publicationEligibility `json:"eligibility"`
	NativeInputs      []publicationFile      `json:"native_inputs"`
	StageAcceptance   *publicationFile       `json:"stage_acceptance,omitempty"`
	Phase             string                 `json:"phase"`
	ReleaseID         int64                  `json:"release_id,omitempty"`
	Assets            []publicationAsset     `json:"assets"`
}

type stageAcceptanceRecord struct {
	SchemaVersion int                `json:"schema_version"`
	Repository    string             `json:"repository"`
	BaseVersion   string             `json:"base_version"`
	StageTag      string             `json:"stage_tag"`
	TagObject     string             `json:"tag_object"`
	TagCommit     string             `json:"tag_commit"`
	ReleaseID     int64              `json:"release_id"`
	Assets        []publicationAsset `json:"assets"`
	Accepted      bool               `json:"accepted"`
	Reviewer      string             `json:"reviewer"`
	AcceptedAt    string             `json:"accepted_at"`
	Checks        []string           `json:"successful_checks"`
}

type githubAsset struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	State string `json:"state"`
}

type githubRelease struct {
	ID         int64         `json:"id"`
	TagName    string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Body       string        `json:"body"`
	Assets     []githubAsset `json:"assets"`
}

type remotePublicationPlan struct {
	Status  string
	Release *githubRelease
	Missing []string
}

func preparePublication(options publicationOptions) error {
	publication, notesBytes, bodyBytes, acceptanceBytes, err := buildPublication(options)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(options.dist, "publication.json")); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("publication state already exists")
	}
	if err := retainExactFile(filepath.Join(options.dist, publication.Notes.Path), notesBytes); err != nil {
		return errors.New("retain release notes")
	}
	if err := retainExactFile(filepath.Join(options.dist, publication.ReleaseBody.Path), bodyBytes); err != nil {
		return errors.New("retain release body")
	}
	if publication.StageAcceptance != nil {
		if err := retainExactFile(filepath.Join(options.dist, publication.StageAcceptance.Path), acceptanceBytes); err != nil {
			return errors.New("retain stage acceptance")
		}
	}
	return writePublicationJSONExclusive(filepath.Join(options.dist, "publication.json"), publication)
}

func buildPublication(options publicationOptions) (publicationRecord, []byte, []byte, []byte, error) {
	if options.environment != "stage" && options.environment != "prod" {
		return publicationRecord{}, nil, nil, nil, errors.New("publication environment must be stage or prod")
	}
	if err := verifyReceipt(options.dist, options.environment); err != nil {
		return publicationRecord{}, nil, nil, nil, fmt.Errorf("verify publication receipt: %w", err)
	}
	receipt, receiptBytes, err := readEvidenceReceipt(options.dist)
	if err != nil {
		return publicationRecord{}, nil, nil, nil, err
	}
	if receipt.BuildKind != "release" {
		return publicationRecord{}, nil, nil, nil, errors.New("snapshots cannot be published")
	}
	if !validDockerImageID(options.publisherImageID) ||
		(options.publisherPlatform != "linux/amd64" && options.publisherPlatform != "linux/arm64") {
		return publicationRecord{}, nil, nil, nil, errors.New("publisher image identity is invalid")
	}
	requirements, err := readNativeRequirements(options.dist)
	if err != nil {
		return publicationRecord{}, nil, nil, nil, fmt.Errorf("read publication native requirements: %w", err)
	}
	evidence, err := readNativeEvidence(options.dist)
	if err != nil {
		return publicationRecord{}, nil, nil, nil, fmt.Errorf("read publication native evidence: %w", err)
	}
	if err := validateNativeEvidence(options.dist, requirements, evidence); err != nil {
		return publicationRecord{}, nil, nil, nil, fmt.Errorf("validate publication native evidence: %w", err)
	}

	notesBytes, err := readBoundedRegularFile(options.notesPath, maxReleaseNotesBytes)
	if err != nil || len(notesBytes) == 0 || !utf8.Valid(notesBytes) || bytes.IndexByte(notesBytes, 0) >= 0 {
		return publicationRecord{}, nil, nil, nil, errors.New("release notes are missing, unsafe, or invalid")
	}
	notes := publicationFile{Path: "release-notes.md", SHA256: sha256Hex(notesBytes), Size: int64(len(notesBytes))}
	nativeInputs, err := publicationNativeInputs(options.dist, requirements, evidence)
	if err != nil {
		return publicationRecord{}, nil, nil, nil, err
	}
	assets, err := publicationAssets(options.dist, receipt)
	if err != nil {
		return publicationRecord{}, nil, nil, nil, err
	}
	var stageAcceptance *publicationFile
	var acceptanceBytes []byte
	eligibilityInputs := append([]publicationFile(nil), nativeInputs...)
	if options.environment == "prod" {
		if options.stageAcceptancePath == "" || options.fromStageTag == "" {
			return publicationRecord{}, nil, nil, nil, errors.New("production publication requires stage acceptance")
		}
		contents, readErr := readBoundedRegularFile(options.stageAcceptancePath, maxPublicationBytes)
		if readErr != nil {
			return publicationRecord{}, nil, nil, nil, errors.New("read stage acceptance")
		}
		acceptance, decodeErr := decodeStageAcceptance(contents)
		if decodeErr != nil || acceptance.StageTag != options.fromStageTag || validateStageAcceptanceForReceipt(acceptance, receipt) != nil {
			return publicationRecord{}, nil, nil, nil, errors.New("stage acceptance is invalid for production release")
		}
		acceptanceBytes = contents
		stageAcceptance = &publicationFile{Path: "stage-acceptance.json", SHA256: sha256Hex(contents), Size: int64(len(contents))}
		eligibilityInputs = append(eligibilityInputs, *stageAcceptance)
	} else if options.stageAcceptancePath != "" {
		return publicationRecord{}, nil, nil, nil, errors.New("stage publication does not accept production stage evidence")
	}
	inputsSHA := publicationInputsSHA(eligibilityInputRecords(eligibilityInputs))
	marker := publicationMarker(sha256Hex(receiptBytes), notes.SHA256, receipt.TagObject, inputsSHA)
	bodyBytes := append([]byte(nil), notesBytes...)
	if bodyBytes[len(bodyBytes)-1] != '\n' {
		bodyBytes = append(bodyBytes, '\n')
	}
	bodyBytes = append(bodyBytes, []byte("\n<!-- hookspot-publication:"+marker+" -->\n")...)
	body := publicationFile{Path: "release-body.md", SHA256: sha256Hex(bodyBytes), Size: int64(len(bodyBytes))}

	publication := publicationRecord{
		SchemaVersion: 1, CreatedAt: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339),
		Repository: receipt.SourceRepository, Environment: receipt.Environment, Version: receipt.Version,
		Tag: receipt.Tag, TagObject: receipt.TagObject, TagCommit: receipt.Commit,
		ReceiptSHA256: sha256Hex(receiptBytes), Notes: notes, ReleaseBody: body, Marker: marker,
		EligibilityInputs: inputsSHA,
		PublisherImageID:  options.publisherImageID, PublisherPlatform: options.publisherPlatform,
		Eligibility: publicationEligibility{
			ReceiptVerified: true, NativeEvidenceVerified: true, RepositoryURLsVerified: true,
			SourcePolicyVerified: true, RemoteAccessVerified: true,
		},
		NativeInputs: nativeInputs, StageAcceptance: stageAcceptance, Phase: "eligible", Assets: assets,
	}
	return publication, notesBytes, bodyBytes, acceptanceBytes, nil
}

func readPublication(dist, environment string) (publicationRecord, error) {
	contents, err := readBoundedRegularFile(filepath.Join(dist, "publication.json"), maxPublicationBytes)
	if err != nil {
		return publicationRecord{}, errors.New("read publication state")
	}
	var publication publicationRecord
	if err := decodeStrictJSON(contents, &publication, "publication state"); err != nil {
		return publicationRecord{}, err
	}
	if err := validatePublication(dist, environment, publication); err != nil {
		return publicationRecord{}, err
	}
	return publication, nil
}

func validatePublication(dist, environment string, publication publicationRecord) error {
	if err := verifyReceipt(dist, environment); err != nil {
		return err
	}
	receipt, receiptBytes, err := readEvidenceReceipt(dist)
	if err != nil {
		return err
	}
	if publication.SchemaVersion != 1 || publication.Repository != receipt.SourceRepository ||
		publication.Environment != receipt.Environment || publication.Environment != environment ||
		publication.Version != receipt.Version || publication.Tag != receipt.Tag ||
		publication.TagObject != receipt.TagObject || publication.TagCommit != receipt.Commit ||
		publication.ReceiptSHA256 != sha256Hex(receiptBytes) || publication.Notes.Path != "release-notes.md" ||
		publication.ReleaseBody.Path != "release-body.md" || publication.Notes.Size <= 0 || publication.ReleaseBody.Size <= 0 ||
		!validDockerImageID(publication.PublisherImageID) ||
		(publication.PublisherPlatform != "linux/amd64" && publication.PublisherPlatform != "linux/arm64") {
		return errors.New("publication state does not match retained release")
	}
	publicationTime, err := parseReceiptTime(publication.CreatedAt)
	receiptTime, receiptTimeErr := parseReceiptTime(receipt.CreatedAt)
	if err != nil || receiptTimeErr != nil || publicationTime.Before(receiptTime) {
		return errors.New("publication creation time is invalid")
	}
	wantEligibility := publicationEligibility{true, true, true, true, true}
	if publication.Eligibility != wantEligibility {
		return errors.New("publication eligibility is incomplete")
	}
	if publication.Phase != "eligible" && publication.Phase != "draft" && publication.Phase != "published" {
		return errors.New("publication phase is invalid")
	}
	if publication.Phase == "eligible" && publication.ReleaseID != 0 ||
		publication.Phase != "eligible" && publication.ReleaseID <= 0 {
		return errors.New("publication release identity is invalid")
	}
	eligibilityInputs := append([]publicationFile(nil), publication.NativeInputs...)
	if publication.StageAcceptance != nil {
		eligibilityInputs = append(eligibilityInputs, *publication.StageAcceptance)
	}
	if publication.EligibilityInputs != publicationInputsSHA(eligibilityInputRecords(eligibilityInputs)) ||
		publication.Marker != publicationMarker(publication.ReceiptSHA256, publication.Notes.SHA256, publication.TagObject, publication.EligibilityInputs) {
		return errors.New("publication ownership marker is invalid")
	}
	if err := verifyPublicationFile(dist, publication.Notes, maxReleaseNotesBytes); err != nil {
		return errors.New("retained release notes changed")
	}
	if err := verifyPublicationFile(dist, publication.ReleaseBody, maxReleaseNotesBytes+1024); err != nil {
		return errors.New("retained release body changed")
	}
	bodyBytes, err := readBoundedRegularFile(filepath.Join(dist, publication.ReleaseBody.Path), maxReleaseNotesBytes+1024)
	if err != nil || !bytes.Contains(bodyBytes, []byte("<!-- hookspot-publication:"+publication.Marker+" -->")) {
		return errors.New("retained release body marker changed")
	}
	requirements, err := readNativeRequirements(dist)
	if err != nil {
		return err
	}
	evidence, err := readNativeEvidence(dist)
	if err != nil {
		return err
	}
	if err := validateNativeEvidence(dist, requirements, evidence); err != nil {
		return err
	}
	wantInputs, err := publicationNativeInputs(dist, requirements, evidence)
	if err != nil || !reflect.DeepEqual(wantInputs, publication.NativeInputs) {
		return errors.New("publication native evidence changed")
	}
	wantAssets, err := publicationAssets(dist, receipt)
	if err != nil || !samePublicationAssets(wantAssets, publication.Assets) {
		return errors.New("publication assets changed")
	}
	remoteIDs := make(map[int64]bool)
	for _, asset := range publication.Assets {
		if publication.Phase == "eligible" && asset.RemoteAssetID != 0 ||
			publication.Phase == "published" && asset.RemoteAssetID <= 0 {
			return errors.New("publication asset identity does not match its phase")
		}
		if asset.RemoteAssetID > 0 {
			if remoteIDs[asset.RemoteAssetID] {
				return errors.New("publication repeats a remote asset identity")
			}
			remoteIDs[asset.RemoteAssetID] = true
		}
	}
	if environment == "stage" {
		if publication.StageAcceptance != nil {
			return errors.New("stage publication contains production acceptance")
		}
	} else {
		if publication.StageAcceptance == nil || publication.StageAcceptance.Path != "stage-acceptance.json" ||
			verifyPublicationFile(dist, *publication.StageAcceptance, maxPublicationBytes) != nil {
			return errors.New("production stage acceptance changed")
		}
		contents, err := readBoundedRegularFile(filepath.Join(dist, publication.StageAcceptance.Path), maxPublicationBytes)
		acceptance, decodeErr := decodeStageAcceptance(contents)
		if err != nil || decodeErr != nil || validateStageAcceptanceForReceipt(acceptance, receipt) != nil {
			return errors.New("production stage acceptance is invalid")
		}
	}
	return nil
}

func publicationMarker(receiptSHA, notesSHA, tagObject, inputsSHA string) string {
	return sha256Hex([]byte("hookspot-publication-v1\n" + receiptSHA + "\n" + notesSHA + "\n" + tagObject + "\n" + inputsSHA + "\n"))
}

func eligibilityInputRecords(inputs []publicationFile) []publicationFile {
	records := append([]publicationFile(nil), inputs...)
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	return records
}

func publicationInputsSHA(inputs []publicationFile) string {
	var builder strings.Builder
	for _, input := range inputs {
		fmt.Fprintf(&builder, "%s\x00%s\n", input.Path, input.SHA256)
	}
	return sha256Hex([]byte(builder.String()))
}

func decodeStageAcceptance(contents []byte) (stageAcceptanceRecord, error) {
	var acceptance stageAcceptanceRecord
	if err := decodeStrictJSON(contents, &acceptance, "stage acceptance"); err != nil {
		return stageAcceptanceRecord{}, err
	}
	return acceptance, nil
}

func createStageAcceptanceTemplate(dist, output string) error {
	if err := validateNoParentTraversal(output); err != nil {
		return errors.New("stage acceptance output path contains parent traversal")
	}
	if err := validateExistingDirectoryPath(dist); err != nil {
		return errors.New("stage retained output parent is unsafe")
	}
	if err := validateExistingDirectoryPath(filepath.Dir(output)); err != nil {
		return errors.New("stage acceptance output parent is unsafe")
	}
	if isFilesystemRoot(dist) {
		return errors.New("stage retained output parent cannot be filesystem root")
	}
	if !disjointPath(dist, filepath.Dir(output)) {
		return errors.New("stage acceptance output must be outside retained release tree")
	}
	publication, err := readPublication(dist, "stage")
	if err != nil {
		return err
	}
	if publication.Phase != "published" || publication.ReleaseID <= 0 {
		return errors.New("stage publication is not complete")
	}
	assets := append([]publicationAsset(nil), publication.Assets...)
	for _, asset := range assets {
		if asset.RemoteAssetID <= 0 {
			return errors.New("stage publication asset identity is incomplete")
		}
	}
	base := strings.TrimSuffix(publication.Version, stageTagSuffix(publication.Version))
	template := stageAcceptanceRecord{
		SchemaVersion: 1, Repository: publication.Repository, BaseVersion: base,
		StageTag: publication.Tag, TagObject: publication.TagObject, TagCommit: publication.TagCommit,
		ReleaseID: publication.ReleaseID, Assets: assets, Accepted: false,
		Reviewer: "", AcceptedAt: "", Checks: []string{},
	}
	return writeJSONExclusive(output, template)
}

func validateStageAcceptanceForReceipt(acceptance stageAcceptanceRecord, receipt buildReceipt) error {
	base := strings.TrimPrefix(strings.TrimSuffix(acceptance.StageTag, stageTagSuffix(acceptance.StageTag)), "v")
	if acceptance.SchemaVersion != 1 || !acceptance.Accepted || acceptance.Repository != receipt.SourceRepository ||
		acceptance.BaseVersion != receipt.Version || base != receipt.Version || !strings.HasPrefix(acceptance.StageTag, "v") ||
		!stageVersionPattern.MatchString(strings.TrimPrefix(acceptance.StageTag, "v")) ||
		acceptance.TagCommit != receipt.Commit || !commitPattern.MatchString(acceptance.TagObject) || acceptance.ReleaseID <= 0 ||
		!validNamedDisplayFact(acceptance.Reviewer) || len(acceptance.Checks) == 0 {
		return errors.New("stage acceptance identity is invalid")
	}
	if _, err := parseReceiptTime(acceptance.AcceptedAt); err != nil {
		return errors.New("stage acceptance time is invalid")
	}
	seen := make(map[string]bool)
	remoteIDs := make(map[int64]bool)
	for _, check := range acceptance.Checks {
		if !validNamedDisplayFact(check) || seen[check] {
			return errors.New("stage acceptance checks are invalid")
		}
		seen[check] = true
	}
	if len(acceptance.Assets) != 7 {
		return errors.New("stage acceptance assets are incomplete")
	}
	stageVersion := strings.TrimPrefix(acceptance.StageTag, "v")
	wantNames := make([]string, 0, 7)
	for _, target := range releaseTargets {
		wantNames = append(wantNames, releaseArchiveName("stage", stageVersion, target))
	}
	wantNames = append(wantNames, "hookspot_stage_"+stageVersion+"_checksums.txt")
	sort.Strings(wantNames)
	for index, asset := range acceptance.Assets {
		if asset.Name != wantNames[index] || index > 0 && acceptance.Assets[index-1].Name >= asset.Name || !validPublicationBasename(asset.Name) ||
			!validDigest(asset.SHA256) || asset.Size <= 0 || asset.RemoteAssetID <= 0 {
			return errors.New("stage acceptance asset is invalid")
		}
		if remoteIDs[asset.RemoteAssetID] {
			return errors.New("stage acceptance repeats a remote asset identity")
		}
		remoteIDs[asset.RemoteAssetID] = true
	}
	return nil
}

func retainExactFile(path string, contents []byte) error {
	if current, err := readBoundedRegularFile(path, int64(len(contents))); err == nil {
		if bytes.Equal(current, contents) {
			return nil
		}
		return errors.New("retained file conflicts")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writePublicationBytesExclusive(path, contents)
}

func writePublicationJSONExclusive(path string, value any) error {
	return writePublicationExclusive(path, func(writer io.Writer) error {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	})
}

func writePublicationBytesExclusive(path string, contents []byte) error {
	return writePublicationExclusive(path, func(writer io.Writer) error {
		_, err := writer.Write(contents)
		return err
	})
}

func writePublicationExclusive(path string, write func(io.Writer) error) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".publication-initial-*.tmp")
	if err != nil {
		return errors.New("create initial publication temporary file")
	}
	temporaryName := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
	}()
	if err := write(temporary); err != nil {
		return errors.New("write initial publication temporary file")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("sync initial publication temporary file")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close initial publication temporary file")
	}
	if err := os.Link(temporaryName, path); err != nil {
		if _, statErr := os.Lstat(path); statErr == nil {
			return errors.New("publication state already exists")
		}
		return errors.New("install initial publication state")
	}
	return nil
}

func writeJSONAtomic(path string, value any) (result error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".publication-*.tmp")
	if err != nil {
		return errors.New("create publication update")
	}
	temporaryName := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if result != nil {
			_ = os.Remove(temporaryName)
		}
	}()
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return errors.New("encode publication update")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("sync publication update")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close publication update")
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return errors.New("replace publication state")
	}
	return nil
}

func readGithubReleasePages(path string) ([]githubRelease, error) {
	contents, err := readBoundedRegularFile(path, 2*1024*1024)
	if err != nil {
		return nil, errors.New("read GitHub release state")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	var pages [][]githubRelease
	if err := decoder.Decode(&pages); err != nil {
		return nil, errors.New("decode GitHub release state")
	}
	if len(pages) == 0 {
		return nil, errors.New("GitHub release state has no pages")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("GitHub release state must contain one value")
	}
	var releases []githubRelease
	for _, page := range pages {
		if page == nil {
			return nil, errors.New("GitHub release state contains a missing page")
		}
		releases = append(releases, page...)
	}
	return releases, nil
}

func reconcilePublication(dist, environment, remotePath, downloads string, allowMissing bool) (string, []string, error) {
	publication, err := readPublication(dist, environment)
	if err != nil {
		return "", nil, err
	}
	releases, err := readGithubReleasePages(remotePath)
	if err != nil {
		return "", nil, err
	}
	plan, err := planRemotePublication(publication, dist, releases)
	if err != nil {
		return "", nil, err
	}
	if plan.Release == nil {
		return "absent", nil, nil
	}
	publication, missing, err := applyRemotePublication(publication, plan, downloads, allowMissing)
	if err != nil {
		return "", nil, err
	}
	if err := writeJSONAtomic(filepath.Join(dist, "publication.json"), publication); err != nil {
		return "", nil, err
	}
	return publication.Phase, missing, nil
}

func recoverPublication(options publicationOptions, remotePath, downloads string) error {
	publication, plan, err := planPublicationRecovery(options, remotePath)
	if err != nil {
		return err
	}
	publication, _, err = applyRemotePublication(publication, plan, downloads, true)
	if err != nil {
		return err
	}
	return writePublicationJSONExclusive(filepath.Join(options.dist, "publication.json"), publication)
}

func planPublicationRecovery(options publicationOptions, remotePath string) (publicationRecord, remotePublicationPlan, error) {
	publication, notesBytes, bodyBytes, acceptanceBytes, err := buildPublication(options)
	if err != nil {
		return publicationRecord{}, remotePublicationPlan{}, err
	}
	if !sameRetainedPublicationFile(filepath.Join(options.dist, publication.Notes.Path), notesBytes, maxReleaseNotesBytes) ||
		!sameRetainedPublicationFile(filepath.Join(options.dist, publication.ReleaseBody.Path), bodyBytes, maxReleaseNotesBytes+1024) {
		return publicationRecord{}, remotePublicationPlan{}, errors.New("retained publication inputs are incomplete or changed")
	}
	if publication.StageAcceptance != nil &&
		!sameRetainedPublicationFile(filepath.Join(options.dist, publication.StageAcceptance.Path), acceptanceBytes, maxPublicationBytes) {
		return publicationRecord{}, remotePublicationPlan{}, errors.New("retained stage acceptance is incomplete or changed")
	}
	releases, err := readGithubReleasePages(remotePath)
	if err != nil {
		return publicationRecord{}, remotePublicationPlan{}, err
	}
	plan, err := planRemotePublication(publication, options.dist, releases)
	if err != nil {
		return publicationRecord{}, remotePublicationPlan{}, err
	}
	if plan.Release == nil {
		return publicationRecord{}, remotePublicationPlan{}, errors.New("owned remote publication is absent")
	}
	return publication, plan, nil
}

func sameRetainedPublicationFile(path string, want []byte, maximum int64) bool {
	contents, err := readBoundedRegularFile(path, maximum)
	return err == nil && bytes.Equal(contents, want)
}

func publicationRecoveryOptions(dist, environment, publisherImageID, publisherPlatform string) (publicationOptions, error) {
	options := publicationOptions{
		dist: dist, environment: environment, notesPath: filepath.Join(dist, "release-notes.md"),
		publisherImageID: publisherImageID, publisherPlatform: publisherPlatform,
	}
	if environment != "prod" {
		return options, nil
	}
	options.stageAcceptancePath = filepath.Join(dist, "stage-acceptance.json")
	contents, err := readBoundedRegularFile(options.stageAcceptancePath, maxPublicationBytes)
	if err != nil {
		return publicationOptions{}, errors.New("read retained stage acceptance")
	}
	acceptance, err := decodeStageAcceptance(contents)
	if err != nil {
		return publicationOptions{}, err
	}
	options.fromStageTag = acceptance.StageTag
	return options, nil
}

func applyRemotePublication(publication publicationRecord, plan remotePublicationPlan, downloads string, allowMissing bool) (publicationRecord, []string, error) {
	remote := plan.Release
	want := make(map[string]int, len(publication.Assets))
	for index, asset := range publication.Assets {
		want[asset.Name] = index
	}
	seen := make(map[string]bool)
	for _, asset := range remote.Assets {
		index, ok := want[asset.Name]
		if !ok || seen[asset.Name] || asset.ID <= 0 || asset.State != "uploaded" || asset.Size != publication.Assets[index].Size {
			return publicationRecord{}, nil, errors.New("remote release contains a conflicting asset")
		}
		seen[asset.Name] = true
		contents, readErr := readBoundedRegularFile(filepath.Join(downloads, asset.Name), defaultArtifactLimits.maxCompressedBytes)
		if readErr != nil || int64(len(contents)) != asset.Size || sha256Hex(contents) != publication.Assets[index].SHA256 {
			return publicationRecord{}, nil, fmt.Errorf("downloaded remote asset %s does not match retained bytes", asset.Name)
		}
		publication.Assets[index].RemoteAssetID = asset.ID
	}
	missing := plan.Missing
	if len(missing) != 0 && (!remote.Draft || !allowMissing) {
		return publicationRecord{}, nil, errors.New("remote release asset set is incomplete")
	}
	wantPrerelease := publication.Environment == "stage"
	if remote.Prerelease != wantPrerelease {
		return publicationRecord{}, nil, errors.New("remote release channel is incorrect")
	}
	publication.ReleaseID = remote.ID
	if remote.Draft {
		publication.Phase = "draft"
	} else {
		if len(missing) != 0 {
			return publicationRecord{}, nil, errors.New("published release is incomplete")
		}
		publication.Phase = "published"
	}
	return publication, missing, nil
}

func planRemotePublication(publication publicationRecord, dist string, releases []githubRelease) (remotePublicationPlan, error) {
	var remote *githubRelease
	for index := range releases {
		if releases[index].TagName == publication.Tag {
			if remote != nil {
				return remotePublicationPlan{}, errors.New("GitHub contains duplicate release identities")
			}
			remote = &releases[index]
		}
	}
	if remote == nil {
		if publication.ReleaseID != 0 || publication.Phase != "eligible" {
			return remotePublicationPlan{}, errors.New("recorded remote release identity is absent")
		}
		return remotePublicationPlan{Status: "absent"}, nil
	}
	if publication.Phase == "published" && remote.Draft {
		return remotePublicationPlan{}, errors.New("published release unexpectedly became a draft")
	}
	if remote.ID <= 0 || remote.Body != mustReadPublicationBody(dist, publication) ||
		publication.ReleaseID != 0 && publication.ReleaseID != remote.ID {
		return remotePublicationPlan{}, errors.New("remote release is not owned by this publication")
	}
	want := make(map[string]publicationAsset, len(publication.Assets))
	for _, asset := range publication.Assets {
		want[asset.Name] = asset
	}
	seen := make(map[string]bool)
	remoteIDs := make(map[int64]bool)
	for _, asset := range remote.Assets {
		expected, ok := want[asset.Name]
		if !ok || seen[asset.Name] || remoteIDs[asset.ID] || asset.ID <= 0 || asset.State != "uploaded" || asset.Size != expected.Size {
			return remotePublicationPlan{}, errors.New("remote release contains a conflicting asset")
		}
		if expected.RemoteAssetID != 0 && expected.RemoteAssetID != asset.ID {
			return remotePublicationPlan{}, errors.New("recorded remote asset identity changed")
		}
		seen[asset.Name] = true
		remoteIDs[asset.ID] = true
	}
	missing := make([]string, 0)
	for _, asset := range publication.Assets {
		if !seen[asset.Name] {
			if asset.RemoteAssetID != 0 {
				return remotePublicationPlan{}, errors.New("recorded remote asset identity is absent")
			}
			missing = append(missing, asset.Name)
		}
	}
	status := "draft"
	if !remote.Draft {
		status = "published"
	}
	return remotePublicationPlan{Status: status, Release: remote, Missing: missing}, nil
}

func validateLatestRelease(publication publicationRecord, path string) error {
	contents, err := readBoundedRegularFile(path, maxPublicationBytes)
	if err != nil {
		return errors.New("read latest release state")
	}
	var latest githubRelease
	decoder := json.NewDecoder(bytes.NewReader(contents))
	if err := decoder.Decode(&latest); err != nil {
		return errors.New("decode latest release state")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("latest release state must contain one value")
	}
	if latest.ID <= 0 || latest.Draft || latest.Prerelease {
		return errors.New("latest release identity is invalid")
	}
	if latest.TagName == publication.Tag {
		return nil
	}
	want, ok := parseStableVersion(publication.Tag)
	if !ok {
		return errors.New("production tag is not stable SemVer")
	}
	current, ok := parseStableVersion(latest.TagName)
	if !ok {
		return errors.New("latest release uses an unsupported version")
	}
	for index := range want {
		if want[index] > current[index] {
			return nil
		}
		if want[index] < current[index] {
			return errors.New("production release would replace a newer latest release")
		}
	}
	return errors.New("production release conflicts with the current latest version")
}

func validateStableHistory(publication publicationRecord, releases []githubRelease) (bool, error) {
	want, ok := parseStableVersion(publication.Tag)
	if !ok {
		return false, errors.New("production tag is not stable SemVer")
	}
	hasStable := false
	for _, release := range releases {
		if release.Draft || release.Prerelease || release.TagName == publication.Tag {
			continue
		}
		current, valid := parseStableVersion(release.TagName)
		if !valid {
			return false, errors.New("published stable release uses an unsupported version")
		}
		hasStable = true
		newer := false
		for index := range want {
			if want[index] > current[index] {
				newer = true
				break
			}
			if want[index] < current[index] {
				return false, errors.New("production release would replace a newer stable release")
			}
		}
		if !newer {
			return false, errors.New("production release conflicts with an existing stable version")
		}
	}
	return hasStable, nil
}

func planStageAcceptanceRemote(dist string, releases []githubRelease) (stageAcceptanceRecord, []githubAsset, error) {
	publication, err := readPublication(dist, "prod")
	if err != nil || publication.StageAcceptance == nil {
		return stageAcceptanceRecord{}, nil, errors.New("production publication lacks stage acceptance")
	}
	contents, err := readBoundedRegularFile(filepath.Join(dist, publication.StageAcceptance.Path), maxPublicationBytes)
	if err != nil {
		return stageAcceptanceRecord{}, nil, errors.New("read retained stage acceptance")
	}
	acceptance, err := decodeStageAcceptance(contents)
	if err != nil {
		return stageAcceptanceRecord{}, nil, err
	}
	var remote *githubRelease
	for index := range releases {
		if releases[index].TagName == acceptance.StageTag {
			if remote != nil {
				return stageAcceptanceRecord{}, nil, errors.New("GitHub contains duplicate stage release identities")
			}
			remote = &releases[index]
		}
	}
	if remote == nil || remote.ID != acceptance.ReleaseID || remote.Draft || !remote.Prerelease || len(remote.Assets) != len(acceptance.Assets) {
		return stageAcceptanceRecord{}, nil, errors.New("accepted stage release is absent or incomplete")
	}
	want := make(map[string]publicationAsset, len(acceptance.Assets))
	for _, asset := range acceptance.Assets {
		want[asset.Name] = asset
	}
	for _, asset := range remote.Assets {
		expected, ok := want[asset.Name]
		if !ok || asset.ID != expected.RemoteAssetID || asset.Size != expected.Size || asset.State != "uploaded" {
			return stageAcceptanceRecord{}, nil, errors.New("accepted stage release asset identity changed")
		}
		delete(want, asset.Name)
	}
	if len(want) != 0 {
		return stageAcceptanceRecord{}, nil, errors.New("accepted stage release asset set changed")
	}
	return acceptance, remote.Assets, nil
}

func verifyStageAcceptanceDownloads(dist, remotePath, downloads string) error {
	releases, err := readGithubReleasePages(remotePath)
	if err != nil {
		return err
	}
	acceptance, assets, err := planStageAcceptanceRemote(dist, releases)
	if err != nil {
		return err
	}
	want := make(map[string]publicationAsset, len(acceptance.Assets))
	for _, asset := range acceptance.Assets {
		want[asset.Name] = asset
	}
	for _, remote := range assets {
		contents, readErr := readBoundedRegularFile(filepath.Join(downloads, remote.Name), defaultArtifactLimits.maxCompressedBytes)
		expected := want[remote.Name]
		if readErr != nil || int64(len(contents)) != expected.Size || sha256Hex(contents) != expected.SHA256 {
			return errors.New("downloaded accepted stage asset does not match recorded bytes")
		}
	}
	return nil
}

func parseStableVersion(tag string) ([3]uint64, bool) {
	var version [3]uint64
	if !strings.HasPrefix(tag, "v") || !prodVersionPattern.MatchString(strings.TrimPrefix(tag, "v")) {
		return version, false
	}
	parts := strings.Split(strings.TrimPrefix(tag, "v"), ".")
	for index, part := range parts {
		var value uint64
		for _, character := range part {
			if value > (^uint64(0)-uint64(character-'0'))/10 {
				return version, false
			}
			value = value*10 + uint64(character-'0')
		}
		version[index] = value
	}
	return version, true
}

func mustReadPublicationBody(dist string, publication publicationRecord) string {
	contents, err := readBoundedRegularFile(filepath.Join(dist, publication.ReleaseBody.Path), maxReleaseNotesBytes+1024)
	if err != nil {
		return ""
	}
	return string(contents)
}

func validPublicationBasename(name string) bool {
	return name != "" && len(name) <= 255 && filepath.Base(name) == name &&
		!strings.ContainsAny(name, "\\/\x00\r\n")
}

func stageTagSuffix(tag string) string {
	index := strings.LastIndex(tag, "-stage.")
	if index < 0 {
		return ""
	}
	return tag[index:]
}

func validDisplayFact(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validNamedDisplayFact(value string) bool {
	return strings.TrimSpace(value) != "" && validDisplayFact(value)
}

func publicationAssets(dist string, receipt buildReceipt) ([]publicationAsset, error) {
	assets := make([]publicationAsset, 0, len(receipt.Artifacts))
	for _, artifact := range receipt.Artifacts {
		path := filepath.Join(dist, "artifacts", artifact.Name)
		contents, err := readBoundedRegularFile(path, defaultArtifactLimits.maxCompressedBytes)
		if err != nil || sha256Hex(contents) != artifact.SHA256 {
			return nil, errors.New("read exact publication asset")
		}
		assets = append(assets, publicationAsset{Name: artifact.Name, SHA256: artifact.SHA256, Size: int64(len(contents))})
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })
	return assets, nil
}

func samePublicationAssets(want, got []publicationAsset) bool {
	if len(want) != len(got) {
		return false
	}
	for index := range want {
		if want[index].Name != got[index].Name || want[index].SHA256 != got[index].SHA256 ||
			want[index].Size != got[index].Size || got[index].RemoteAssetID < 0 {
			return false
		}
	}
	return true
}

func publicationNativeInputs(dist string, requirements nativeRequirements, evidence nativeEvidence) ([]publicationFile, error) {
	paths := []string{filepath.Join("native", "requirements.json"), filepath.Join("native", "evidence.json")}
	paths = append(paths, selectedEvidencePaths("", evidence)...)
	if requirements.Mode == "routine" {
		paths = append(paths, filepath.Join("native", "review.json"))
		baselineRoot := filepath.Join("native", "baseline")
		baselineDist := filepath.Join(dist, baselineRoot)
		baselineReceipt, _, err := readEvidenceReceipt(baselineDist)
		if err != nil {
			return nil, errors.New("read publication native baseline")
		}
		paths = append(paths, filepath.Join(baselineRoot, "receipt.json"))
		for _, artifact := range baselineReceipt.Artifacts {
			paths = append(paths, filepath.Join(baselineRoot, "artifacts", artifact.Name))
		}
		paths = append(paths, filepath.Join(baselineRoot, "native", "requirements.json"), filepath.Join(baselineRoot, "native", "evidence.json"))
		baselineEvidence, err := readNativeEvidence(baselineDist)
		if err != nil {
			return nil, errors.New("read publication native baseline evidence")
		}
		paths = append(paths, selectedEvidencePaths(baselineRoot, baselineEvidence)...)
	}
	sort.Strings(paths)
	inputs := make([]publicationFile, 0, len(paths))
	seen := make(map[string]bool)
	for _, path := range paths {
		path = filepath.Clean(path)
		if path == "." || filepath.IsAbs(path) || strings.HasPrefix(path, ".."+string(filepath.Separator)) || seen[path] {
			return nil, errors.New("publication native input path is invalid")
		}
		seen[path] = true
		contents, err := readBoundedRegularFile(filepath.Join(dist, path), publicationInputLimit(path))
		if err != nil {
			return nil, errors.New("read publication native input")
		}
		inputs = append(inputs, publicationFile{Path: filepath.ToSlash(path), SHA256: sha256Hex(contents), Size: int64(len(contents))})
	}
	return inputs, nil
}

func selectedEvidencePaths(prefix string, evidence nativeEvidence) []string {
	paths := make([]string, 0)
	for _, row := range evidence.Targets {
		if row.Report != nil {
			for _, name := range []string{"record.env", "version.stdout", "version.stderr", "help.stdout", "help.stderr"} {
				paths = append(paths, filepath.Join(prefix, filepath.FromSlash(row.Report.Path), name))
			}
		}
		for _, manual := range row.ManualChecks {
			paths = append(paths, filepath.Join(prefix, filepath.FromSlash(manual.Path)))
		}
	}
	return paths
}

func publicationInputLimit(path string) int64 {
	switch filepath.Base(path) {
	case "version.stdout", "version.stderr", "help.stdout", "help.stderr":
		return maxSmokeOutputBytes
	case "record.env":
		return maxSmokeRecordBytes
	case "requirements.json", "evidence.json", "review.json":
		return maxNativeRecordBytes
	case "receipt.json":
		return maxReceiptBytes
	default:
		if strings.HasSuffix(path, ".json") {
			return maxNativeRecordBytes
		}
		return defaultArtifactLimits.maxCompressedBytes
	}
}

func verifyPublicationFile(dist string, file publicationFile, maximum int64) error {
	clean := filepath.Clean(filepath.FromSlash(file.Path))
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) ||
		file.Path != filepath.ToSlash(clean) || !validDigest(file.SHA256) || file.Size < 0 || file.Size > maximum {
		return errors.New("publication file record is invalid")
	}
	contents, err := readBoundedRegularFile(filepath.Join(dist, clean), maximum)
	if err != nil || int64(len(contents)) != file.Size || sha256Hex(contents) != file.SHA256 {
		return errors.New("publication file changed")
	}
	return nil
}
