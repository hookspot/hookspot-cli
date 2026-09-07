package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRunEnvironmentPrintsOnlySafeConfirmation(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "release"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release", "environments.json"), []byte(validManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	var output bytes.Buffer
	err = run([]string{"env", "--environment", "stage", "--server-url", "https://stage.example.invalid/gateway"}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "environment stage is valid\n" {
		t.Fatalf("output = %q", got)
	}
}
