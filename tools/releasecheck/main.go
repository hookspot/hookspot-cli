package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: releasecheck env|env-url|repository|metadata|artifacts|receipt|smoke|native-requirements|native-review-template|native-manual|native-evidence|publication")
	}
	switch args[0] {
	case "env":
		return runEnvironment(args[1:], out)
	case "env-url":
		return runEnvironmentURL(args[1:], out)
	case "repository":
		return runRepository(args[1:], out)
	case "metadata":
		return runMetadata(args[1:])
	case "artifacts":
		return runArtifacts(args[1:])
	case "receipt":
		return runReceipt(args[1:])
	case "smoke":
		return runSmoke(args[1:], out)
	case "native-requirements":
		return runNativeRequirements(args[1:])
	case "native-review-template":
		return runNativeReviewTemplate(args[1:], out)
	case "native-manual":
		return runNativeManual(args[1:], out)
	case "native-evidence":
		return runNativeEvidence(args[1:])
	case "publication":
		return runPublication(args[1:], out)
	default:
		return errors.New("usage: releasecheck env|env-url|repository|metadata|artifacts|receipt|smoke|native-requirements|native-review-template|native-manual|native-evidence|publication")
	}
}

func runPublication(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: releasecheck publication candidate|prepare|describe|remote-plan|reconcile|recover-plan|recover|stable-history|latest|acceptance-template|acceptance-plan|acceptance-verify")
	}
	switch args[0] {
	case "candidate":
		flags := flag.NewFlagSet("releasecheck publication candidate", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		dist := flags.String("dist", "", "retained output parent")
		environment := flags.String("environment", "", "release environment")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dist) {
			return errors.New("publication candidate requires absolute --dist and environment")
		}
		if err := verifyReceipt(*dist, *environment); err != nil {
			return err
		}
		receipt, _, err := readEvidenceReceipt(*dist)
		if err != nil || receipt.BuildKind != "release" {
			return errors.New("publication candidate must be a tagged release")
		}
		_, err = fmt.Fprintf(out, "repository\t%s\ntag\t%s\ntag_object\t%s\ntag_commit\t%s\n",
			receipt.SourceRepository, receipt.Tag, receipt.TagObject, receipt.Commit)
		return err
	case "prepare":
		flags := flag.NewFlagSet("releasecheck publication prepare", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		dist := flags.String("dist", "", "retained output parent")
		environment := flags.String("environment", "", "release environment")
		notes := flags.String("notes", "", "release notes")
		imageID := flags.String("publisher-image-id", "", "publisher image ID")
		platform := flags.String("publisher-platform", "", "publisher platform")
		acceptance := flags.String("stage-acceptance", "", "stage acceptance")
		fromStage := flags.String("from-stage-tag", "", "accepted stage tag")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dist) || !filepath.IsAbs(*notes) {
			return errors.New("publication prepare requires absolute --dist and --notes plus environment and publisher identity")
		}
		return preparePublication(publicationOptions{dist: *dist, environment: *environment, notesPath: *notes,
			publisherImageID: *imageID, publisherPlatform: *platform, stageAcceptancePath: *acceptance, fromStageTag: *fromStage})
	case "describe":
		flags := flag.NewFlagSet("releasecheck publication describe", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		dist := flags.String("dist", "", "retained output parent")
		environment := flags.String("environment", "", "release environment")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dist) {
			return errors.New("publication describe requires absolute --dist and environment")
		}
		publication, err := readPublication(*dist, *environment)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "repository\t%s\ntag\t%s\ntag_object\t%s\ntag_commit\t%s\nmarker\t%s\nphase\t%s\nrelease_id\t%d\n",
			publication.Repository, publication.Tag, publication.TagObject, publication.TagCommit, publication.Marker, publication.Phase, publication.ReleaseID); err != nil {
			return err
		}
		for _, asset := range publication.Assets {
			if _, err := fmt.Fprintf(out, "asset\t%s\n", asset.Name); err != nil {
				return err
			}
		}
		if publication.StageAcceptance != nil {
			contents, err := readBoundedRegularFile(filepath.Join(*dist, publication.StageAcceptance.Path), maxPublicationBytes)
			if err != nil {
				return err
			}
			acceptance, err := decodeStageAcceptance(contents)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "stage_tag\t%s\nstage_tag_object\t%s\nstage_tag_commit\t%s\n",
				acceptance.StageTag, acceptance.TagObject, acceptance.TagCommit)
			return err
		}
		return nil
	case "reconcile":
		flags := flag.NewFlagSet("releasecheck publication reconcile", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		dist := flags.String("dist", "", "retained output parent")
		environment := flags.String("environment", "", "release environment")
		remote := flags.String("remote-json", "", "complete paginated GitHub release response")
		downloads := flags.String("downloads", "", "fresh downloaded assets")
		allowMissing := flags.Bool("allow-missing", false, "allow missing draft assets")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dist) || !filepath.IsAbs(*remote) || !filepath.IsAbs(*downloads) {
			return errors.New("publication reconcile requires absolute retained, remote, and download paths")
		}
		status, missing, err := reconcilePublication(*dist, *environment, *remote, *downloads, *allowMissing)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "status\t%s\n", status); err != nil {
			return err
		}
		for _, name := range missing {
			if _, err := fmt.Fprintf(out, "missing\t%s\n", name); err != nil {
				return err
			}
		}
		return nil
	case "recover-plan", "recover":
		operation := args[0]
		flags := flag.NewFlagSet("releasecheck publication "+operation, flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		dist := flags.String("dist", "", "retained output parent")
		environment := flags.String("environment", "", "release environment")
		imageID := flags.String("publisher-image-id", "", "publisher image ID")
		platform := flags.String("publisher-platform", "", "publisher platform")
		remote := flags.String("remote-json", "", "complete paginated GitHub release response")
		downloads := flags.String("downloads", "", "fresh downloaded assets")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dist) || !filepath.IsAbs(*remote) ||
			(operation == "recover" && !filepath.IsAbs(*downloads)) {
			return errors.New("publication recovery requires absolute retained, remote, and download paths plus publisher identity")
		}
		options, err := publicationRecoveryOptions(*dist, *environment, *imageID, *platform)
		if err != nil {
			return err
		}
		if operation == "recover" {
			return recoverPublication(options, *remote, *downloads)
		}
		_, plan, err := planPublicationRecovery(options, *remote)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "status\t%s\nrelease_id\t%d\n", plan.Status, plan.Release.ID); err != nil {
			return err
		}
		for _, asset := range plan.Release.Assets {
			if _, err := fmt.Fprintf(out, "remote_asset\t%d\t%s\n", asset.ID, asset.Name); err != nil {
				return err
			}
		}
		for _, name := range plan.Missing {
			if _, err := fmt.Fprintf(out, "missing\t%s\n", name); err != nil {
				return err
			}
		}
		return nil
	case "remote-plan":
		flags := flag.NewFlagSet("releasecheck publication remote-plan", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		dist := flags.String("dist", "", "retained output parent")
		environment := flags.String("environment", "", "release environment")
		remote := flags.String("remote-json", "", "complete paginated GitHub release response")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dist) || !filepath.IsAbs(*remote) {
			return errors.New("publication remote-plan requires absolute retained and remote paths")
		}
		publication, err := readPublication(*dist, *environment)
		if err != nil {
			return err
		}
		releases, err := readGithubReleasePages(*remote)
		if err != nil {
			return err
		}
		plan, err := planRemotePublication(publication, *dist, releases)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "status\t%s\n", plan.Status); err != nil {
			return err
		}
		if plan.Release != nil {
			if _, err := fmt.Fprintf(out, "release_id\t%d\n", plan.Release.ID); err != nil {
				return err
			}
			for _, asset := range plan.Release.Assets {
				if _, err := fmt.Fprintf(out, "remote_asset\t%d\t%s\n", asset.ID, asset.Name); err != nil {
					return err
				}
			}
		}
		for _, name := range plan.Missing {
			if _, err := fmt.Fprintf(out, "missing\t%s\n", name); err != nil {
				return err
			}
		}
		return nil
	case "latest":
		flags := flag.NewFlagSet("releasecheck publication latest", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		dist := flags.String("dist", "", "retained output parent")
		environment := flags.String("environment", "", "release environment")
		remote := flags.String("remote-json", "", "latest GitHub release response")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dist) || !filepath.IsAbs(*remote) {
			return errors.New("publication latest requires absolute retained and remote paths")
		}
		publication, err := readPublication(*dist, *environment)
		if err != nil {
			return err
		}
		return validateLatestRelease(publication, *remote)
	case "stable-history":
		flags := flag.NewFlagSet("releasecheck publication stable-history", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		dist := flags.String("dist", "", "retained output parent")
		remote := flags.String("remote-json", "", "complete paginated GitHub release response")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dist) || !filepath.IsAbs(*remote) {
			return errors.New("publication stable-history requires absolute retained and remote paths")
		}
		publication, err := readPublication(*dist, "prod")
		if err != nil {
			return err
		}
		releases, err := readGithubReleasePages(*remote)
		if err != nil {
			return err
		}
		required, err := validateStableHistory(publication, releases)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "latest_required\t%t\n", required)
		return err
	case "acceptance-template":
		flags := flag.NewFlagSet("releasecheck publication acceptance-template", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		dist := flags.String("dist", "", "completed stage output")
		output := flags.String("output", "", "new acceptance record")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dist) || !filepath.IsAbs(*output) {
			return errors.New("acceptance-template requires absolute --dist and --output")
		}
		return createStageAcceptanceTemplate(*dist, *output)
	case "acceptance-plan", "acceptance-verify":
		operation := args[0]
		flags := flag.NewFlagSet("releasecheck publication "+operation, flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		dist := flags.String("dist", "", "production output parent")
		remote := flags.String("remote-json", "", "complete paginated GitHub release response")
		downloads := flags.String("downloads", "", "fresh downloaded stage assets")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dist) || !filepath.IsAbs(*remote) {
			return errors.New("publication acceptance check requires absolute retained and remote paths")
		}
		if operation == "acceptance-verify" {
			if !filepath.IsAbs(*downloads) {
				return errors.New("publication acceptance verification requires absolute downloads path")
			}
			return verifyStageAcceptanceDownloads(*dist, *remote, *downloads)
		}
		releases, err := readGithubReleasePages(*remote)
		if err != nil {
			return err
		}
		_, assets, err := planStageAcceptanceRemote(*dist, releases)
		if err != nil {
			return err
		}
		for _, asset := range assets {
			if _, err := fmt.Fprintf(out, "remote_asset\t%d\t%s\n", asset.ID, asset.Name); err != nil {
				return err
			}
		}
		return nil
	default:
		return errors.New("usage: releasecheck publication candidate|prepare|describe|remote-plan|reconcile|recover-plan|recover|stable-history|latest|acceptance-template|acceptance-plan|acceptance-verify")
	}
}

func runRepository(args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New("repository does not accept arguments")
	}
	manifest, err := loadEnvironmentManifest("release/environments.json")
	if err != nil {
		return err
	}
	if err := validateEnvironmentManifest(manifest); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, manifest.Repository)
	return err
}

func runEnvironmentURL(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("releasecheck env-url", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	environment := flags.String("environment", "", "release environment")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid env-url arguments")
	}
	manifest, err := loadEnvironmentManifest("release/environments.json")
	if err != nil {
		return err
	}
	if err := validateEnvironmentManifest(manifest); err != nil {
		return err
	}
	var serverURL string
	switch *environment {
	case "stage":
		serverURL = manifest.Stage.ServerURL
	case "prod":
		serverURL = manifest.Prod.ServerURL
	default:
		return errors.New("environment must be stage or prod")
	}
	_, err = fmt.Fprintln(out, serverURL)
	return err
}

func runReceipt(args []string) error {
	if len(args) == 0 || args[0] != "verify" {
		return errors.New("usage: releasecheck receipt verify --environment ENV --dist PATH")
	}
	flags := flag.NewFlagSet("releasecheck receipt verify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	environment := flags.String("environment", "", "release environment")
	dist := flags.String("dist", "", "retained output parent")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dist) {
		return errors.New("receipt verify requires --environment and absolute --dist")
	}
	return verifyReceipt(*dist, *environment)
}

func runEnvironment(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("releasecheck env", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	environment := flags.String("environment", "", "release environment")
	serverURL := flags.String("server-url", "", "asserted server URL")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid env arguments")
	}
	if *environment == "" || *serverURL == "" {
		return errors.New("env requires --environment and --server-url")
	}
	manifest, err := loadEnvironmentManifest("release/environments.json")
	if err != nil {
		return err
	}
	if err := checkEnvironment(manifest, *environment, *serverURL); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "environment %s is valid\n", *environment)
	return err
}
