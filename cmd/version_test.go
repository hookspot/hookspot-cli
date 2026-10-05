package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const commandHelperEnvironment = "HOOKSPOT_COMMAND_HELPER"

func TestCommandHelper(t *testing.T) {
	if os.Getenv(commandHelperEnvironment) != "1" {
		return
	}
	version = os.Getenv("TEST_BUILD_VERSION")
	serverURL = os.Getenv("TEST_BUILD_SERVER_URL")
	commit = os.Getenv("TEST_BUILD_COMMIT")
	sourceDate = os.Getenv("TEST_BUILD_SOURCE_DATE")
	buildKind = os.Getenv("TEST_BUILD_KIND")
	// The linker sets version before init runs; the helper sets it after, so
	// cobra's copy has to be refreshed by hand.
	rootCmd.Version = version
	githubAPIBaseURL = os.Getenv("TEST_GITHUB_API_URL")
	// The test binary is in no package manager's tree, so the upgrade command
	// is the docs' on every machine.
	dockerEnvPath = ""

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
	command := commandProcess(t, metadata, environment, args...)
	if directory != "" {
		command.Dir = directory
	}
	command.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return commandResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

// commandProcess re-executes the test binary as the command, with only the
// given environment and a temporary HOME unless it sets one.
func commandProcess(t *testing.T, metadata, environment map[string]string, args ...string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	commandArgs := append([]string{"-test.run=TestCommandHelper", "--"}, args...)
	command := exec.Command(executable, commandArgs...)
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
		"TEST_BUILD_COMMIT=" + metadata["commit"],
		"TEST_BUILD_SOURCE_DATE=" + metadata["source_date"],
		"TEST_BUILD_KIND=" + metadata["build_kind"],
		// A closed port: the subprocess never reaches the real GitHub API.
		"TEST_GITHUB_API_URL=http://127.0.0.1:1",
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
	return command
}

func TestVersionJSONIsStableAndBypassesMalformedConfig(t *testing.T) {
	badConfig := filepath.Join(t.TempDir(), "bad.toml")
	if err := writeCommandFixture(badConfig, []byte("not = [valid")); err != nil {
		t.Fatal(err)
	}
	metadata := releaseMetadata("1.2.3-rc.4")
	metadata["server_url"] = "https://PROD.example.invalid:0443/prefix/"
	result := runCommandProcess(t, "", metadata, "--config", badConfig, "version", "--json")
	if result.err != nil {
		t.Fatalf("version failed: %v\nstderr: %s", result.err, result.stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &got); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, result.stdout)
	}
	want := map[string]string{
		"version": metadata["version"], "commit": metadata["commit"],
		"source_date": metadata["source_date"], "build_kind": "release", "go_version": runtime.Version(),
		"os": runtime.GOOS, "arch": runtime.GOARCH, "server_url": "https://prod.example.invalid/prefix",
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

func TestVersionPrintsNameAndVersion(t *testing.T) {
	metadata := developmentMetadata("")
	result := runCommandProcess(t, "", metadata, "version")
	if result.err != nil {
		t.Fatalf("version failed: %v\nstderr: %s", result.err, result.stderr)
	}
	if result.stdout != "hookspot version dev\n" {
		t.Fatalf("stdout = %q", result.stdout)
	}
}

func TestVersionFlagPrintsVersionWithoutUpgradeCheck(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"-v"}} {
		result := runCommandProcess(t, "", releaseMetadata("9.9.9"), args...)
		if result.err != nil {
			t.Fatalf("%v failed: %v\nstderr: %s", args, result.err, result.stderr)
		}
		if result.stdout != "hookspot version 9.9.9\n" {
			t.Fatalf("%v stdout = %q", args, result.stdout)
		}
	}
}

func TestNeedsToUpgrade(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"1.2.3", "v1.2.3", false},
		{"1.2.3", "1.2.3", false},
		{"1.2.3", "1.2.4", true},
		{"v1.2.3", "v1.2.4", true},
		{"1.9.1", "1.10.0", true},
		{"1.10.0", "1.9.1", false},
		{"1.9.0-rc.4", "1.9.0", true},
		{"1.10.0-rc.4", "1.9.1", false},
		{"1.9.0-rc.3", "1.9.0-rc.4", true},
		{"1.9.0-rc.9", "1.9.0-rc.10", true},
		{"1.9.0-rc.4", "1.9.0-rc.3", false},
		{"1.9.0-rc.4", "1.9.0-rc.4", false},
		{"0.0.0-snapshot.0123456", "0.1.0", true},
		{"1.2.3", "", false},
		{"1.2.3", "not-a-version", false},
	}
	for _, test := range tests {
		if got := needsToUpgrade(test.current, test.latest); got != test.want {
			t.Errorf("needsToUpgrade(%q, %q) = %v, want %v", test.current, test.latest, got, test.want)
		}
	}
}

func TestChannelUpgradeCommand(t *testing.T) {
	tests := []struct {
		executable string
		docker     bool
		want       string
	}{
		{"/opt/homebrew/Cellar/hookspot-cli/1.2.3/bin/hookspot", false, "brew upgrade hookspot-cli"},
		{`C:\Users\dev\AppData\Roaming\npm\node_modules\@hookspot\cli\binaries\windows-amd64\hookspot.exe`, false, "npm install -g @hookspot/cli@latest"},
		// A Node image installs through npm.
		{"/usr/local/lib/node_modules/@hookspot/cli/binaries/linux-arm64/hookspot", true, "npm install -g @hookspot/cli@latest"},
		{"/usr/local/bin/hookspot", true, "docker pull hookspot/cli"},
		{"/usr/local/bin/hookspot", false, upgradeDocsURL},
	}
	for _, test := range tests {
		if got := channelUpgradeCommand(test.executable, test.docker); got != test.want {
			t.Errorf("channelUpgradeCommand(%q, %v) = %q, want %q", test.executable, test.docker, got, test.want)
		}
	}
}

func stubLatestRelease(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	setGitHubAPIBaseURL(t, server.URL)
}

func setGitHubAPIBaseURL(t *testing.T, url string) {
	t.Helper()
	original := githubAPIBaseURL
	githubAPIBaseURL = url
	t.Cleanup(func() { githubAPIBaseURL = original })
}

func TestLatestVersionReadsTagFromGitHub(t *testing.T) {
	var gotPath, gotAccept, gotUserAgent string
	stubLatestRelease(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAccept = r.Header.Get("Accept")
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v2.5.0","name":"v2.5.0"}`))
	})
	if got := latestVersion(context.Background(), "1.0.0"); got != "v2.5.0" {
		t.Fatalf("latestVersion = %q", got)
	}
	if gotPath != "/repos/hookspot/hookspot-cli/releases/latest" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Fatalf("accept = %q", gotAccept)
	}
	if gotUserAgent != "hookspot-cli/1.0.0" {
		t.Fatalf("user agent = %q", gotUserAgent)
	}
}

func TestLatestVersionIgnoresFailures(t *testing.T) {
	stubLatestRelease(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	})
	if got := latestVersion(context.Background(), "1.0.0"); got != "" {
		t.Fatalf("latestVersion on 403 = %q", got)
	}
	stubLatestRelease(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})
	if got := latestVersion(context.Background(), "1.0.0"); got != "" {
		t.Fatalf("latestVersion on malformed body = %q", got)
	}

	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	setGitHubAPIBaseURL(t, server.URL)
	if got := latestVersion(context.Background(), "1.0.0"); got != "" {
		t.Fatalf("latestVersion on closed server = %q", got)
	}

	stubLatestRelease(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if got := latestVersion(ctx, "1.0.0"); got != "" {
		t.Fatalf("latestVersion on a stalled server = %q", got)
	}
}

func TestCheckLatestVersionStaysQuietWhenUpToDateOrDev(t *testing.T) {
	requests := 0
	stubLatestRelease(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"tag_name":"v1.3.0"}`))
	})

	var output bytes.Buffer
	checkLatestVersion(context.Background(), &output, "1.3.0")
	if output.Len() != 0 {
		t.Fatalf("up-to-date output = %q", output.String())
	}

	checkLatestVersion(context.Background(), &output, "dev")
	if output.Len() != 0 || requests != 1 {
		t.Fatalf("dev build checked GitHub: output = %q, requests = %d", output.String(), requests)
	}
}

func TestVersionPrintsUpdateCard(t *testing.T) {
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.3.0"}`))
	}))
	defer github.Close()
	result := runCommandProcessEnvironment(t, "", releaseMetadata("1.2.3"), map[string]string{"TEST_GITHUB_API_URL": github.URL}, "version")
	if result.err != nil {
		t.Fatalf("version failed: %v\nstderr: %s", result.err, result.stderr)
	}
	want := "hookspot version 1.2.3\n" +
		"╭─ Update available ──────────────────────╮\n" +
		"│ 1.2.3 → 1.3.0                           │\n" +
		"│ https://hookspot.dev/docs/cli#upgrading │\n" +
		"╰─────────────────────────────────────────╯\n"
	if result.stdout != want {
		t.Fatalf("stdout = %q, want %q", result.stdout, want)
	}
}

func TestHelpAndDevelopmentVersionStayOffline(t *testing.T) {
	badConfig := filepath.Join(t.TempDir(), "bad.toml")
	if err := writeCommandFixture(badConfig, []byte("not = [valid")); err != nil {
		t.Fatal(err)
	}
	for _, serverURL := range []string{"", "http://127.0.0.1:1"} {
		metadata := map[string]string{
			"version": "dev", "server_url": serverURL,
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
		"version": "dev", "server_url": "https://credential-sentinel@example.invalid",
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
	if err := writeCommandFixture(configPath, []byte("schema_version = 1\ncli_key = 'key-sentinel'\nproject = 'proj_1'\n")); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{
		"version": "dev", "server_url": "http://127.0.0.1:1",
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

func TestListenRequiresAnActiveProject(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := writeCommandFixture(configPath, []byte("schema_version = 1\ncli_key = 'key-sentinel'\n")); err != nil {
		t.Fatal(err)
	}
	metadata := developmentMetadata("http://127.0.0.1:1")
	result := runCommandProcess(t, "", metadata, "--config", configPath, "listen")
	if result.err == nil {
		t.Fatal("listen ran without a project")
	}
	if !strings.Contains(result.stderr, "no active project") || !strings.Contains(result.stderr, "Run 'hookspot project use'") {
		t.Fatalf("unexpected output:\n%s", result.stderr)
	}
}

func TestNetworkCommandRejectsMissingEndpointBeforeConfigOrPrompt(t *testing.T) {
	badConfig := filepath.Join(t.TempDir(), "bad.toml")
	if err := writeCommandFixture(badConfig, []byte("not = [valid")); err != nil {
		t.Fatal(err)
	}
	result := runCommandProcess(t, "credential-sentinel\n", developmentMetadata(""), "--config", badConfig, "login")
	if result.err == nil {
		t.Fatal("login succeeded without a server URL")
	}
	combined := result.stdout + result.stderr
	if !strings.Contains(combined, "Rebuild with SERVER_URL") {
		t.Fatalf("missing rebuild guidance:\n%s", combined)
	}
	for _, forbidden := range []string{"Enter your", "credential-sentinel", "TOML"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("output contains %q:\n%s", forbidden, combined)
		}
	}
}
