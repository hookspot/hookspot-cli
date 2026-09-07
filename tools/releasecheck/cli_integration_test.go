package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const privateAPIError = "PRIVATE_API_ERROR_SENTINEL"

type retainedCLIIntegration struct {
	environment string
	dist        string
	serverURL   string
	host        string
	port        string
	basePath    string
	projectUID  string
	sourceUID   string
}

type retainedCLI struct {
	path       string
	target     buildTarget
	receipt    buildReceipt
	binaryHash string
	dist       string
}

const (
	fixtureHappy           = "happy"
	fixtureInvalid         = "invalid-delivery"
	fixtureWithholdUpgrade = "withhold-upgrade"
	fixtureCancelForward   = "cancel-forward"
)

func TestRetainedStageAndProdBinariesUseTLSAPIAndPhoenix(t *testing.T) {
	stageDist := os.Getenv("HOOKSPOT_INTEGRATION_STAGE_DIST")
	prodDist := os.Getenv("HOOKSPOT_INTEGRATION_PROD_DIST")
	if stageDist == "" && prodDist == "" {
		t.Skip("retained binary integration inputs are not configured")
	}
	if !filepath.IsAbs(stageDist) || !filepath.IsAbs(prodDist) {
		t.Fatal("both retained binary integration inputs must be absolute paths")
	}
	runnerPlatform := os.Getenv("HOOKSPOT_INTEGRATION_RUNNER_PLATFORM")
	if runnerPlatform != "linux/amd64" && runnerPlatform != "linux/arm64" {
		t.Fatal("retained binary integration runner platform is missing or invalid")
	}
	evidenceDir := os.Getenv("HOOKSPOT_INTEGRATION_EVIDENCE_DIR")
	if !filepath.IsAbs(evidenceDir) {
		t.Fatal("retained binary integration evidence directory must be absolute")
	}

	certificate, caPath := newFixtureCertificate(t)
	cases := []retainedCLIIntegration{
		{
			environment: "stage",
			dist:        stageDist,
			serverURL:   "https://stage.release.invalid:18443/gateway/hookspot",
			host:        "stage.release.invalid",
			port:        "18443",
			basePath:    "/gateway/hookspot",
			projectUID:  "stage-project-uid",
			sourceUID:   "stage-source-uid",
		},
		{
			environment: "prod",
			dist:        prodDist,
			serverURL:   "https://prod.release.invalid",
			host:        "prod.release.invalid",
			port:        "443",
			projectUID:  "prod-project-uid",
			sourceUID:   "prod-source-uid",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.environment, func(t *testing.T) {
			runRetainedCLIIntegration(t, testCase, certificate, caPath, runnerPlatform, evidenceDir)
		})
	}
}

func runRetainedCLIIntegration(t *testing.T, fixture retainedCLIIntegration, certificate tls.Certificate, caPath, runnerPlatform, evidenceDir string) {
	t.Helper()
	runRoot, err := os.MkdirTemp(evidenceDir, fixture.environment+"-")
	if err != nil {
		t.Fatalf("allocate retained integration evidence: %v", err)
	}
	workingDist := filepath.Join(runRoot, "retained")
	if err := os.CopyFS(workingDist, os.DirFS(fixture.dist)); err != nil {
		t.Fatalf("copy retained integration input: %v", err)
	}
	cli := extractRetainedCLI(t, workingDist, fixture.environment, fixture.serverURL, runnerPlatform)
	assertRetainedCLIIdentity(t, cli)
	cliKey := strings.Repeat(string(fixture.environment[0]), 32)
	backend := newCLIBackend(fixture, cliKey, fixtureHappy)
	backend.start(t, certificate)
	defer func() {
		if backend != nil && backend.server != nil {
			backend.close(t)
		}
	}()

	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	env := []string{
		"HOME=" + home,
		"USERPROFILE=" + home,
		"SSL_CERT_FILE=" + caPath,
		"NO_PROXY=*",
	}
	scopedPrefix := "HOOKSPOT_" + strings.ToUpper(fixture.environment) + "_"

	noCAEnv := []string{"HOME=" + home, "USERPROFILE=" + home, "NO_PROXY=*", scopedPrefix + "CLI_KEY=" + cliKey}
	stdout, stderr, err := runFixtureCLI(cli.path, noCAEnv, "--config", filepath.Join(home, "no-ca.toml"), "login")
	requireOrdinaryFailure(t, err, "no-CA login")
	if !strings.Contains(stderr, "certificate signed by unknown authority") {
		t.Fatal("private fixture CA was trusted without the process-local CA file")
	}
	assertNoPrivateOutput(t, stdout, stderr, cliKey)

	stdout, stderr, err = runFixtureCLI(cli.path, append(env, scopedPrefix+"CLI_KEY="+cliKey), "--config", configPath, "login")
	if err != nil || !strings.Contains(stdout, "Logged in as fixture@example.invalid") || stderr != "" {
		t.Fatal("retained login did not complete through the fixture API")
	}
	stdout, stderr, err = runFixtureCLI(cli.path, env, "--config", configPath, "project", "list")
	if err != nil || !strings.Contains(stdout, fixture.projectUID) || stderr != "" {
		t.Fatal("retained project list did not complete through the fixture API")
	}
	stdout, stderr, err = runFixtureCLI(cli.path, env, "--config", configPath, "project", "use", fixture.projectUID)
	if err != nil || !strings.Contains(stdout, "Active project set") || stderr != "" {
		t.Fatal("retained project selection did not complete through the fixture API")
	}

	forward := newForwardFixture(t)
	defer forward.Close()
	listenEnv := append(append([]string{}, env...),
		scopedPrefix+"ORGANIZATION_SLUG=acme",
		scopedPrefix+"PROJECT_SLUG=payments",
	)
	listenOut, listenErr := runFixtureListen(t, cli.path, listenEnv, configPath, forward.URL, backend)
	if !strings.Contains(listenOut, "Listening on 1 source") || listenErr != "" {
		t.Fatal("retained listen did not complete cleanly after cancellation")
	}
	if forward.redirectedRequests() != 0 {
		t.Fatal("local response redirect was followed")
	}
	if err := forward.result(); err != nil {
		t.Fatal(err)
	}
	waitFixtureBarrier(t, backend.sessionDone, "closed Phoenix session")
	if err := backend.failure(); err != nil {
		t.Fatal(err)
	}
	if err := backend.assertSuccessfulRequestSet(); err != nil {
		t.Fatal(err)
	}

	backend.enableProjectRedirect()
	redirectStdout, stderr, err := runFixtureCLI(cli.path, env, "--config", configPath, "project", "list")
	requireOrdinaryFailure(t, err, "redirected project list")
	if !strings.Contains(stderr, "redirect blocked") || !strings.Contains(stderr, "protect the Hookspot CLI key") {
		t.Fatal("API redirect did not produce protected-key guidance")
	}
	assertNoPrivateOutput(t, redirectStdout, stderr, cliKey)
	if backend.redirectedRequests() != 0 {
		t.Fatal("API redirect exposed the key or followed the redirect")
	}

	backend.enableUnauthorizedMe()
	unauthorizedConfig := filepath.Join(home, "unauthorized.toml")
	unauthorizedStdout, stderr, err := runFixtureCLI(cli.path, append(env, scopedPrefix+"CLI_KEY="+cliKey), "--config", unauthorizedConfig, "login")
	requireOrdinaryFailure(t, err, "unauthorized login")
	if !strings.Contains(stderr, "authentication failed") {
		t.Fatal("API authentication failure was not presented clearly")
	}
	assertNoPrivateOutput(t, unauthorizedStdout, stderr, cliKey)

	backend.close(t)
	backend = nil
	runInvalidDeliveryControl(t, fixture, certificate, cliKey, cli.path, listenEnv, configPath)
	runUpgradeCancellationControl(t, fixture, certificate, cliKey, cli.path, listenEnv, configPath)
	runForwardCancellationControl(t, fixture, certificate, cliKey, cli.path, listenEnv, configPath)

	var manualOutput bytes.Buffer
	err = runNativeManual([]string{
		"--dist", cli.dist,
		"--target", cli.target.OS + "/" + cli.target.Arch,
		"--check", "network",
		"--result", "pass",
		"--operator", "fixture-operator",
		"--procedure", "local-tls-phoenix-v1",
	}, &manualOutput)
	if err != nil {
		t.Fatalf("retain identity-bound network observation: %v", err)
	}
	relative, found := strings.CutPrefix(strings.TrimSpace(manualOutput.String()), "native manual check retained: ")
	if !found || relative == "" {
		t.Fatal("native manual command did not report the retained record")
	}
	stored, _, err := readManualCheckRecord(cli.dist, relative, fixture.environment, cli.target)
	if err != nil || stored.Check != "network" || stored.Target != cli.target || stored.BinarySHA256 != cli.binaryHash {
		t.Fatal("stored network observation did not retain its artifact identity")
	}
}

func extractRetainedCLI(t *testing.T, dist, environment, serverURL, runnerPlatform string) retainedCLI {
	t.Helper()
	receipt, _, err := readEvidenceReceipt(dist)
	if err != nil {
		t.Fatalf("read retained receipt: %v", err)
	}
	parts := strings.Split(receipt.BuilderPlatform, "/")
	if len(parts) != 2 || parts[0] != "linux" {
		t.Fatalf("receipt has invalid original builder target %q", receipt.BuilderPlatform)
	}
	if runnerPlatform != receipt.BuilderPlatform || runtime.GOOS+"/"+runtime.GOARCH != runnerPlatform {
		t.Fatalf("fixture runner does not match original builder target %q", receipt.BuilderPlatform)
	}
	if receipt.Environment != environment || receipt.ServerURL != serverURL {
		t.Fatal("retained binary has the wrong environment endpoint")
	}
	target := buildTarget{OS: parts[0], Arch: parts[1]}
	artifact, ok := receiptArtifact(receipt, target)
	if !ok {
		t.Fatal("receipt has no archive for its original builder target")
	}
	binaryName := releaseBinaryName(environment, target.OS)
	archive, err := readReleaseArchive(filepath.Join(dist, "artifacts", artifact.Name), binaryName, defaultArtifactLimits)
	if err != nil || archive.sha256 != artifact.SHA256 {
		t.Fatal("retained archive does not match the receipt")
	}
	binary := filepath.Join(t.TempDir(), binaryName)
	if err := os.WriteFile(binary, archive.members[binaryName].data, 0o700); err != nil {
		t.Fatal(err)
	}
	writtenHash, err := hashBoundedFile(binary, defaultArtifactLimits.maxMemberBytes)
	if err != nil || writtenHash != sha256Hex(archive.members[binaryName].data) {
		t.Fatal("extracted executable bytes changed before execution")
	}
	return retainedCLI{
		path: binary, target: target, receipt: receipt,
		binaryHash: writtenHash, dist: dist,
	}
}

func assertRetainedCLIIdentity(t *testing.T, cli retainedCLI) {
	t.Helper()
	home := t.TempDir()
	stdout, stderr, err := runFixtureCLI(cli.path, []string{"HOME=" + home, "USERPROFILE=" + home}, "version", "--json")
	if err != nil || stderr != "" {
		t.Fatal("retained executable did not report build identity")
	}
	observed, err := decodeObservedBuildInfo([]byte(stdout))
	want := observedBuildInfo{
		Version: cli.receipt.Version, Environment: cli.receipt.Environment, Commit: cli.receipt.Commit,
		SourceDate: cli.receipt.SourceDate, BuildKind: cli.receipt.BuildKind, GoVersion: cli.receipt.GoVersion,
		OS: cli.target.OS, Arch: cli.target.Arch, ServerURL: cli.receipt.ServerURL,
	}
	if err != nil || observed != want || filepath.Base(cli.path) != releaseBinaryName(cli.receipt.Environment, cli.target.OS) {
		t.Fatal("retained executable identity does not match its receipt")
	}
}

func assertNoPrivateOutput(t *testing.T, stdout, stderr, cliKey string) {
	t.Helper()
	if strings.Contains(stdout, cliKey) || strings.Contains(stderr, cliKey) ||
		strings.Contains(stdout, privateAPIError) || strings.Contains(stderr, privateAPIError) {
		t.Fatal("fault output exposed fixture-private data")
	}
}

func requireOrdinaryFailure(t *testing.T, err error, operation string) {
	t.Helper()
	var exitError *exec.ExitError
	if err == nil || errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &exitError) || exitError.ExitCode() <= 0 {
		t.Fatalf("%s did not return an ordinary nonzero process exit", operation)
	}
}

func runInvalidDeliveryControl(t *testing.T, fixture retainedCLIIntegration, certificate tls.Certificate, cliKey, binary string, environment []string, configPath string) {
	t.Helper()
	backend := newCLIBackend(fixture, cliKey, fixtureInvalid)
	backend.start(t, certificate)
	defer backend.close(t)
	forward := newForwardFixture(t)
	defer forward.Close()

	stdout, stderr, err := runFixtureCLI(binary, environment, "--config", configPath, "listen", "orders", "--forward-to", forward.URL)
	requireOrdinaryFailure(t, err, "invalid delivery listen")
	if !strings.Contains(stderr, "missing correlation fields") || strings.Contains(stderr, privateAPIError) {
		t.Fatal("invalid joined delivery did not produce a concise private diagnostic")
	}
	assertNoPrivateOutput(t, stdout, stderr, cliKey)
	if err := waitFixturePhase(backend.networkResult); err != nil {
		t.Fatal(err)
	}
	waitFixtureBarrier(t, backend.sessionDone, "closed invalid-delivery session")
	if forward.requestCount() != 0 {
		t.Fatal("invalid joined delivery reached the local target")
	}
}

func runUpgradeCancellationControl(t *testing.T, fixture retainedCLIIntegration, certificate tls.Certificate, cliKey, binary string, environment []string, configPath string) {
	t.Helper()
	backend := newCLIBackend(fixture, cliKey, fixtureWithholdUpgrade)
	backend.start(t, certificate)
	defer backend.close(t)
	command := startFixtureListen(t, binary, environment, configPath, "http://127.0.0.1:1")
	waitFixtureBarrier(t, backend.phaseReady, "HTTP upgrade")
	cancelFixtureListen(t, command)
	assertNoPrivateOutput(t, command.stdout.String(), command.stderr.String(), cliKey)
	backend.releaseHandler()
	if err := waitFixturePhase(backend.networkResult); err != nil {
		t.Fatal(err)
	}
}

func runForwardCancellationControl(t *testing.T, fixture retainedCLIIntegration, certificate tls.Certificate, cliKey, binary string, environment []string, configPath string) {
	t.Helper()
	backend := newCLIBackend(fixture, cliKey, fixtureCancelForward)
	backend.start(t, certificate)
	defer backend.close(t)
	forward := newBlockingForwardFixture(t)
	command := startFixtureListen(t, binary, environment, configPath, forward.URL)
	waitFixtureBarrier(t, forward.started, "local forward response")
	cancelFixtureListen(t, command)
	assertNoPrivateOutput(t, command.stdout.String(), command.stderr.String(), cliKey)
	forward.releaseResponse()
	backend.releaseHandler()
	if err := waitFixturePhase(backend.networkResult); err != nil {
		t.Fatal(err)
	}
	waitFixtureBarrier(t, backend.sessionDone, "closed forward-cancellation session")
	if forward.requestCount() != 1 {
		t.Fatal("forward cancellation fixture received the wrong request count")
	}
}

func waitFixtureBarrier(t *testing.T, barrier <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-barrier:
	case <-time.After(15 * time.Second):
		t.Fatalf("fixture did not reach %s phase", name)
	}
}

func waitFixturePhase(result <-chan error) error {
	select {
	case err := <-result:
		return err
	case <-time.After(15 * time.Second):
		return errors.New("fixture phase did not finish")
	}
}

func runFixtureCLI(binary string, environment []string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = environment
	command.Stdin = strings.NewReader("")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if ctx.Err() != nil {
		return stdout.String(), stderr.String(), ctx.Err()
	}
	return stdout.String(), stderr.String(), err
}

func runFixtureListen(t *testing.T, binary string, environment []string, configPath, forwardURL string, backend *cliBackend) (string, string) {
	t.Helper()
	running := startFixtureListen(t, binary, environment, configPath, forwardURL)

	select {
	case err := <-backend.networkResult:
		if err != nil {
			_ = killAndReapFixtureCLI(running)
			t.Fatalf("Phoenix fixture failed: %v", err)
		}
	case err := <-running.waited:
		t.Fatalf("listen exited before the network phase completed: %v", err)
	case <-time.After(15 * time.Second):
		_ = killAndReapFixtureCLI(running)
		t.Fatal("listen did not complete the network phase")
	}

	cancelFixtureListen(t, running)
	return running.stdout.String(), running.stderr.String()
}

type runningFixtureCLI struct {
	command *exec.Cmd
	waited  chan error
	done    chan struct{}
	stdout  *bytes.Buffer
	stderr  *bytes.Buffer
}

func startFixtureListen(t *testing.T, binary string, environment []string, configPath, forwardURL string) *runningFixtureCLI {
	t.Helper()
	command := exec.Command(binary, "--config", configPath, "listen", "orders", "--forward-to", forwardURL+"/local-prefix")
	command.Env = environment
	command.Stdin = strings.NewReader("")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		waited <- command.Wait()
		close(done)
	}()
	running := &runningFixtureCLI{command: command, waited: waited, done: done, stdout: stdout, stderr: stderr}
	t.Cleanup(func() {
		select {
		case <-done:
			return
		default:
		}
		_ = command.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})
	return running
}

func cancelFixtureListen(t *testing.T, running *runningFixtureCLI) {
	t.Helper()
	if err := running.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-running.waited:
		if err != nil {
			t.Fatalf("listen cancellation returned an error: %v", err)
		}
	case <-time.After(10 * time.Second):
		if err := killAndReapFixtureCLI(running); err != nil {
			t.Fatalf("listen did not stop after cancellation: %v", err)
		}
		t.Fatal("listen did not stop after cancellation before force-kill")
	}
	if running.stderr.Len() != 0 {
		t.Fatal("listen cancellation produced an unexpected diagnostic")
	}
}

func killAndReapFixtureCLI(running *runningFixtureCLI) error {
	_ = running.command.Process.Kill()
	select {
	case <-running.waited:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("force-killed fixture process was not reaped")
	}
}

type cliBackend struct {
	fixture  retainedCLIIntegration
	key      string
	mode     string
	server   *http.Server
	listener net.Listener

	mu              sync.Mutex
	requests        map[string]int
	projectRedirect bool
	unauthorizedMe  bool
	failureErr      error
	networkOnce     sync.Once
	networkResult   chan error
	phaseReady      chan struct{}
	releaseOnce     sync.Once
	release         chan struct{}
	sessionOnce     sync.Once
	sessionDone     chan struct{}
	wsConnection    *websocket.Conn
}

func newCLIBackend(fixture retainedCLIIntegration, key, mode string) *cliBackend {
	return &cliBackend{
		fixture:       fixture,
		key:           key,
		mode:          mode,
		requests:      make(map[string]int),
		networkResult: make(chan error, 1),
		phaseReady:    make(chan struct{}),
		release:       make(chan struct{}),
		sessionDone:   make(chan struct{}),
	}
}

func (b *cliBackend) start(t *testing.T, certificate tls.Certificate) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:"+b.fixture.port)
	if err != nil {
		t.Fatalf("listen on fixture port: %v", err)
	}
	b.listener = tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	b.server = &http.Server{
		Handler: b, ReadHeaderTimeout: 5 * time.Second,
		ErrorLog: log.New(io.Discard, "", 0),
	}
	go func() {
		if err := b.server.Serve(b.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			b.finishNetwork(fmt.Errorf("serve TLS fixture: %w", err))
		}
	}()
}

func (b *cliBackend) close(t *testing.T) {
	t.Helper()
	b.releaseHandler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.server.Shutdown(ctx); err != nil {
		t.Fatalf("stop TLS fixture: %v", err)
	}
}

func (b *cliBackend) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	b.mu.Lock()
	b.requests[request.URL.Path]++
	projectRedirect := b.projectRedirect
	unauthorizedMe := b.unauthorizedMe
	b.mu.Unlock()

	wantHost := b.fixture.host + ":" + b.fixture.port
	if b.fixture.port == "443" {
		wantHost = b.fixture.host
	}
	websocketPath := b.fixture.basePath + "/cli/websocket"
	if request.Method != http.MethodGet || request.TLS == nil || request.TLS.ServerName != b.fixture.host ||
		(request.URL.Path != websocketPath && request.URL.RawQuery != "") {
		http.Error(response, "bad request", http.StatusBadRequest)
		return
	}
	if request.Host != wantHost || request.Header.Get("X-CLI-KEY") != b.key {
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	base := b.fixture.basePath
	switch request.URL.Path {
	case base + "/cli/me":
		if unauthorizedMe {
			response.WriteHeader(http.StatusUnauthorized)
			_, _ = response.Write([]byte(`{"message":"` + privateAPIError + `"}`))
			return
		}
		writeFixtureJSON(response, map[string]string{"uid": "user-uid", "email": "fixture@example.invalid"})
	case base + "/cli/projects":
		if projectRedirect {
			response.Header().Set("Location", base+"/redirected")
			response.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		writeFixtureJSON(response, []any{fixtureProject(b.fixture)})
	case base + "/cli/projects/" + b.fixture.projectUID:
		writeFixtureJSON(response, fixtureProject(b.fixture))
	case base + "/cli/projects/" + b.fixture.projectUID + "/sources":
		writeFixtureJSON(response, fixtureSources(b.fixture))
	case base + "/cli/websocket":
		if request.URL.RawQuery != "vsn=2.0.0" {
			http.Error(response, "bad websocket version", http.StatusBadRequest)
			return
		}
		if b.mode == fixtureWithholdUpgrade {
			close(b.phaseReady)
			select {
			case <-request.Context().Done():
			case <-b.release:
			}
			b.finishNetwork(nil)
			return
		}
		b.serveWebSocket(response, request)
	case base + "/redirected":
		response.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(response, request)
	}
}

func fixtureProject(fixture retainedCLIIntegration) map[string]any {
	return map[string]any{
		"uid": fixture.projectUID, "name": "Payments", "slug": "payments",
		"organization": map[string]string{"uid": "org-uid", "name": "Acme", "slug": "acme"},
	}
}

func fixtureSources(fixture retainedCLIIntegration) []any {
	return []any{map[string]any{
		"uid": fixture.sourceUID, "name": "orders", "url": "https://webhook.example.invalid/orders", "active": true,
		"connections": []any{map[string]any{
			"uid": "connection-uid", "active": true, "display_name": "local",
			"destination": map[string]any{"uid": "destination-uid", "path": "/capture", "active": true},
		}},
	}}
}

func writeFixtureJSON(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(value)
}

func (b *cliBackend) serveWebSocket(response http.ResponseWriter, request *http.Request) {
	defer b.sessionOnce.Do(func() { close(b.sessionDone) })
	connection, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(response, request, nil)
	if err != nil {
		b.finishNetwork(fmt.Errorf("upgrade websocket: %w", err))
		return
	}
	b.mu.Lock()
	b.wsConnection = connection
	b.mu.Unlock()
	defer connection.Close()

	_, joinBytes, err := connection.ReadMessage()
	if err != nil {
		b.finishNetwork(fmt.Errorf("read Phoenix join: %w", err))
		return
	}
	joinRef, err := validateFixtureJoin(joinBytes, b.fixture)
	if err != nil {
		b.finishNetwork(err)
		return
	}
	topic := "project:" + b.fixture.projectUID
	joinReply := []any{joinRef, joinRef, topic, "phx_reply", map[string]any{"status": "ok", "response": map[string]any{}}}
	if err := connection.WriteJSON(joinReply); err != nil {
		b.finishNetwork(fmt.Errorf("write Phoenix join reply: %w", err))
		return
	}
	if b.mode == fixtureInvalid {
		invalid := map[string]any{"attempt_uid": "attempt-uid", "private": privateAPIError}
		if err := connection.WriteJSON([]any{joinRef, nil, topic, "delivery", invalid}); err != nil {
			b.finishNetwork(fmt.Errorf("write invalid Phoenix delivery: %w", err))
			return
		}
		if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			b.finishNetwork(fmt.Errorf("set invalid-delivery read deadline: %w", err))
			return
		}
		_, _, err := connection.ReadMessage()
		if err == nil {
			b.finishNetwork(errors.New("invalid Phoenix delivery received a response"))
			return
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			b.finishNetwork(errors.New("invalid Phoenix delivery did not close the session"))
			return
		}
		b.finishNetwork(nil)
		return
	}
	delivery := map[string]any{
		"attempt_uid": "attempt-uid", "request_uid": "request-uid", "source_uid": b.fixture.sourceUID,
		"method": "POST", "path": "/capture", "query": "fixture=1",
		"headers": map[string][]string{"Content-Type": {"text/plain"}},
		"body":    base64.RawStdEncoding.EncodeToString([]byte("hello")),
	}
	if err := connection.WriteJSON([]any{joinRef, nil, topic, "delivery", delivery}); err != nil {
		b.finishNetwork(fmt.Errorf("write Phoenix delivery: %w", err))
		return
	}
	_, responseBytes, err := connection.ReadMessage()
	if err != nil {
		if b.mode == fixtureCancelForward {
			b.finishNetwork(nil)
			return
		}
		b.finishNetwork(fmt.Errorf("read Phoenix delivery response: %w", err))
		return
	}
	if err := validateFixtureDeliveryResponse(responseBytes, joinRef, b.fixture); err != nil {
		b.finishNetwork(err)
		return
	}
	b.finishNetwork(nil)
	if _, _, err := connection.ReadMessage(); err == nil {
		b.setFailure(errors.New("Phoenix connection received a frame after the completed response"))
	}
}

func validateFixtureJoin(frameBytes []byte, fixture retainedCLIIntegration) (string, error) {
	frame, err := decodeFixtureFrame(frameBytes)
	if err != nil {
		return "", err
	}
	var joinRef, reference, topic, event string
	var payload struct {
		Sources []string `json:"sources"`
	}
	if json.Unmarshal(frame[0], &joinRef) != nil || json.Unmarshal(frame[1], &reference) != nil || json.Unmarshal(frame[2], &topic) != nil ||
		json.Unmarshal(frame[3], &event) != nil || json.Unmarshal(frame[4], &payload) != nil ||
		joinRef == "" || reference == "" || joinRef != reference || topic != "project:"+fixture.projectUID || event != "phx_join" ||
		len(payload.Sources) != 1 || payload.Sources[0] != fixture.sourceUID {
		return "", errors.New("Phoenix join did not preserve topic and selected source identity")
	}
	return reference, nil
}

func validateFixtureDeliveryResponse(frameBytes []byte, activeJoinRef string, fixture retainedCLIIntegration) error {
	if !bytes.Contains(frameBytes, []byte(`"body":"b2s="`)) {
		return errors.New("Phoenix delivery response was not padded Base64 on the wire")
	}
	frame, err := decodeFixtureFrame(frameBytes)
	if err != nil {
		return err
	}
	var joinRef, responseRef, topic, event string
	var payload struct {
		AttemptUID string      `json:"attempt_uid"`
		Status     int         `json:"status"`
		Headers    http.Header `json:"headers"`
		Body       string      `json:"body"`
	}
	if json.Unmarshal(frame[0], &joinRef) != nil || json.Unmarshal(frame[1], &responseRef) != nil ||
		json.Unmarshal(frame[2], &topic) != nil || json.Unmarshal(frame[3], &event) != nil || json.Unmarshal(frame[4], &payload) != nil ||
		joinRef != activeJoinRef || responseRef == "" ||
		topic != "project:"+fixture.projectUID || event != "delivery_response" || payload.AttemptUID != "attempt-uid" ||
		payload.Status != http.StatusFound || payload.Headers.Get("Location") != "/local-redirect" || payload.Body != "b2s=" {
		return errors.New("Phoenix delivery response did not preserve correlation and first local response")
	}
	return nil
}

func decodeFixtureFrame(frameBytes []byte) ([]json.RawMessage, error) {
	var frame []json.RawMessage
	if err := json.Unmarshal(frameBytes, &frame); err != nil || len(frame) != 5 {
		return nil, errors.New("invalid Phoenix V2 frame")
	}
	return frame, nil
}

func (b *cliBackend) finishNetwork(err error) {
	b.networkOnce.Do(func() { b.networkResult <- err })
}

func (b *cliBackend) setFailure(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failureErr == nil {
		b.failureErr = err
	}
}

func (b *cliBackend) failure() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failureErr
}

func (b *cliBackend) releaseHandler() {
	b.releaseOnce.Do(func() { close(b.release) })
	b.mu.Lock()
	connection := b.wsConnection
	b.mu.Unlock()
	if connection != nil {
		_ = connection.Close()
	}
}

func (b *cliBackend) enableProjectRedirect() {
	b.mu.Lock()
	b.projectRedirect = true
	b.mu.Unlock()
}

func (b *cliBackend) enableUnauthorizedMe() {
	b.mu.Lock()
	b.unauthorizedMe = true
	b.mu.Unlock()
}

func (b *cliBackend) redirectedRequests() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.requests[b.fixture.basePath+"/redirected"]
}

func (b *cliBackend) assertSuccessfulRequestSet() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	base := b.fixture.basePath
	want := map[string]int{
		base + "/cli/me":                                            1,
		base + "/cli/projects":                                      2,
		base + "/cli/projects/" + b.fixture.projectUID:              1,
		base + "/cli/projects/" + b.fixture.projectUID + "/sources": 1,
		base + "/cli/websocket":                                     1,
	}
	for path, count := range want {
		if b.requests[path] != count {
			return fmt.Errorf("fixture route %s received %d requests, want %d", path, b.requests[path], count)
		}
	}
	for path := range b.requests {
		if _, expected := want[path]; !expected {
			return fmt.Errorf("fixture received an unexpected request for %s", path)
		}
	}
	return nil
}

type forwardFixture struct {
	*httptest.Server
	mu         sync.Mutex
	redirected int
	requests   int
	err        error
}

func newForwardFixture(t *testing.T) *forwardFixture {
	t.Helper()
	fixture := &forwardFixture{}
	fixture.Server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		fixture.requests++
		switch request.URL.Path {
		case "/local-prefix/capture":
			if request.Method != http.MethodPost || request.URL.RawQuery != "fixture=1" {
				fixture.err = errors.New("forwarded request did not preserve method, path, and query")
			}
			body, err := io.ReadAll(io.LimitReader(request.Body, 6))
			if err != nil {
				fixture.err = fmt.Errorf("read forwarded body: %w", err)
			}
			if len(body) != 5 || string(body) != "hello" {
				fixture.err = errors.New("forwarded request did not decode raw Base64 body")
			}
			if request.Header.Get("Content-Type") != "text/plain" {
				fixture.err = errors.New("forwarded request did not preserve Content-Type")
			}
			response.Header().Set("Location", "/local-redirect")
			response.WriteHeader(http.StatusFound)
			_, _ = response.Write([]byte("ok"))
		case "/local-redirect":
			fixture.redirected++
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	return fixture
}

func (f *forwardFixture) redirectedRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.redirected
}

func (f *forwardFixture) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func (f *forwardFixture) result() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.requests != 1 {
		return fmt.Errorf("forward fixture received %d requests, want 1", f.requests)
	}
	return f.err
}

type blockingForwardFixture struct {
	*httptest.Server
	mu          sync.Mutex
	requests    int
	started     chan struct{}
	startedOnce sync.Once
	release     chan struct{}
	releaseOnce sync.Once
}

func newBlockingForwardFixture(t *testing.T) *blockingForwardFixture {
	t.Helper()
	fixture := &blockingForwardFixture{
		started: make(chan struct{}), release: make(chan struct{}),
	}
	fixture.Server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fixture.mu.Lock()
		fixture.requests++
		fixture.mu.Unlock()
		fixture.startedOnce.Do(func() { close(fixture.started) })
		select {
		case <-request.Context().Done():
		case <-fixture.release:
		}
	}))
	t.Cleanup(fixture.Close)
	return fixture
}

func (f *blockingForwardFixture) Close() {
	f.releaseResponse()
	f.Server.Close()
}

func (f *blockingForwardFixture) releaseResponse() {
	f.releaseOnce.Do(func() { close(f.release) })
}

func (f *blockingForwardFixture) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func newFixtureCertificate(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	now := time.Now()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Hookspot CLI integration fixture CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Hookspot CLI integration fixture"},
		DNSNames:  []string{"stage.release.invalid", "prod.release.invalid"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, rootTemplate, &leafKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKeyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafKeyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(t.TempDir(), "fixture-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certificate, caPath
}
