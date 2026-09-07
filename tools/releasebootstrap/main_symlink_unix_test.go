//go:build unix

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunRejectsSymlinkHelper(t *testing.T) {
	receipt, tools := bootstrapFixture(t)
	helper := filepath.Join(tools, "releasecheck-linux-arm64")
	if err := os.Remove(helper); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tools, "releasecheck-linux-amd64"), helper); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--receipt", receipt, "--tools", tools, "--arch", "arm64"}); err == nil {
		t.Fatal("symlink helper was accepted")
	}
}
