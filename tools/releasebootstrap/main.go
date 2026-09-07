package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maximumReceiptBytes = 256 * 1024
	maximumHelperBytes  = 128 * 1024 * 1024
)

type helperDigest struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type bootstrapReceipt struct {
	SchemaVersion int            `json:"schema_version"`
	Helpers       []helperDigest `json:"helpers"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "release helper check:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("release-helper-check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	receiptPath := flags.String("receipt", "", "receipt path")
	toolsPath := flags.String("tools", "", "retained tools directory")
	arch := flags.String("arch", "", "native helper architecture")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*receiptPath) || !filepath.IsAbs(*toolsPath) {
		return errors.New("receipt, tools directory, and architecture are required")
	}
	if *arch != "amd64" && *arch != "arm64" {
		return errors.New("helper architecture is unsupported")
	}

	receiptBytes, err := readRegularFile(*receiptPath, maximumReceiptBytes)
	if err != nil {
		return errors.New("receipt is missing or unsafe")
	}
	decoder := json.NewDecoder(bytes.NewReader(receiptBytes))
	var receipt bootstrapReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return errors.New("receipt JSON is invalid")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("receipt must contain one JSON object")
	}
	if receipt.SchemaVersion != 2 || !validHelperRecords(receipt.Helpers) {
		return errors.New("receipt helper records are invalid")
	}

	toolsInfo, err := os.Lstat(*toolsPath)
	if err != nil || !toolsInfo.IsDir() || toolsInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("retained tools directory is missing or unsafe")
	}
	index := 0
	if *arch == "arm64" {
		index = 1
	}
	helper := receipt.Helpers[index]
	actual, err := hashRegularFile(filepath.Join(*toolsPath, helper.Name), maximumHelperBytes)
	if err != nil {
		return errors.New("retained helper is missing or unsafe")
	}
	if actual != helper.SHA256 {
		return errors.New("retained helper digest does not match receipt")
	}
	return nil
}

func validHelperRecords(helpers []helperDigest) bool {
	names := []string{"releasecheck-linux-amd64", "releasecheck-linux-arm64"}
	if len(helpers) != len(names) {
		return false
	}
	for index, name := range names {
		if helpers[index].Name != name || !validDigest(helpers[index].SHA256) {
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	digest, err := hex.DecodeString(value)
	return err == nil && len(digest) == sha256.Size && value == strings.ToLower(value)
}

func readRegularFile(path string, maximum int64) ([]byte, error) {
	file, size, err := openRegularFile(path, maximum)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(contents)) != size {
		return nil, errors.New("file changed while reading")
	}
	return contents, nil
}

func hashRegularFile(path string, maximum int64) (string, error) {
	file, size, err := openRegularFile(path, maximum)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maximum+1))
	if err != nil || written != size {
		return "", errors.New("file changed while hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func openRegularFile(path string, maximum int64) (*os.File, int64, error) {
	before, err := os.Lstat(path)
	if err != nil || maximum < 0 || before == nil || !before.Mode().IsRegular() {
		return nil, 0, errors.New("file is missing or not regular")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() < 0 || opened.Size() > maximum {
		file.Close()
		return nil, 0, errors.New("file identity or size is invalid")
	}
	return file, opened.Size(), nil
}
