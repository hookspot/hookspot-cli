package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validManifest = `{
  "schema_version": 1,
  "repository": "example/hookspot-cli",
  "stage": {"server_url": "https://stage.example.invalid/gateway", "branch": "stage"},
  "prod": {"server_url": "https://prod.example.invalid/gateway", "branch": "main"}
}`

func writeManifest(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "environments.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckEnvironmentAcceptsMatchingCanonicalURL(t *testing.T) {
	manifest, err := loadEnvironmentManifest(writeManifest(t, validManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkEnvironment(manifest, "stage", "https://STAGE.example.invalid:0443/gateway/"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckEnvironmentRejectsSwappedURL(t *testing.T) {
	manifest, err := loadEnvironmentManifest(writeManifest(t, validManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkEnvironment(manifest, "stage", "https://prod.example.invalid/gateway"); err == nil {
		t.Fatal("swapped environment URL was accepted")
	}
}

func TestManifestRejectsEquivalentEnvironmentURLs(t *testing.T) {
	fixtures := []string{
		`{"schema_version":1,"repository":"example/repo","stage":{"server_url":"https://same.example.invalid","branch":"stage"},"prod":{"server_url":"https://same.example.invalid:443/","branch":"main"}}`,
		`{"schema_version":1,"repository":"example/repo","stage":{"server_url":"https://[2001:db8::1]","branch":"stage"},"prod":{"server_url":"https://[2001:0db8:0:0:0:0:0:1]:0443","branch":"main"}}`,
	}
	for _, fixture := range fixtures {
		manifest, err := loadEnvironmentManifest(writeManifest(t, fixture))
		if err != nil {
			t.Fatal(err)
		}
		if err := validateEnvironmentManifest(manifest); err == nil {
			t.Fatal("equivalent stage and prod URLs were accepted")
		}
	}
}

func TestManifestRejectsUnknownFieldsAndSchema(t *testing.T) {
	fixtures := []string{
		`{"schema_version":2,"repository":"example/repo","stage":{"server_url":"https://stage.example.invalid","branch":"stage"},"prod":{"server_url":"https://prod.example.invalid","branch":"main"}}`,
		`{"schema_version":1,"repository":"example/repo","unknown":true,"stage":{"server_url":"https://stage.example.invalid","branch":"stage"},"prod":{"server_url":"https://prod.example.invalid","branch":"main"}}`,
		`{"schema_version":1,"repository":"example/repo","stage":{"server_url":"https://stage.example.invalid","branch":"stage","unknown":true},"prod":{"server_url":"https://prod.example.invalid","branch":"main"}}`,
	}
	for _, fixture := range fixtures {
		manifest, err := loadEnvironmentManifest(writeManifest(t, fixture))
		if err == nil {
			err = validateEnvironmentManifest(manifest)
		}
		if err == nil {
			t.Fatal("invalid manifest was accepted")
		}
	}
}

func TestManifestRejectsUnsafeRepositoryNames(t *testing.T) {
	for _, repository := range []string{".", "../repo", "owner/..", "owner/repo:other", "https://example.invalid/repo", "owner/repo\nother"} {
		fixture := strings.Replace(validManifest, "example/hookspot-cli", repository, 1)
		manifest, err := loadEnvironmentManifest(writeManifest(t, fixture))
		if err == nil {
			err = validateEnvironmentManifest(manifest)
		}
		if err == nil {
			t.Fatalf("unsafe repository %q was accepted", repository)
		}
	}
}

func TestEnvironmentErrorsDoNotEchoURLs(t *testing.T) {
	manifest, err := loadEnvironmentManifest(writeManifest(t, validManifest))
	if err != nil {
		t.Fatal(err)
	}
	err = checkEnvironment(manifest, "stage", "https://credential-sentinel@stage.example.invalid")
	if err == nil {
		t.Fatal("userinfo URL was accepted")
	}
	if strings.Contains(err.Error(), "credential-sentinel") {
		t.Fatalf("error leaked URL: %v", err)
	}
}

func TestManifestRejectsEmptyReleaseURLs(t *testing.T) {
	manifest, err := loadEnvironmentManifest(writeManifest(t, `{
  "schema_version": 1,
  "repository": "example/hookspot-cli",
  "stage": {"server_url": "", "branch": "stage"},
  "prod": {"server_url": "", "branch": "main"}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateEnvironmentManifest(manifest); err == nil {
		t.Fatal("incomplete manifest passed release validation")
	}
}
