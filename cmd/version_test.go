package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const commandHelperEnvironment = "HOOKSPOT_COMMAND_HELPER"

func TestCommandHelper(t *testing.T) {
	if os.Getenv(commandHelperEnvironment) != "1" {
		return
	}
	version = os.Getenv("TEST_BUILD_VERSION")
	serverURL = os.Getenv("TEST_BUILD_SERVER_URL")
	buildEnvironment = os.Getenv("TEST_BUILD_ENVIRONMENT")
	commit = os.Getenv("TEST_BUILD_COMMIT")
	sourceDate = os.Getenv("TEST_BUILD_SOURCE_DATE")
	buildKind = os.Getenv("TEST_BUILD_KIND")

	separator := 0
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i + 1
			break
		}
	}
	rootCmd.SetArgs(os.Args[separator:])
	err := ExecuteContext(context.Background())
	os.Exit(HandleError(os.Stderr, err))
}

type commandResult struct {
	stdout string
	stderr string
	err    error
}

func runCommandProcess(t *testing.T, input string, metadata map[string]string, args ...string) commandResult {
	return runCommandProcessEnvironment(t, input, metadata, nil, args...)
}

func runCommandProcessEnvironment(t *testing.T, input string, metadata, environment map[string]string, args ...string) commandResult {
	return runCommandProcessDirectoryEnvironment(t, "", input, metadata, environment, args...)
}

func runCommandProcessDirectoryEnvironment(t *testing.T, directory, input string, metadata, environment map[string]string, args ...string) commandResult {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	commandArgs := append([]string{"-test.run=TestCommandHelper", "--"}, args...)
	command := exec.Command(executable, commandArgs...)
	if directory != "" {
		command.Dir = directory
	}
	command.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	home := t.TempDir()
	if configured, set := environment["HOME"]; set {
		home = configured
	}
	userProfile := home
	if configured, set := environment["USERPROFILE"]; set {
		userProfile = configured
	}
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"USERPROFILE=" + userProfile,
		commandHelperEnvironment + "=1",
		"TEST_BUILD_VERSION=" + metadata["version"],
		"TEST_BUILD_SERVER_URL=" + metadata["server_url"],
		"TEST_BUILD_ENVIRONMENT=" + metadata["environment"],
		"TEST_BUILD_COMMIT=" + metadata["commit"],
		"TEST_BUILD_SOURCE_DATE=" + metadata["source_date"],
		"TEST_BUILD_KIND=" + metadata["build_kind"],
	}
	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		command.Env = append(command.Env, "SystemRoot="+systemRoot)
	}
	for key, value := range environment {
		if key == "HOME" || key == "USERPROFILE" {
			continue
		}
		command.Env = append(command.Env, key+"="+value)
	}
	err = command.Run()
	return commandResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func TestVersionJSONIsStableAndBypassesMalformedConfig(t *testing.T) {
	badConfig := filepath.Join(t.TempDir(), "bad.toml")
	if err := writeCommandFixture(badConfig, []byte("not = [valid")); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{
		"version": "1.2.3-stage.4", "server_url": "https://STAGE.example.invalid:0443/prefix/",
		"environment": "stage", "commit": strings.Repeat("a", 40),
		"source_date": "2026-09-05T10:11:12Z", "build_kind": "release",
	}
	result := runCommandProcess(t, "", metadata, "--config", badConfig, "version", "--json")
	if result.err != nil {
		t.Fatalf("version failed: %v\nstderr: %s", result.err, result.stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &got); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, result.stdout)
	}
	want := map[string]string{
		"version": metadata["version"], "environment": "stage", "commit": metadata["commit"],
		"source_date": metadata["source_date"], "build_kind": "release", "go_version": runtime.Version(),
		"os": runtime.GOOS, "arch": runtime.GOARCH, "server_url": "https://stage.example.invalid/prefix",
	}
	if len(got) != len(want) {
		t.Fatalf("JSON keys = %v", got)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("%s = %v, want %q", key, got[key], value)
		}
	}
	if strings.Contains(result.stdout+result.stderr, "credential-sentinel") {
		t.Fatal("version output contained credential sentinel")
	}
}

func TestHumanVersionUsesReadableLabels(t *testing.T) {
	info := BuildInfo{
		Version: "1.2.3", Environment: "prod", Commit: strings.Repeat("c", 40),
		SourceDate: "2026-09-05T10:11:12Z", BuildKind: "release", GoVersion: "go1.26.8",
		OS: "darwin", Arch: "arm64", ServerURL: "https://prod.example.invalid",
	}
	var output bytes.Buffer
	if err := writeHumanVersion(&output, "hookspot", info); err != nil {
		t.Fatal(err)
	}
	want := "hookspot 1.2.3\n" +
		"environment: prod\n" +
		"server: https://prod.example.invalid\n" +
		"source: " + strings.Repeat("c", 40) + " (2026-09-05T10:11:12Z)\n" +
		"build: release\n" +
		"platform: go1.26.8 darwin/arm64\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func TestHelpAndDevelopmentVersionStayOffline(t *testing.T) {
	badConfig := filepath.Join(t.TempDir(), "bad.toml")
	if err := writeCommandFixture(badConfig, []byte("not = [valid")); err != nil {
		t.Fatal(err)
	}
	for _, serverURL := range []string{"", "http://127.0.0.1:1"} {
		metadata := map[string]string{
			"version": "dev", "server_url": serverURL, "environment": "dev",
			"commit": "unknown", "source_date": "unknown", "build_kind": "dev",
		}
		for _, args := range [][]string{{"--config", badConfig, "--help"}, {"--config", badConfig, "version"}} {
			result := runCommandProcess(t, "", metadata, args...)
			if result.err != nil {
				t.Fatalf("server %q %v failed: %v\nstderr: %s", serverURL, args, result.err, result.stderr)
			}
			if result.stdout == "" {
				t.Fatalf("server %q %v produced no output", serverURL, args)
			}
		}
	}
}

func TestHelpAndVersionIgnoreMalformedLocalConfig(t *testing.T) {
	working := t.TempDir()
	localPath := filepath.Join(working, ".hookspot", "dev", "config.toml")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeCommandFixture(localPath, []byte("schema_version = [")); err != nil {
		t.Fatal(err)
	}
	metadata := developmentMetadata("")
	for _, args := range [][]string{{"--help"}, {"version"}} {
		result := runCommandProcessDirectoryEnvironment(t, working, "", metadata, nil, args...)
		if result.err != nil || result.stdout == "" {
			t.Fatalf("%v result = %v, stdout = %q, stderr = %q", args, result.err, result.stdout, result.stderr)
		}
	}
}

func TestDeprecatedLogLevelFlagRemainsAccepted(t *testing.T) {
	result := runCommandProcess(t, "", developmentMetadata(""), "--log-level", "debug", "version")
	if result.err != nil {
		t.Fatalf("deprecated flag failed: %v\n%s", result.err, result.stderr)
	}
	if !strings.Contains(result.stderr, "deprecated") {
		t.Fatalf("deprecated flag did not warn: %q", result.stderr)
	}
}

func TestVersionDoesNotPrintMalformedCredentialBearingEndpoint(t *testing.T) {
	metadata := map[string]string{
		"version": "dev", "server_url": "https://credential-sentinel@example.invalid", "environment": "dev",
		"commit": "unknown", "source_date": "unknown", "build_kind": "dev",
	}
	result := runCommandProcess(t, "", metadata, "version", "--json")
	if result.err != nil {
		t.Fatalf("version failed: %v", result.err)
	}
	if strings.Contains(result.stdout+result.stderr, "credential-sentinel") {
		t.Fatalf("version leaked malformed endpoint:\n%s%s", result.stdout, result.stderr)
	}
}

func TestListenRejectsForwardTargetBeforeAPIRequest(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := writeCommandFixture(configPath, []byte("schema_version = 1\nenvironment = 'dev'\ncli_key = 'key-sentinel'\nproject = 'proj_1'\n")); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{
		"version": "dev", "server_url": "http://127.0.0.1:1", "environment": "dev",
		"commit": "unknown", "source_date": "unknown", "build_kind": "dev",
	}
	result := runCommandProcess(t, "", metadata, "--config", configPath, "listen", "--forward-to", "http://localhost:")
	if result.err == nil {
		t.Fatal("listen accepted invalid forward target")
	}
	combined := result.stdout + result.stderr
	if !strings.Contains(combined, "invalid --forward-to value") || strings.Contains(combined, "cannot reach") {
		t.Fatalf("unexpected output:\n%s", combined)
	}
}

func TestNetworkCommandRejectsReleaseMetadataBeforeConfigOrPrompt(t *testing.T) {
	badConfig := filepath.Join(t.TempDir(), "bad.toml")
	if err := writeCommandFixture(badConfig, []byte("not = [valid")); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{
		"version": "dev", "server_url": "", "environment": "stage",
		"commit": "unknown", "source_date": "unknown", "build_kind": "dev",
	}
	result := runCommandProcess(t, "credential-sentinel\n", metadata, "--config", badConfig, "login")
	if result.err == nil {
		t.Fatal("login succeeded with incomplete release metadata")
	}
	combined := result.stdout + result.stderr
	if !strings.Contains(combined, "install the correct stage release") {
		t.Fatalf("missing release guidance:\n%s", combined)
	}
	for _, forbidden := range []string{"Enter your", "credential-sentinel", "TOML"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("output contains %q:\n%s", forbidden, combined)
		}
	}
}

func TestCompleteSnapshotMetadataReachesCommandConfiguration(t *testing.T) {
	badConfig := filepath.Join(t.TempDir(), "bad.toml")
	if err := writeCommandFixture(badConfig, []byte("not = [valid")); err != nil {
		t.Fatal(err)
	}
	for _, environment := range []string{"stage", "prod"} {
		t.Run(environment, func(t *testing.T) {
			metadata := map[string]string{
				"version": "1.2.3-snapshot", "server_url": "https://" + environment + ".example.invalid",
				"environment": environment, "commit": strings.Repeat("b", 40),
				"source_date": "2026-09-05T10:11:12Z", "build_kind": "snapshot",
			}
			result := runCommandProcess(t, "", metadata, "--config", badConfig, "login")
			if result.err == nil {
				t.Fatal("login accepted malformed config")
			}
			if !strings.Contains(result.stderr, "load configuration") {
				t.Fatalf("snapshot did not reach config validation:\n%s", result.stderr)
			}
			if strings.Contains(result.stderr, "build metadata") {
				t.Fatalf("snapshot was rejected as build metadata:\n%s", result.stderr)
			}
		})
	}
}

func TestIncompleteSnapshotMetadataStopsBeforeConfiguration(t *testing.T) {
	metadata := map[string]string{
		"version": "1.2.3-snapshot", "server_url": "https://stage.example.invalid", "environment": "stage",
		"commit": "unknown", "source_date": "unknown", "build_kind": "snapshot",
	}
	result := runCommandProcess(t, "", metadata, "--config", filepath.Join(t.TempDir(), "missing.toml"), "login")
	if result.err == nil || !strings.Contains(result.stderr, "build metadata") {
		t.Fatalf("incomplete snapshot was not rejected:\n%s", result.stderr)
	}
}
