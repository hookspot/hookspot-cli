package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAcceptsExactNativeHelper(t *testing.T) {
	receipt, tools := bootstrapFixture(t)
	if err := run([]string{"--receipt", receipt, "--tools", tools, "--arch", "arm64"}); err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsSubstitutedOrUnsafeHelper(t *testing.T) {
	t.Run("substituted bytes", func(t *testing.T) {
		receipt, tools := bootstrapFixture(t)
		if err := os.WriteFile(filepath.Join(tools, "releasecheck-linux-arm64"), []byte("substituted"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := run([]string{"--receipt", receipt, "--tools", tools, "--arch", "arm64"}); err == nil {
			t.Fatal("substituted helper was accepted")
		}
	})
}

func TestRunRequiresFixedReceiptHelperRecords(t *testing.T) {
	tests := []struct {
		name   string
		change func(*bootstrapReceipt)
	}{
		{name: "schema", change: func(receipt *bootstrapReceipt) { receipt.SchemaVersion = 1 }},
		{name: "missing helper", change: func(receipt *bootstrapReceipt) { receipt.Helpers = receipt.Helpers[:1] }},
		{name: "wrong name", change: func(receipt *bootstrapReceipt) { receipt.Helpers[1].Name = "other" }},
		{name: "invalid digest", change: func(receipt *bootstrapReceipt) { receipt.Helpers[1].SHA256 = strings.Repeat("A", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receiptPath, tools := bootstrapFixture(t)
			contents, err := os.ReadFile(receiptPath)
			if err != nil {
				t.Fatal(err)
			}
			var receipt bootstrapReceipt
			if err := json.Unmarshal(contents, &receipt); err != nil {
				t.Fatal(err)
			}
			test.change(&receipt)
			writeBootstrapReceipt(t, receiptPath, receipt)
			if err := run([]string{"--receipt", receiptPath, "--tools", tools, "--arch", "arm64"}); err == nil {
				t.Fatal("invalid helper record was accepted")
			}
		})
	}
}

func bootstrapFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	tools := filepath.Join(dir, "tools")
	if err := os.Mkdir(tools, 0o700); err != nil {
		t.Fatal(err)
	}
	receipt := bootstrapReceipt{SchemaVersion: 2}
	for _, arch := range []string{"amd64", "arm64"} {
		name := "releasecheck-linux-" + arch
		contents := []byte("helper-" + arch)
		if err := os.WriteFile(filepath.Join(tools, name), contents, 0o700); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(contents)
		receipt.Helpers = append(receipt.Helpers, helperDigest{Name: name, SHA256: hex.EncodeToString(digest[:])})
	}
	receiptPath := filepath.Join(dir, "receipt.json")
	writeBootstrapReceipt(t, receiptPath, receipt)
	return receiptPath, tools
}

func writeBootstrapReceipt(t *testing.T, path string, receipt bootstrapReceipt) {
	t.Helper()
	contents, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}
