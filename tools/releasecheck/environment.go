package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"hookspot/internal/endpoint"
)

var repositoryPartPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

const maxEnvironmentManifestBytes = 64 * 1024

type environmentManifest struct {
	SchemaVersion int               `json:"schema_version"`
	Repository    string            `json:"repository"`
	Stage         environmentRecord `json:"stage"`
	Prod          environmentRecord `json:"prod"`
}

type environmentRecord struct {
	ServerURL string `json:"server_url"`
	Branch    string `json:"branch"`
}

func loadEnvironmentManifest(path string) (environmentManifest, error) {
	contents, err := readBoundedRegularFile(path, maxEnvironmentManifestBytes)
	if err != nil {
		return environmentManifest{}, fmt.Errorf("open environment manifest: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var manifest environmentManifest
	if err := decoder.Decode(&manifest); err != nil {
		return environmentManifest{}, fmt.Errorf("decode environment manifest: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return environmentManifest{}, errors.New("environment manifest must contain one JSON object")
	}
	return manifest, nil
}

func validateEnvironmentManifest(manifest environmentManifest) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("unsupported environment manifest schema %d", manifest.SchemaVersion)
	}
	if err := validateRepositoryName(manifest.Repository); err != nil {
		return err
	}
	if manifest.Stage.Branch != "stage" || manifest.Prod.Branch != "main" {
		return errors.New("environment manifest branches must be stage and main")
	}
	stage, err := endpoint.Parse(manifest.Stage.ServerURL, "stage")
	if err != nil {
		return fmt.Errorf("invalid stage environment record: %w", err)
	}
	prod, err := endpoint.Parse(manifest.Prod.ServerURL, "prod")
	if err != nil {
		return fmt.Errorf("invalid prod environment record: %w", err)
	}
	if stage.String() == prod.String() {
		return errors.New("stage and prod server URLs must identify different endpoints")
	}
	return nil
}

func validateRepositoryName(repository string) error {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || !repositoryPartPattern.MatchString(parts[0]) || !repositoryPartPattern.MatchString(parts[1]) ||
		parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return errors.New("environment manifest repository must be a safe owner/name")
	}
	return nil
}

func checkEnvironment(manifest environmentManifest, environment, assertedURL string) error {
	if err := validateEnvironmentManifest(manifest); err != nil {
		return err
	}
	var configured string
	switch environment {
	case "stage":
		configured = manifest.Stage.ServerURL
	case "prod":
		configured = manifest.Prod.ServerURL
	default:
		return errors.New("environment must be stage or prod")
	}
	asserted, err := endpoint.Parse(assertedURL, environment)
	if err != nil {
		return fmt.Errorf("invalid asserted server URL: %w", err)
	}
	want, err := endpoint.Parse(configured, environment)
	if err != nil {
		return fmt.Errorf("invalid selected environment record: %w", err)
	}
	if asserted.String() != want.String() {
		return errors.New("asserted server URL does not match the selected environment")
	}
	return nil
}
