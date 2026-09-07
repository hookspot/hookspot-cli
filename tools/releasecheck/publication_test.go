package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteShellPublicationFixture(t *testing.T) {
	output := os.Getenv("HOOKSPOT_SHELL_FIXTURE_OUTPUT")
	if output == "" {
		t.Skip("shell fixture output was not requested")
	}
	publisherPlatform := os.Getenv("HOOKSPOT_SHELL_PUBLISHER_PLATFORM")
	if publisherPlatform != "linux/amd64" && publisherPlatform != "linux/arm64" {
		t.Fatal("shell fixture publisher platform is invalid")
	}
	dist, _ := completePublicationFixture(t)
	notes := filepath.Join(t.TempDir(), "notes.md")
	writeTestFile(t, notes, "Fixture notes.\n")
	if err := preparePublication(publicationOptions{dist: dist, environment: "stage", notesPath: notes,
		publisherImageID: "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56", publisherPlatform: publisherPlatform}); err != nil {
		t.Fatal(err)
	}
	if err := copyFixtureDirectory(dist, output); err != nil {
		t.Fatal(err)
	}
	baselineOutput := os.Getenv("HOOKSPOT_SHELL_BASELINE_FIXTURE_OUTPUT")
	reviewOutput := os.Getenv("HOOKSPOT_SHELL_REVIEW_FIXTURE_OUTPUT")
	if (baselineOutput == "") != (reviewOutput == "") {
		t.Fatal("shell routine fixture outputs must be paired")
	}
	if baselineOutput != "" {
		baselineDist, _ := completeFirstSupportFixture(t)
		if err := createNativeEvidence(baselineDist); err != nil {
			t.Fatal(err)
		}
		current, _, err := readEvidenceReceipt(dist)
		if err != nil {
			t.Fatal(err)
		}
		reviewPath := writeRoutineReviewFixture(t, dist, current, baselineDist)
		if err := copyFixtureDirectory(baselineDist, baselineOutput); err != nil {
			t.Fatal(err)
		}
		reviewBytes, err := os.ReadFile(reviewPath)
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, reviewOutput, string(reviewBytes))
	}
	prodOutput := os.Getenv("HOOKSPOT_SHELL_PROD_FIXTURE_OUTPUT")
	publishedOutput := os.Getenv("HOOKSPOT_SHELL_PUBLISHED_FIXTURE_OUTPUT")
	if prodOutput == "" && publishedOutput == "" {
		return
	}
	stage, err := readPublication(dist, "stage")
	if err != nil {
		t.Fatal(err)
	}
	stage.Phase = "published"
	stage.ReleaseID = 42
	for index := range stage.Assets {
		stage.Assets[index].RemoteAssetID = int64(index + 100)
	}
	if err := writeJSONAtomic(filepath.Join(dist, "publication.json"), stage); err != nil {
		t.Fatal(err)
	}
	if publishedOutput != "" {
		if err := copyFixtureDirectory(dist, publishedOutput); err != nil {
			t.Fatal(err)
		}
	}
	if prodOutput == "" {
		return
	}
	acceptancePath := filepath.Join(t.TempDir(), "stage-acceptance.json")
	if err := createStageAcceptanceTemplate(dist, acceptancePath); err != nil {
		t.Fatal(err)
	}
	acceptanceBytes, err := os.ReadFile(acceptancePath)
	if err != nil {
		t.Fatal(err)
	}
	acceptance, err := decodeStageAcceptance(acceptanceBytes)
	if err != nil {
		t.Fatal(err)
	}
	acceptance.Accepted = true
	acceptance.Reviewer = "fixture operator"
	acceptance.AcceptedAt = "2026-09-07T12:00:00Z"
	acceptance.Checks = []string{"authenticated staging smoke"}
	writeJSONFile(t, acceptancePath, acceptance)

	prodDist, _ := completePublicationFixtureFor(t, "prod", "1.2.3")
	prodNotes := filepath.Join(t.TempDir(), "prod-notes.md")
	writeTestFile(t, prodNotes, "Production fixture notes.\n")
	if err := preparePublication(publicationOptions{dist: prodDist, environment: "prod", notesPath: prodNotes,
		publisherImageID: "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56", publisherPlatform: publisherPlatform,
		stageAcceptancePath: acceptancePath, fromStageTag: stage.Tag}); err != nil {
		t.Fatal(err)
	}
	if err := copyFixtureDirectory(prodDist, prodOutput); err != nil {
		t.Fatal(err)
	}
}

func copyFixtureDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("shell fixture contains an unsafe file")
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, contents, info.Mode().Perm())
	})
}

type atomicPublicationObservation struct {
	path    string
	visible *bool
}

func (observation atomicPublicationObservation) MarshalJSON() ([]byte, error) {
	_, err := os.Lstat(observation.path)
	*observation.visible = err == nil
	return []byte(`{"schema_version":1}`), nil
}

type interruptedPublicationEncoding struct{}

func (interruptedPublicationEncoding) MarshalJSON() ([]byte, error) {
	os.Exit(99)
	return nil, nil
}

func TestInitialPublicationWriterIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "publication.json")
	visible := false
	if err := writePublicationJSONExclusive(path, atomicPublicationObservation{path: path, visible: &visible}); err != nil {
		t.Fatal(err)
	}
	if visible {
		t.Fatal("final publication state was visible before encoding completed")
	}
	if err := writePublicationJSONExclusive(path, struct{}{}); err == nil {
		t.Fatal("exclusive publication writer replaced existing state")
	}
}

func TestInterruptedInitialPublicationLeavesNoFinalState(t *testing.T) {
	const childPath = "HOOKSPOT_TEST_INITIAL_PUBLICATION_PATH"
	if path := os.Getenv(childPath); path != "" {
		if err := writePublicationJSONExclusive(path, interruptedPublicationEncoding{}); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "publication.json")
	command := exec.Command(os.Args[0], "-test.run=^TestInterruptedInitialPublicationLeavesNoFinalState$")
	command.Env = append(os.Environ(), childPath+"="+path)
	err := command.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 99 {
		t.Fatalf("unexpected child result: %v", err)
	}
	if contents, err := os.ReadFile(path); !os.IsNotExist(err) {
		t.Fatalf("interrupted writer left final state (%d bytes; read error %v)", len(contents), err)
	}
}

func TestPublicationMarkerBindsEligibilityInputs(t *testing.T) {
	inputs := []publicationFile{{Path: "native/requirements.json", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	first := publicationInputsSHA(eligibilityInputRecords(inputs))
	marker := publicationMarker("receipt", "notes", "tag", first)
	inputs[0].SHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	second := publicationInputsSHA(eligibilityInputRecords(inputs))
	if first == second || marker == publicationMarker("receipt", "notes", "tag", second) {
		t.Fatal("publication marker did not bind eligibility inputs")
	}
}

func TestPreparePublicationRejectsLocalDiagnosticNetworkProcedure(t *testing.T) {
	dist, _ := completePublicationFixture(t)
	evidence, err := readNativeEvidence(dist)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for index := range evidence.Targets {
		for manualIndex := range evidence.Targets[index].ManualChecks {
			if evidence.Targets[index].ManualChecks[manualIndex].Check == "network" {
				evidence.Targets[index].ManualChecks[manualIndex].Procedure = "local-tls-phoenix-v1"
				found = true
			}
		}
	}
	if !found {
		t.Fatal("publication fixture has no network evidence")
	}
	writeTestJSON(t, filepath.Join(dist, "native", "evidence.json"), evidence)
	notes := filepath.Join(t.TempDir(), "notes.md")
	writeTestFile(t, notes, "Fixture notes.\n")
	err = preparePublication(publicationOptions{dist: dist, environment: "stage", notesPath: notes,
		publisherImageID: "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56", publisherPlatform: "linux/arm64"})
	if err == nil || !strings.Contains(err.Error(), "network_release_v1") {
		t.Fatalf("local diagnostic network procedure error = %v, want canonical procedure", err)
	}
}

func TestPreparePublicationRecoversOnlyIdenticalLostState(t *testing.T) {
	dist, _ := completePublicationFixture(t)
	notes := filepath.Join(t.TempDir(), "notes.md")
	writeTestFile(t, notes, "Fixture notes.\n")
	options := publicationOptions{dist: dist, environment: "stage", notesPath: notes,
		publisherImageID: "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56", publisherPlatform: "linux/arm64"}
	if err := preparePublication(options); err != nil {
		t.Fatal(err)
	}
	before, err := readPublication(dist, "stage")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dist, "publication.json")); err != nil {
		t.Fatal(err)
	}
	if err := preparePublication(options); err != nil {
		t.Fatalf("identical lost state was not recoverable: %v", err)
	}
	after, err := readPublication(dist, "stage")
	if err != nil {
		t.Fatal(err)
	}
	if before.Marker != after.Marker || before.EligibilityInputs != after.EligibilityInputs {
		t.Fatal("identical recovery changed publication ownership")
	}
	if err := os.Remove(filepath.Join(dist, "publication.json")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, notes, "Different reviewed notes.\n")
	if err := preparePublication(options); err == nil {
		t.Fatal("lost-state recovery replaced retained publication inputs")
	}
}

func TestRecoverPublicationRequiresExactOwnedRemoteRelease(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(map[bool]string{false: "draft", true: "published"}[published], func(t *testing.T) {
			dist, _ := completePublicationFixture(t)
			notes := filepath.Join(t.TempDir(), "notes.md")
			writeTestFile(t, notes, "Fixture notes.\n")
			options := publicationOptions{dist: dist, environment: "stage", notesPath: notes,
				publisherImageID: "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56", publisherPlatform: "linux/arm64"}
			if err := preparePublication(options); err != nil {
				t.Fatal(err)
			}
			original, err := readPublication(dist, "stage")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(dist, "publication.json")); err != nil {
				t.Fatal(err)
			}
			options.notesPath = filepath.Join(dist, original.Notes.Path)
			downloads := t.TempDir()
			assetCount := 3
			if published {
				assetCount = len(original.Assets)
			}
			remoteAssets := make([]githubAsset, 0, assetCount)
			for index, asset := range original.Assets[:assetCount] {
				contents, readErr := os.ReadFile(filepath.Join(dist, "artifacts", asset.Name))
				if readErr != nil {
					t.Fatal(readErr)
				}
				if err := os.WriteFile(filepath.Join(downloads, asset.Name), contents, 0o600); err != nil {
					t.Fatal(err)
				}
				remoteAssets = append(remoteAssets, githubAsset{ID: int64(index + 100), Name: asset.Name, Size: asset.Size, State: "uploaded"})
			}
			remote := githubRelease{ID: 42, TagName: original.Tag, Draft: !published, Prerelease: true,
				Body: mustReadPublicationBody(dist, original), Assets: remoteAssets}
			remotePath := filepath.Join(t.TempDir(), "releases.json")
			writeJSONFile(t, remotePath, [][]githubRelease{{remote}})
			if err := recoverPublication(options, remotePath, downloads); err != nil {
				t.Fatal(err)
			}
			recovered, err := readPublication(dist, "stage")
			if err != nil {
				t.Fatal(err)
			}
			wantPhase := "draft"
			if published {
				wantPhase = "published"
			}
			if recovered.Marker != original.Marker || recovered.Phase != wantPhase || recovered.ReleaseID != 42 {
				t.Fatalf("recovered wrong publication identity: %+v", recovered)
			}

			if err := os.Remove(filepath.Join(dist, "publication.json")); err != nil {
				t.Fatal(err)
			}
			remote.Body = "foreign body"
			writeJSONFile(t, remotePath, [][]githubRelease{{remote}})
			if err := recoverPublication(options, remotePath, downloads); err == nil {
				t.Fatal("recovered a foreign remote release")
			}
			if _, err := os.Stat(filepath.Join(dist, "publication.json")); !os.IsNotExist(err) {
				t.Fatal("failed recovery created publication state")
			}
		})
	}
}

func TestReconcilePublicationVerifiesDownloadedBytesAndPersistsRemoteIDs(t *testing.T) {
	dist, _ := completePublicationFixture(t)
	notes := filepath.Join(t.TempDir(), "notes.md")
	writeTestFile(t, notes, "Fixture notes.\n")
	if err := preparePublication(publicationOptions{dist: dist, environment: "stage", notesPath: notes,
		publisherImageID: "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56", publisherPlatform: "linux/arm64"}); err != nil {
		t.Fatal(err)
	}
	publication, err := readPublication(dist, "stage")
	if err != nil {
		t.Fatal(err)
	}
	downloads := t.TempDir()
	remoteAssets := make([]githubAsset, 0, len(publication.Assets))
	for index, asset := range publication.Assets {
		contents, readErr := os.ReadFile(filepath.Join(dist, "artifacts", asset.Name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		writeTestFile(t, filepath.Join(downloads, asset.Name), string(contents))
		remoteAssets = append(remoteAssets, githubAsset{ID: int64(index + 100), Name: asset.Name, Size: asset.Size, State: "uploaded"})
	}
	body, err := os.ReadFile(filepath.Join(dist, publication.ReleaseBody.Path))
	if err != nil {
		t.Fatal(err)
	}
	remotePath := filepath.Join(t.TempDir(), "releases.json")
	writeJSONFile(t, remotePath, [][]githubRelease{{{ID: 42, TagName: publication.Tag, Draft: true, Prerelease: true, Body: string(body), Assets: remoteAssets}}})
	status, missing, err := reconcilePublication(dist, "stage", remotePath, downloads, true)
	if err != nil {
		t.Fatal(err)
	}
	if status != "draft" || len(missing) != 0 {
		t.Fatalf("unexpected reconciliation: %s %v", status, missing)
	}
	updated, err := readPublication(dist, "stage")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ReleaseID != 42 || updated.Assets[0].RemoteAssetID != 100 {
		t.Fatalf("remote identities were not retained: %+v", updated)
	}

	writeTestFile(t, filepath.Join(downloads, publication.Assets[0].Name), "different")
	if _, _, err := reconcilePublication(dist, "stage", remotePath, downloads, true); err == nil {
		t.Fatal("mismatched downloaded bytes were accepted")
	}
}

func TestReconcilePublicationPreservesRecordedRemoteAssetIDs(t *testing.T) {
	for _, phase := range []string{"draft", "published"} {
		t.Run(phase, func(t *testing.T) {
			dist, _ := completePublicationFixture(t)
			notes := filepath.Join(t.TempDir(), "notes.md")
			writeTestFile(t, notes, "Fixture notes.\n")
			if err := preparePublication(publicationOptions{dist: dist, environment: "stage", notesPath: notes,
				publisherImageID: "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56", publisherPlatform: "linux/arm64"}); err != nil {
				t.Fatal(err)
			}
			publication, err := readPublication(dist, "stage")
			if err != nil {
				t.Fatal(err)
			}
			downloads := t.TempDir()
			remoteAssets := make([]githubAsset, 0, len(publication.Assets))
			for index, asset := range publication.Assets {
				contents, readErr := os.ReadFile(filepath.Join(dist, "artifacts", asset.Name))
				if readErr != nil {
					t.Fatal(readErr)
				}
				if err := os.WriteFile(filepath.Join(downloads, asset.Name), contents, 0o600); err != nil {
					t.Fatal(err)
				}
				remoteAssets = append(remoteAssets, githubAsset{ID: int64(index + 100), Name: asset.Name, Size: asset.Size, State: "uploaded"})
			}
			body := mustReadPublicationBody(dist, publication)
			remotePath := filepath.Join(t.TempDir(), "releases.json")
			remote := githubRelease{ID: 42, TagName: publication.Tag, Draft: phase == "draft", Prerelease: true, Body: body, Assets: remoteAssets}
			writeJSONFile(t, remotePath, [][]githubRelease{{remote}})
			if _, _, err := reconcilePublication(dist, "stage", remotePath, downloads, phase == "draft"); err != nil {
				t.Fatal(err)
			}

			remote.Assets[0].ID = 900
			writeJSONFile(t, remotePath, [][]githubRelease{{remote}})
			if _, _, err := reconcilePublication(dist, "stage", remotePath, downloads, phase == "draft"); err == nil {
				t.Fatal("accepted replacement remote asset identity")
			}
			retained, err := readPublication(dist, "stage")
			if err != nil || retained.Assets[0].RemoteAssetID != 100 {
				t.Fatalf("changed retained asset identity after conflict: %d, %v", retained.Assets[0].RemoteAssetID, err)
			}

			remote.Assets = remoteAssets[1:]
			writeJSONFile(t, remotePath, [][]githubRelease{{remote}})
			if _, _, err := reconcilePublication(dist, "stage", remotePath, downloads, phase == "draft"); err == nil {
				t.Fatal("accepted disappearance of a recorded remote asset")
			}
		})
	}
}

func TestReconcilePublicationAllowsOnlyExplicitPartialDraft(t *testing.T) {
	dist, _ := completePublicationFixture(t)
	notes := filepath.Join(t.TempDir(), "notes.md")
	writeTestFile(t, notes, "Fixture notes.\n")
	if err := preparePublication(publicationOptions{dist: dist, environment: "stage", notesPath: notes,
		publisherImageID: "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56", publisherPlatform: "linux/arm64"}); err != nil {
		t.Fatal(err)
	}
	publication, _ := readPublication(dist, "stage")
	body, _ := os.ReadFile(filepath.Join(dist, publication.ReleaseBody.Path))
	remotePath := filepath.Join(t.TempDir(), "releases.json")
	writeJSONFile(t, remotePath, [][]githubRelease{{{ID: 42, TagName: publication.Tag, Draft: true, Prerelease: true, Body: string(body), Assets: []githubAsset{}}}})
	if _, _, err := reconcilePublication(dist, "stage", remotePath, t.TempDir(), false); err == nil {
		t.Fatal("ordinary reconciliation accepted a partial draft")
	}
	status, missing, err := reconcilePublication(dist, "stage", remotePath, t.TempDir(), true)
	if err != nil || status != "draft" || len(missing) != 7 {
		t.Fatalf("explicit resume did not describe missing assets: %s %v %v", status, missing, err)
	}
}

func TestStageAcceptanceTemplateAndProductionBinding(t *testing.T) {
	stageDist, _ := completePublicationFixture(t)
	stageNotes := filepath.Join(t.TempDir(), "stage-notes.md")
	writeTestFile(t, stageNotes, "Stage notes.\n")
	if err := preparePublication(publicationOptions{dist: stageDist, environment: "stage", notesPath: stageNotes,
		publisherImageID: "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56", publisherPlatform: "linux/arm64"}); err != nil {
		t.Fatal(err)
	}
	stage, err := readPublication(stageDist, "stage")
	if err != nil {
		t.Fatal(err)
	}
	stage.Phase = "published"
	stage.ReleaseID = 42
	for index := range stage.Assets {
		stage.Assets[index].RemoteAssetID = int64(index + 100)
	}
	if err := writeJSONAtomic(filepath.Join(stageDist, "publication.json"), stage); err != nil {
		t.Fatal(err)
	}
	if err := createStageAcceptanceTemplate(stageDist, filepath.Join(stageDist, "acceptance.json")); err == nil {
		t.Fatal("acceptance output inside retained stage tree was accepted")
	}
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	if err := createStageAcceptanceTemplate(root, filepath.Join(t.TempDir(), "acceptance.json")); err == nil || !strings.Contains(err.Error(), "filesystem root") {
		t.Fatalf("acceptance root error = %v", err)
	}
	ancestorOutput := filepath.Join(filepath.Dir(stageDist), "acceptance.json")
	if _, err := os.Lstat(ancestorOutput); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("acceptance ancestor fixture is not empty: %v", err)
	}
	if err := createStageAcceptanceTemplate(stageDist, ancestorOutput); err == nil {
		t.Fatal("acceptance output parent containing retained stage tree was accepted")
	}
	if _, err := os.Lstat(ancestorOutput); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected acceptance output was created: %v", err)
	}
	rawOutput, resolvedOutput := parentTraversalTestPath(t, "acceptance.json")
	if err := createStageAcceptanceTemplate(stageDist, rawOutput); err == nil || !strings.Contains(err.Error(), "parent traversal") {
		t.Fatalf("acceptance parent-traversal error = %v", err)
	}
	if _, err := os.Lstat(resolvedOutput); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected parent-traversal acceptance output was created: %v", err)
	}
	realParent := t.TempDir()
	linkedParent := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatal(err)
	}
	if err := createStageAcceptanceTemplate(stageDist, filepath.Join(linkedParent, "acceptance.json")); err == nil {
		t.Fatal("symlinked acceptance output parent was accepted")
	}
	templatePath := filepath.Join(t.TempDir(), "acceptance.json")
	if err := createStageAcceptanceTemplate(stageDist, templatePath); err != nil {
		t.Fatal(err)
	}
	templateBytes, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	template, err := decodeStageAcceptance(templateBytes)
	if err != nil {
		t.Fatal(err)
	}
	if template.Accepted || template.BaseVersion != "1.2.3" || template.StageTag != "v1.2.3-stage.1" || len(template.Assets) != 7 {
		t.Fatalf("acceptance template is inaccurate: %+v", template)
	}
	template.Accepted = true
	template.Reviewer = "release operator"
	template.AcceptedAt = "2026-09-06T12:00:00Z"
	template.Checks = []string{"authenticated staging smoke", "downloaded archive smoke"}
	writeJSONFile(t, templatePath, template)

	prodDist, _ := completePublicationFixtureFor(t, "prod", "1.2.3")
	prodNotes := filepath.Join(t.TempDir(), "prod-notes.md")
	writeTestFile(t, prodNotes, "Production notes.\n")
	options := publicationOptions{dist: prodDist, environment: "prod", notesPath: prodNotes,
		publisherImageID: "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56", publisherPlatform: "linux/arm64",
		stageAcceptancePath: templatePath, fromStageTag: "v1.2.3-stage.1"}
	if err := preparePublication(options); err != nil {
		t.Fatal(err)
	}
	publication, err := readPublication(prodDist, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if publication.StageAcceptance == nil || publication.EligibilityInputs == "" {
		t.Fatal("production publication did not bind accepted stage evidence")
	}
	remoteAssets := make([]githubAsset, 0, len(template.Assets))
	downloads := t.TempDir()
	for _, asset := range template.Assets {
		remoteAssets = append(remoteAssets, githubAsset{ID: asset.RemoteAssetID, Name: asset.Name, Size: asset.Size, State: "uploaded"})
		contents, readErr := os.ReadFile(filepath.Join(stageDist, "artifacts", asset.Name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err := os.WriteFile(filepath.Join(downloads, asset.Name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	remotePath := filepath.Join(t.TempDir(), "releases.json")
	writeJSONFile(t, remotePath, [][]githubRelease{{{ID: template.ReleaseID, TagName: template.StageTag,
		Draft: false, Prerelease: true, Assets: remoteAssets}}})
	if err := verifyStageAcceptanceDownloads(prodDist, remotePath, downloads); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(downloads, template.Assets[0].Name), "different")
	if err := verifyStageAcceptanceDownloads(prodDist, remotePath, downloads); err == nil {
		t.Fatal("changed remote stage asset bytes were accepted")
	}
}

func TestLatestPolicyFailsClosed(t *testing.T) {
	publication := publicationRecord{Tag: "v1.2.3"}
	if required, err := validateStableHistory(publication, nil); err != nil || required {
		t.Fatalf("first stable release was rejected: %t %v", required, err)
	}
	if required, err := validateStableHistory(publication, []githubRelease{{ID: 2, TagName: "v1.2.2"}}); err != nil || !required {
		t.Fatalf("valid stable history was rejected: %t %v", required, err)
	}
	if _, err := validateStableHistory(publication, []githubRelease{{ID: 2, TagName: "latest-old"}}); err == nil {
		t.Fatal("unknown stable history was accepted")
	}
	path := filepath.Join(t.TempDir(), "latest.json")
	writeJSONFile(t, path, githubRelease{ID: 3, TagName: "stable-current", Draft: false})
	if err := validateLatestRelease(publication, path); err == nil {
		t.Fatal("unknown latest version was accepted")
	}
	writeJSONFile(t, path, githubRelease{ID: 3, TagName: "v1.2.2", Draft: false})
	if err := validateLatestRelease(publication, path); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, path, githubRelease{ID: 3, TagName: "v1.2.4", Draft: false})
	if err := validateLatestRelease(publication, path); err == nil {
		t.Fatal("newer latest version was accepted")
	}
}

func TestGithubReleasePagesRequireActualPages(t *testing.T) {
	for _, input := range []string{"null", "[null]", "[]"} {
		t.Run(input, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "releases.json")
			writeTestFile(t, path, input)
			if _, err := readGithubReleasePages(path); err == nil {
				t.Fatal("accepted a missing GitHub release page")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "releases.json")
	writeTestFile(t, path, "[[]]")
	if releases, err := readGithubReleasePages(path); err != nil || len(releases) != 0 {
		t.Fatalf("rejected an authoritative empty page: %v", err)
	}
}

func TestStageAcceptanceRequiresNamedFactsAndCanonicalTag(t *testing.T) {
	acceptance, receipt := acceptedStageFixture(t)
	for _, test := range []struct {
		name   string
		change func(*stageAcceptanceRecord)
	}{
		{name: "blank reviewer", change: func(value *stageAcceptanceRecord) { value.Reviewer = " \u2003" }},
		{name: "blank check", change: func(value *stageAcceptanceRecord) { value.Checks = []string{" \u2003"} }},
		{name: "tag without v", change: func(value *stageAcceptanceRecord) { value.StageTag = strings.TrimPrefix(value.StageTag, "v") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := acceptance
			test.change(&changed)
			if err := validateStageAcceptanceForReceipt(changed, receipt); err == nil {
				t.Fatal("accepted incomplete stage acceptance identity")
			}
		})
	}
}

func acceptedStageFixture(t *testing.T) (stageAcceptanceRecord, buildReceipt) {
	t.Helper()
	stageDist, _ := completePublicationFixture(t)
	stageNotes := filepath.Join(t.TempDir(), "stage-notes.md")
	writeTestFile(t, stageNotes, "Stage notes.\n")
	if err := preparePublication(publicationOptions{dist: stageDist, environment: "stage", notesPath: stageNotes,
		publisherImageID: "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56", publisherPlatform: "linux/arm64"}); err != nil {
		t.Fatal(err)
	}
	publication, err := readPublication(stageDist, "stage")
	if err != nil {
		t.Fatal(err)
	}
	publication.Phase = "published"
	publication.ReleaseID = 42
	for index := range publication.Assets {
		publication.Assets[index].RemoteAssetID = int64(index + 100)
	}
	if err := writeJSONAtomic(filepath.Join(stageDist, "publication.json"), publication); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "acceptance.json")
	if err := createStageAcceptanceTemplate(stageDist, path); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	acceptance, err := decodeStageAcceptance(contents)
	if err != nil {
		t.Fatal(err)
	}
	acceptance.Accepted = true
	acceptance.Reviewer = "release operator"
	acceptance.AcceptedAt = "2026-09-07T12:00:00Z"
	acceptance.Checks = []string{"authenticated staging smoke"}
	_, receipt := completePublicationFixtureFor(t, "prod", "1.2.3")
	return acceptance, receipt
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPreparePublicationFreezesExactBundleAndSelectedEvidence(t *testing.T) {
	dist, receipt := completePublicationFixture(t)
	notes := filepath.Join(t.TempDir(), "notes.md")
	writeTestFile(t, notes, "Release notes for the fixture.\n")
	receiptBefore, err := os.ReadFile(filepath.Join(dist, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}

	if err := preparePublication(publicationOptions{
		dist: dist, environment: "stage", notesPath: notes,
		publisherImageID:  "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56",
		publisherPlatform: "linux/arm64",
	}); err != nil {
		t.Fatal(err)
	}
	publication, err := readPublication(dist, "stage")
	if err != nil {
		t.Fatal(err)
	}
	if publication.Phase != "eligible" || publication.Repository != receipt.SourceRepository ||
		publication.Tag != receipt.Tag || publication.TagObject != receipt.TagObject ||
		publication.TagCommit != receipt.Commit || publication.Marker == "" {
		t.Fatalf("publication identity is incomplete: %+v", publication)
	}
	if len(publication.Assets) != 7 || len(publication.NativeInputs) < 2 {
		t.Fatalf("publication did not freeze exact inputs: %+v", publication)
	}
	receiptAfter, err := os.ReadFile(filepath.Join(dist, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(receiptBefore, receiptAfter) {
		t.Fatal("publication preparation changed receipt.json")
	}

	evidence, err := readNativeEvidence(dist)
	if err != nil {
		t.Fatal(err)
	}
	selected := evidence.Targets[0].Report
	if selected == nil {
		t.Fatal("fixture lacks selected smoke evidence")
	}
	selectedOutput := filepath.Join(dist, filepath.FromSlash(selected.Path), "version.stdout")
	contents, err := os.ReadFile(selectedOutput)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, selectedOutput, string(contents)+"changed")
	if _, err := readPublication(dist, "stage"); err == nil {
		t.Fatal("changed selected native evidence was accepted")
	}
}

func TestPreparePublicationRejectsSnapshotAndDoesNotCreateState(t *testing.T) {
	dist, _, _ := completeReceiptFixture(t)
	notes := filepath.Join(t.TempDir(), "notes.md")
	writeTestFile(t, notes, "Fixture notes.\n")
	if err := preparePublication(publicationOptions{
		dist: dist, environment: "stage", notesPath: notes,
		publisherImageID:  "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56",
		publisherPlatform: "linux/arm64",
	}); err == nil {
		t.Fatal("snapshot was accepted for publication")
	}
	if _, err := os.Stat(filepath.Join(dist, "publication.json")); !os.IsNotExist(err) {
		t.Fatal("failed publication preparation left state")
	}
}

func completePublicationFixture(t *testing.T) (string, buildReceipt) {
	return completePublicationFixtureFor(t, "stage", "1.2.3-stage.1")
}

func completePublicationFixtureFor(t *testing.T, environment, version string) (string, buildReceipt) {
	t.Helper()
	dist, options, metadata := receiptFixture(t)
	metadata.Environment = environment
	if environment == "prod" {
		metadata.ServerURL = "https://prod.example.invalid/gateway"
	}
	metadata.Version = version
	metadata.BuildKind = "release"
	options.snapshot = false
	options.environment = environment
	options.tag = "v" + metadata.Version
	writeReceiptMetadata(t, dist, metadata)
	t.Setenv("RELEASE_BUILDER_IMAGE_ID", "sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56")
	t.Setenv("RELEASE_TAG_OBJECT", "abcdef0123456789abcdef0123456789abcdef01")
	if err := os.MkdirAll(filepath.Join(dist, "artifacts"), 0o700); err != nil {
		t.Fatal(err)
	}

	artifacts := make([]verifiedArtifact, 0, 7)
	for _, target := range releaseTargets {
		name := releaseArchiveName(environment, metadata.Version, target)
		format := "tar.gz"
		if target.OS == "windows" {
			format = "zip"
		}
		writeTestArchive(t, filepath.Join(dist, "artifacts", name), format, validArchiveEntries(releaseBinaryName(environment, target.OS)), nil)
		hash, err := hashBoundedFile(filepath.Join(dist, "artifacts", name), defaultArtifactLimits.maxCompressedBytes)
		if err != nil {
			t.Fatal(err)
		}
		artifacts = append(artifacts, verifiedArtifact{
			Name: name, SHA256: hash, OS: target.OS, Arch: target.Arch,
			Format: true, BuildInfo: true, FreshMatch: true,
		})
	}
	checksumName := "hookspot_" + environment + "_" + metadata.Version + "_checksums.txt"
	writeTestFile(t, filepath.Join(dist, "artifacts", checksumName), "fixture checksums\n")
	checksumHash, err := hashBoundedFile(filepath.Join(dist, "artifacts", checksumName), maxChecksumBytes)
	if err != nil {
		t.Fatal(err)
	}
	artifacts = append(artifacts, verifiedArtifact{Name: checksumName, SHA256: checksumHash})
	if err := writeArtifactReceipt(options, metadata, artifacts); err != nil {
		t.Fatal(err)
	}
	receiptBytes, err := os.ReadFile(filepath.Join(dist, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt buildReceipt
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
		t.Fatal(err)
	}
	for _, target := range releaseTargets {
		writeSmokeReportForDist(t, dist, receipt, receiptBytes, target)
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
	if err := createNativeEvidence(dist); err != nil {
		t.Fatal(err)
	}
	return dist, receipt
}
