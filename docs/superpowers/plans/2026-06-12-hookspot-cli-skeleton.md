# hookspot-cli Skeleton Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the initial Cobra-based skeleton for `hookspot-cli`: `login`/`logout` (PAT auth), `project list`/`project use`, and `listen <port> [source...]` which opens a websocket to hookspot and proxies events to a local port.

**Architecture:** A Cobra root command wires persistent flags into a Viper-backed `internal/config` package (flag > env > config file > default). `internal/api` is a small REST client (`/api/me`, `/api/projects`) used by `login` and `project list`. `internal/proxy` forwards received event bytes as HTTP POSTs to a local target. `internal/ws` is a thin websocket client (gorilla/websocket) that the `listen` command uses to receive events and hand them to the proxy.

**Tech Stack:** Go 1.23, Cobra, Viper, gorilla/websocket. All builds/tests/runs happen via Docker (no local Go toolchain required) using the `golang:1.23` image, via a `Makefile`.

---

## Build/test environment

All Go commands run inside Docker via the `Makefile` created in Task 1:

- `make build` — `go build ./...` (compile-check all packages)
- `make test` — `go test ./...`
- `make tidy` — `go mod tidy`
- `make get PKG=<module@version>` — `go get <module@version>`
- `make run ARGS="<args>"` — `go run . <args>`

A named Docker volume caches the Go module/build cache across runs so repeated commands don't re-download dependencies.

---

### Task 1: Module scaffolding, Makefile, root + version commands

**Files:**
- Create: `go.mod`
- Create: `Makefile`
- Create: `main.go`
- Create: `cmd/root.go`
- Create: `cmd/version.go`

- [ ] **Step 1: Initialize the Go module via Docker**

```bash
cd /Users/bohdan/projects/my/hookspot-cli && docker run --rm -v "$(pwd)":/src -w /src golang:1.23 go mod init hookspot-cli
```

Expected: `go: creating new go.mod: module hookspot-cli` and a `go.mod` file containing `module hookspot-cli` and a `go 1.23...` directive.

- [ ] **Step 2: Create the Makefile**

```makefile
GO_IMAGE := golang:1.23
RUN := docker run --rm -v "$(CURDIR)":/src -w /src -v hookspot-cli-gomod:/go/pkg/mod -v hookspot-cli-gocache:/root/.cache/go-build $(GO_IMAGE)

.PHONY: tidy build test vet run get

tidy:
	$(RUN) go mod tidy

build:
	$(RUN) go build ./...

test:
	$(RUN) go test ./...

vet:
	$(RUN) go vet ./...

run:
	$(RUN) go run . $(ARGS)

get:
	$(RUN) go get $(PKG)
```

- [ ] **Step 3: Add the Cobra dependency**

```bash
make get PKG="github.com/spf13/cobra@latest"
```

Expected: `go.mod` gains a `require github.com/spf13/cobra ...` line and `go.sum` is created/updated.

- [ ] **Step 4: Write `main.go`**

```go
package main

import (
	"fmt"
	"os"

	"hookspot-cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Write `cmd/root.go`**

```go
package cmd

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "hookspot-cli",
	Short: "Forward hookspot webhook events to your local machine",
	Long: `hookspot-cli connects to your hookspot project over a websocket
and proxies incoming webhook events to a local host and port.`,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
```

- [ ] **Step 6: Write `cmd/version.go`**

```go
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the hookspot-cli version",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "hookspot-cli %s\n", version)
		return err
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
```

- [ ] **Step 7: Tidy and build**

```bash
make tidy
make build
```

Expected: both commands complete with no errors.

- [ ] **Step 8: Smoke test the CLI**

```bash
make run ARGS="--help"
make run ARGS="version"
```

Expected: the first prints Cobra's help text listing the `version` and `completion`/`help` commands; the second prints `hookspot-cli dev`.

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum Makefile main.go cmd/
git commit -m "Scaffold hookspot-cli Cobra module with root and version commands"
```

---

### Task 2: `internal/config` package (Viper-backed, TDD)

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Add the Viper dependency**

```bash
make get PKG="github.com/spf13/viper@latest"
```

- [ ] **Step 2: Write the failing tests**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew_DefaultServerURL(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cfg := Load(v)
	if cfg.ServerURL != DefaultServerURL {
		t.Fatalf("ServerURL = %q, want %q", cfg.ServerURL, DefaultServerURL)
	}
}

func TestLoad_ReadsValuesFromFile(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")

	contents := "token = \"from-file\"\nproject = \"proj_1\"\n"
	if err := os.WriteFile(configFile, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cfg := Load(v)
	if cfg.Token != "from-file" {
		t.Fatalf("Token = %q, want %q", cfg.Token, "from-file")
	}
	if cfg.Project != "proj_1" {
		t.Fatalf("Project = %q, want %q", cfg.Project, "proj_1")
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(configFile, []byte("token = \"from-file\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("HOOKSPOT_TOKEN", "from-env")

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cfg := Load(v)
	if cfg.Token != "from-env" {
		t.Fatalf("Token = %q, want %q", cfg.Token, "from-env")
	}
}

func TestSave_WritesAndReloads(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "nested", "config.toml")

	v, err := New(configFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	v.Set("token", "saved-token")

	if err := Save(v, configFile); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := New(configFile)
	if err != nil {
		t.Fatalf("New (reload): %v", err)
	}

	cfg := Load(reloaded)
	if cfg.Token != "saved-token" {
		t.Fatalf("Token = %q, want %q", cfg.Token, "saved-token")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
make test
```

Expected: FAIL — `internal/config` package has no non-test Go files (`New`, `Load`, `Save`, `DefaultServerURL` undefined).

- [ ] **Step 4: Write `internal/config/config.go`**

```go
package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// DefaultServerURL is used when no server URL is configured.
const DefaultServerURL = "https://api.hookspot.dev"

// Config holds the resolved hookspot-cli settings.
type Config struct {
	Token     string
	Project   string
	ServerURL string
	LogLevel  string
}

// New returns a viper instance configured with hookspot-cli's defaults,
// environment variable bindings, and config file location. If configFile
// is empty, it defaults to $HOME/.config/hookspot-cli/config.toml.
func New(configFile string) (*viper.Viper, error) {
	v := viper.New()
	v.SetEnvPrefix("HOOKSPOT")
	v.AutomaticEnv()
	v.SetDefault("server_url", DefaultServerURL)
	v.SetDefault("log_level", "info")

	if configFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		configFile = filepath.Join(home, ".config", "hookspot-cli", "config.toml")
	}

	v.SetConfigFile(configFile)
	v.SetConfigType("toml")

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) && !os.IsNotExist(err) {
			return nil, err
		}
	}

	return v, nil
}

// Load resolves the current configuration from v.
func Load(v *viper.Viper) Config {
	return Config{
		Token:     v.GetString("token"),
		Project:   v.GetString("project"),
		ServerURL: v.GetString("server_url"),
		LogLevel:  v.GetString("log_level"),
	}
}

// Save writes v's settings to configFile (or the default location if
// empty), creating parent directories as needed.
func Save(v *viper.Viper, configFile string) error {
	if configFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		configFile = filepath.Join(home, ".config", "hookspot-cli", "config.toml")
	}

	if err := os.MkdirAll(filepath.Dir(configFile), 0o700); err != nil {
		return err
	}

	return v.WriteConfigAs(configFile)
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
make test
```

Expected: `ok      hookspot-cli/internal/config`

- [ ] **Step 6: Tidy and commit**

```bash
make tidy
git add go.mod go.sum internal/config
git commit -m "Add Viper-backed config package with load/save and env precedence"
```

---

### Task 3: Wire config into the root command

**Files:**
- Modify: `cmd/root.go`

- [ ] **Step 1: Replace `cmd/root.go` with the config-aware version**

```go
package cmd

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hookspot-cli/internal/config"
)

var (
	cfgFile string
	v       *viper.Viper
)

var rootCmd = &cobra.Command{
	Use:   "hookspot-cli",
	Short: "Forward hookspot webhook events to your local machine",
	Long: `hookspot-cli connects to your hookspot project over a websocket
and proxies incoming webhook events to a local host and port.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		v, err = config.New(cfgFile)
		if err != nil {
			return err
		}

		for _, name := range []string{"token", "project", "server-url", "log-level"} {
			key := strings.ReplaceAll(name, "-", "_")
			if err := v.BindPFlag(key, rootCmd.PersistentFlags().Lookup(name)); err != nil {
				return err
			}
		}

		return nil
	},
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default $HOME/.config/hookspot-cli/config.toml)")
	rootCmd.PersistentFlags().String("token", "", "hookspot API token (env HOOKSPOT_TOKEN)")
	rootCmd.PersistentFlags().String("project", "", "active hookspot project ID (env HOOKSPOT_PROJECT)")
	rootCmd.PersistentFlags().String("server-url", "", "hookspot server URL (env HOOKSPOT_SERVER_URL)")
	rootCmd.PersistentFlags().String("log-level", "", "log level: debug, info, warn, error (env HOOKSPOT_LOG_LEVEL)")
}
```

- [ ] **Step 2: Build**

```bash
make build
```

Expected: completes with no errors.

- [ ] **Step 3: Smoke test**

```bash
make run ARGS="version"
make run ARGS="--server-url=http://localhost:4000 version"
```

Expected: both print `hookspot-cli dev` with no errors (the second exercises the new `--server-url` flag and config binding without erroring).

- [ ] **Step 4: Commit**

```bash
git add cmd/root.go
git commit -m "Wire persistent flags into Viper config with flag > env > file > default precedence"
```

---

### Task 4: `internal/api` client (`Me`) + `login`/`logout` commands

**Files:**
- Create: `internal/api/client.go`
- Test: `internal/api/client_test.go`
- Create: `cmd/login.go`
- Create: `cmd/logout.go`

- [ ] **Step 1: Write the failing test for `Me`**

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_Me_ReturnsUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" {
			t.Errorf("path = %q, want /api/me", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-token")
		}
		if err := json.NewEncoder(w).Encode(User{ID: "usr_1", Email: "dev@example.com"}); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}))
	defer server.Close()

	client := New(server.URL, "test-token")

	user, err := client.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if user.Email != "dev@example.com" {
		t.Fatalf("Email = %q, want %q", user.Email, "dev@example.com")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
make test
```

Expected: FAIL — `internal/api` package has no non-test Go files (`New`, `User`, `Client.Me` undefined).

- [ ] **Step 3: Write `internal/api/client.go`**

```go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Client is a small REST client for the hookspot API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New returns a Client configured for baseURL, authenticating with token.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    http.DefaultClient,
	}
}

// User represents the authenticated hookspot user.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// Me returns the user associated with the client's token.
func (c *Client) Me(ctx context.Context) (*User, error) {
	var user User
	if err := c.get(ctx, "/api/me", &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (c *Client) get(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: unexpected status %d", req.Method, req.URL, resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
make test
```

Expected: `ok      hookspot-cli/internal/api`

- [ ] **Step 5: Write `cmd/login.go`**

```go
package cmd

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"hookspot-cli/internal/api"
	"hookspot-cli/internal/config"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate hookspot-cli with a personal access token",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load(v)

		token := cfg.Token
		if token == "" {
			fmt.Fprint(cmd.OutOrStdout(), "Enter your hookspot API token: ")
			reader := bufio.NewReader(cmd.InOrStdin())
			line, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("read token: %w", err)
			}
			token = strings.TrimSpace(line)
		}

		if token == "" {
			return fmt.Errorf("no token provided")
		}

		client := api.New(cfg.ServerURL, token)
		user, err := client.Me(cmd.Context())
		if err != nil {
			return fmt.Errorf("validate token: %w", err)
		}

		v.Set("token", token)
		if err := config.Save(v, cfgFile); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Logged in as %s\n", user.Email)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}
```

- [ ] **Step 6: Write `cmd/logout.go`**

```go
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"hookspot-cli/internal/config"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the stored hookspot API token",
	RunE: func(cmd *cobra.Command, args []string) error {
		v.Set("token", "")
		if err := config.Save(v, cfgFile); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Logged out.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
```

- [ ] **Step 7: Build and smoke test**

```bash
make build
make run ARGS="login --help"
make run ARGS="logout --help"
```

Expected: both `--help` invocations print usage text with no errors.

- [ ] **Step 8: Commit**

```bash
git add internal/api cmd/login.go cmd/logout.go
git commit -m "Add API client Me() and login/logout commands with PAT auth"
```

---

### Task 5: `ListProjects` + `project list`/`project use` commands

**Files:**
- Modify: `internal/api/client.go`
- Modify: `internal/api/client_test.go`
- Create: `cmd/project.go`

- [ ] **Step 1: Add the failing test for `ListProjects`**

Append to `internal/api/client_test.go`:

```go
func TestClient_ListProjects_ReturnsProjects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects" {
			t.Errorf("path = %q, want /api/projects", r.URL.Path)
		}
		projects := []Project{
			{ID: "proj_1", Name: "Production"},
			{ID: "proj_2", Name: "Staging"},
		}
		if err := json.NewEncoder(w).Encode(projects); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}))
	defer server.Close()

	client := New(server.URL, "test-token")

	projects, err := client.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("len(projects) = %d, want 2", len(projects))
	}
	if projects[0].ID != "proj_1" || projects[1].ID != "proj_2" {
		t.Fatalf("unexpected projects: %+v", projects)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
make test
```

Expected: FAIL — `Project` type and `Client.ListProjects` undefined.

- [ ] **Step 3: Add `Project` type and `ListProjects` to `internal/api/client.go`**

Append to `internal/api/client.go`:

```go
// Project represents a hookspot project accessible to the current user.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListProjects returns the projects accessible to the client's token.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var projects []Project
	if err := c.get(ctx, "/api/projects", &projects); err != nil {
		return nil, err
	}
	return projects, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
make test
```

Expected: `ok      hookspot-cli/internal/api`

- [ ] **Step 5: Write `cmd/project.go`**

```go
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"hookspot-cli/internal/api"
	"hookspot-cli/internal/config"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage the active hookspot project",
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List projects accessible to your account",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load(v)
		if cfg.Token == "" {
			return fmt.Errorf("not logged in: run 'hookspot-cli login' or set HOOKSPOT_TOKEN")
		}

		client := api.New(cfg.ServerURL, cfg.Token)
		projects, err := client.ListProjects(cmd.Context())
		if err != nil {
			return fmt.Errorf("list projects: %w", err)
		}

		for _, p := range projects {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", p.ID, p.Name)
		}
		return nil
	},
}

var projectUseCmd = &cobra.Command{
	Use:   "use <project>",
	Short: "Set the active hookspot project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v.Set("project", args[0])
		if err := config.Save(v, cfgFile); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Active project set to %s\n", args[0])
		return nil
	},
}

func init() {
	projectCmd.AddCommand(projectListCmd, projectUseCmd)
	rootCmd.AddCommand(projectCmd)
}
```

- [ ] **Step 6: Build and smoke test**

```bash
make build
make run ARGS="project --help"
make run ARGS="project list --help"
make run ARGS="project use --help"
```

Expected: all print usage text with no errors.

- [ ] **Step 7: Commit**

```bash
git add internal/api cmd/project.go
git commit -m "Add ListProjects and project list/use commands"
```

---

### Task 6: `internal/proxy` package (TDD)

**Files:**
- Create: `internal/proxy/proxy.go`
- Test: `internal/proxy/proxy_test.go`

- [ ] **Step 1: Write the failing test**

```go
package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestForwarder_Forward_SendsRequestToTarget(t *testing.T) {
	var gotPath, gotBody, gotHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeader = r.Header.Get("X-Hookspot-Event")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		gotBody = string(body)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	f := New(server.URL)

	headers := http.Header{}
	headers.Set("X-Hookspot-Event", "evt_123")

	resp, err := f.Forward(context.Background(), "/webhooks", []byte(`{"id":"evt_123"}`), headers)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if gotPath != "/webhooks" {
		t.Fatalf("path = %q, want %q", gotPath, "/webhooks")
	}
	if gotBody != `{"id":"evt_123"}` {
		t.Fatalf("body = %q, want %q", gotBody, `{"id":"evt_123"}`)
	}
	if gotHeader != "evt_123" {
		t.Fatalf("header = %q, want %q", gotHeader, "evt_123")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
make test
```

Expected: FAIL — `internal/proxy` package has no non-test Go files (`New`, `Forwarder.Forward` undefined).

- [ ] **Step 3: Write `internal/proxy/proxy.go`**

```go
package proxy

import (
	"bytes"
	"context"
	"net/http"
	"strings"
)

// Forwarder forwards received event bytes to a local target via HTTP POST.
type Forwarder struct {
	targetBaseURL string
	client        *http.Client
}

// New returns a Forwarder that sends requests to targetBaseURL.
func New(targetBaseURL string) *Forwarder {
	return &Forwarder{
		targetBaseURL: strings.TrimRight(targetBaseURL, "/"),
		client:        http.DefaultClient,
	}
}

// Forward sends body as an HTTP POST to targetBaseURL+path, copying the
// given headers onto the outgoing request.
func (f *Forwarder) Forward(ctx context.Context, path string, body []byte, headers http.Header) (*http.Response, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.targetBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	return f.client.Do(req)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
make test
```

Expected: `ok      hookspot-cli/internal/proxy`

- [ ] **Step 5: Commit**

```bash
git add internal/proxy
git commit -m "Add proxy package that forwards events via HTTP POST"
```

---

### Task 7: `internal/ws` client (gorilla/websocket, TDD)

**Files:**
- Create: `internal/ws/client.go`
- Test: `internal/ws/client_test.go`

- [ ] **Step 1: Add the gorilla/websocket dependency**

```bash
make get PKG="github.com/gorilla/websocket@latest"
```

- [ ] **Step 2: Write the failing test**

```go
package ws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var errStop = errors.New("stop after first message")

func TestClient_Listen_ReceivesMessages(t *testing.T) {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
			t.Errorf("write message: %v", err)
		}

		// Keep the connection open briefly so the client can read
		// before the handler returns and closes it.
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client := New(wsURL, "test-token")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	received := make(chan string, 1)
	err := client.Listen(ctx, func(message []byte) error {
		received <- string(message)
		return errStop
	})

	if !errors.Is(err, errStop) {
		t.Fatalf("Listen error = %v, want %v", err, errStop)
	}

	select {
	case msg := <-received:
		if msg != "hello" {
			t.Fatalf("message = %q, want %q", msg, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
make test
```

Expected: FAIL — `internal/ws` package has no non-test Go files (`New`, `Client.Listen` undefined).

- [ ] **Step 4: Write `internal/ws/client.go`**

```go
package ws

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
)

// Client connects to a hookspot websocket event stream.
type Client struct {
	url   string
	token string
}

// New returns a Client that will connect to url, authenticating with token.
func New(url, token string) *Client {
	return &Client{url: url, token: token}
}

// Listen connects to the websocket and invokes handler for each received
// message. It blocks until handler returns an error, the connection is
// closed, or ctx is cancelled.
func (c *Client) Listen(ctx context.Context, handler func(message []byte) error) error {
	header := http.Header{}
	if c.token != "" {
		header.Set("Authorization", "Bearer "+c.token)
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.url, header)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", c.url, err)
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read message: %w", err)
		}

		if err := handler(message); err != nil {
			return err
		}
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
make test
```

Expected: `ok      hookspot-cli/internal/ws`

- [ ] **Step 6: Tidy and commit**

```bash
make tidy
git add go.mod go.sum internal/ws
git commit -m "Add websocket client for receiving hookspot events"
```

---

### Task 8: `listen` command

**Files:**
- Create: `cmd/listen.go`

- [ ] **Step 1: Write `cmd/listen.go`**

```go
package cmd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"hookspot-cli/internal/config"
	"hookspot-cli/internal/proxy"
	"hookspot-cli/internal/ws"
)

var (
	listenPath  string
	forwardHost string
)

var listenCmd = &cobra.Command{
	Use:   "listen <port> [source...]",
	Short: "Forward hookspot webhook events to a local port",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		port := args[0]
		sources := args[1:]

		cfg := config.Load(v)
		if cfg.Token == "" {
			return fmt.Errorf("not logged in: run 'hookspot-cli login' or set HOOKSPOT_TOKEN")
		}
		if cfg.Project == "" {
			return fmt.Errorf("no active project: run 'hookspot-cli project use <project>' or set HOOKSPOT_PROJECT")
		}

		if len(sources) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Listening for all sources in project %s, forwarding to http://%s:%s%s\n", cfg.Project, forwardHost, port, listenPath)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Listening for sources %s in project %s, forwarding to http://%s:%s%s\n", strings.Join(sources, ", "), cfg.Project, forwardHost, port, listenPath)
		}

		query := url.Values{}
		query.Set("project", cfg.Project)
		for _, source := range sources {
			query.Add("source", source)
		}

		wsURL := strings.Replace(cfg.ServerURL, "http", "ws", 1) + "/cli/listen?" + query.Encode()

		wsClient := ws.New(wsURL, cfg.Token)
		forwarder := proxy.New("http://" + forwardHost + ":" + port)

		return wsClient.Listen(cmd.Context(), func(message []byte) error {
			resp, err := forwarder.Forward(cmd.Context(), listenPath, message, nil)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "forward error: %v\n", err)
				return nil
			}
			defer resp.Body.Close()

			fmt.Fprintf(cmd.OutOrStdout(), "forwarded event -> %d\n", resp.StatusCode)
			return nil
		})
	},
}

func init() {
	listenCmd.Flags().StringVar(&listenPath, "path", "/", "path to forward events to on the local server")
	listenCmd.Flags().StringVar(&forwardHost, "forward-host", "localhost", "host to forward events to (use host.docker.internal when running in Docker)")
	rootCmd.AddCommand(listenCmd)
}
```

- [ ] **Step 2: Build and smoke test**

```bash
make build
make run ARGS="listen --help"
```

Expected: builds with no errors; `listen --help` prints usage including the `--path` and `--forward-host` flags and `<port> [source...]` usage line.

- [ ] **Step 3: Verify the auth/project guard errors**

```bash
make run ARGS="listen 3000"
```

Expected: exits with error `Error: not logged in: run 'hookspot-cli login' or set HOOKSPOT_TOKEN` (no token configured yet).

- [ ] **Step 4: Commit**

```bash
git add cmd/listen.go
git commit -m "Add listen command wiring config, websocket client, and proxy"
```

---

### Task 9: Production Dockerfile, README, final verification

**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`
- Create: `README.md`

- [ ] **Step 1: Write `.dockerignore`**

```
.git
docs
bin
*.md
```

- [ ] **Step 2: Write `Dockerfile`**

```dockerfile
# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/hookspot-cli .

FROM alpine:3.20
RUN adduser -D -u 10001 hookspot
COPY --from=builder /out/hookspot-cli /usr/local/bin/hookspot-cli
USER hookspot
ENTRYPOINT ["hookspot-cli"]
```

- [ ] **Step 3: Build and run the production image**

```bash
docker build -t hookspot-cli:dev .
docker run --rm hookspot-cli:dev --help
docker run --rm hookspot-cli:dev version
```

Expected: image builds successfully; `--help` prints Cobra usage; `version` prints `hookspot-cli dev`.

- [ ] **Step 4: Write `README.md`**

```markdown
# hookspot-cli

A companion CLI for hookspot. Connects to your hookspot project over a
websocket and forwards incoming webhook events to a local host and port.

## Usage

### Authenticate

Generate a personal access token from the hookspot UI, then either:

```bash
hookspot-cli login
```

or set it via environment variable (recommended for Docker / CI):

```bash
export HOOKSPOT_TOKEN=hk_...
```

### Select a project

```bash
hookspot-cli project list
hookspot-cli project use <project-id>
```

Or set `HOOKSPOT_PROJECT=<project-id>`.

### Forward events to a local server

```bash
# All sources in the active project, forwarded to http://localhost:3000/webhooks
hookspot-cli listen 3000 --path /webhooks

# Only specific sources
hookspot-cli listen 3000 my-source --path /webhooks
```

## Configuration

Settings are resolved in this order: command-line flags, environment
variables (`HOOKSPOT_TOKEN`, `HOOKSPOT_PROJECT`, `HOOKSPOT_SERVER_URL`,
`HOOKSPOT_LOG_LEVEL`), the config file (`~/.config/hookspot-cli/config.toml`
by default, override with `--config`), then built-in defaults.

## Running in Docker

```bash
docker run --rm \
  -e HOOKSPOT_TOKEN=hk_... \
  -e HOOKSPOT_PROJECT=proj_... \
  --network host \
  hookspot-cli:dev listen 3000 --path /webhooks
```

## Development

All builds/tests run via Docker through the Makefile:

```bash
make build   # compile-check
make test    # run tests
make run ARGS="listen --help"
```
```

- [ ] **Step 5: Run the full test suite**

```bash
make build
make test
make vet
```

Expected: all three succeed with no errors.

- [ ] **Step 6: Commit**

```bash
git add Dockerfile .dockerignore README.md
git commit -m "Add production Dockerfile, README, and Docker usage docs"
```

---

### Task 10: docker-compose for running the built CLI

**Files:**
- Create: `docker-compose.yml`
- Modify: `README.md`

This task depends on the `Dockerfile` created in Task 9.

- [ ] **Step 1: Write `docker-compose.yml`**

```yaml
services:
  hookspot-cli:
    build: .
    image: hookspot-cli:dev
    environment:
      - HOOKSPOT_TOKEN
      - HOOKSPOT_PROJECT
      - HOOKSPOT_SERVER_URL
      - HOOKSPOT_LOG_LEVEL
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

- [ ] **Step 2: Build and smoke test via Compose**

```bash
docker compose build
docker compose run --rm hookspot-cli version
docker compose run --rm hookspot-cli --help
HOOKSPOT_TOKEN=test-token HOOKSPOT_PROJECT=proj_1 docker compose run --rm hookspot-cli listen 3000 --path /webhooks --forward-host host.docker.internal
```

Expected: `version` prints `hookspot-cli dev`; `--help` prints Cobra usage; the `listen` invocation prints `Listening for all sources in project proj_1, forwarding to http://host.docker.internal:3000/webhooks` and then fails with a websocket connection error (there is no real hookspot server yet) — that failure is expected at this stage.

- [ ] **Step 3: Append a "Docker Compose" section to `README.md`**

Add this section after the existing "Running in Docker" section:

```markdown
## Running with Docker Compose

A `docker-compose.yml` is provided for building and running `hookspot-cli`
without a local Go toolchain.

```bash
docker compose build

# Set credentials via env vars (or a .env file) before running
export HOOKSPOT_TOKEN=hk_...
export HOOKSPOT_PROJECT=proj_...

docker compose run --rm hookspot-cli login
docker compose run --rm hookspot-cli project list
docker compose run --rm hookspot-cli listen 3000 --path /webhooks --forward-host host.docker.internal
```

`host.docker.internal` (mapped via `extra_hosts` in `docker-compose.yml`)
lets the container reach services running on your host machine — use it as
the `--forward-host` when the target server (e.g. `localhost:3000`) runs
outside the container.
```

- [ ] **Step 4: Commit**

```bash
git add docker-compose.yml README.md
git commit -m "Add docker-compose for building and running hookspot-cli"
```

---

## Plan self-review notes

- **Spec coverage:** auth (Task 4), project commands (Task 5), listen command (Task 8), config/Docker precedence (Tasks 2-3), Dockerfile (Task 9), docker-compose (Task 10), proxy/ws placeholders (Tasks 6-7) — all spec sections have a corresponding task.
- **Type consistency:** `config.Config{Token, Project, ServerURL, LogLevel}` (Task 2) is used identically in Tasks 3-5 and 8. `proxy.New(targetBaseURL string) *Forwarder` / `Forward(ctx, path, body, headers)` (Task 6) matches its use in Task 8. `ws.New(url, token string) *Client` / `Listen(ctx, handler)` (Task 7) matches its use in Task 8. `api.Client.Me`/`ListProjects` (Tasks 4-5) match their use in `cmd/login.go` and `cmd/project.go`. `--forward-host` (added to Task 8's `listenCmd`, default `localhost`) is the value Task 10's docker-compose docs tell users to set to `host.docker.internal`.
- **No placeholders:** all code blocks are complete and compile together; out-of-scope items (browser-pairing login, real hookspot server endpoints/protocol) are explicitly deferred in the spec, not left as TODOs in code.
