# Human-readable project in `listen` output Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show `Organization/Project` instead of the raw project uid in `hookspot listen` output, resolved from the API at listen time.

**Architecture:** Config keeps storing the project uid (unchanged, backward compatible). The API client gains a `GetProject(uid)` method hitting `GET /cli/projects/{uid}`, returning a `Project` with a nested `Organization`. The `listen` command resolves the uid to names before connecting and aborts if resolution fails.

**Tech Stack:** Go, cobra (CLI), viper (config), standard `net/http`, `net/http/httptest` for tests.

## Global Constraints

- Module path is `hookspot` (imports like `hookspot/internal/api`).
- The websocket query MUST still send the project **uid** (`query.Set("project", cfg.Project)`). Do not change what is sent — only the displayed message.
- `HOOKSPOT_PROJECT` / config key `project` stays a uid. No config schema change.
- Follow existing code style: table-free, `fmt.Fprintf` to `cmd.OutOrStdout()`/`cmd.ErrOrStderr()`, errors wrapped with `%w`.
- Run tests with `go test ./...` from the repo root `/Users/bohdan/projects/my/hookspot-cli`.

---

### Task 1: Add `Organization` type and `GetProject` to the API client

**Files:**
- Modify: `internal/api/client.go` (the `Project` struct near line 43-47; add new method)
- Test: `internal/api/client_test.go` (add a new test function)

**Interfaces:**
- Consumes: existing unexported `(c *Client) get(ctx, path, out) error` helper.
- Produces:
  - `type Organization struct { UID string; Name string }` (JSON `uid`, `name`)
  - `Project` struct gains field `Organization Organization` (JSON `organization`)
  - `(c *Client) GetProject(ctx context.Context, uid string) (*Project, error)` — GET `/cli/projects/{uid}`

- [ ] **Step 1: Write the failing test**

Add to `internal/api/client_test.go`:

```go
func TestClient_GetProject_ReturnsProjectWithOrganization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cli/projects/proj_1" {
			t.Errorf("path = %q, want /cli/projects/proj_1", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
		}
		project := Project{
			UID:          "proj_1",
			Name:         "Production",
			Organization: Organization{UID: "org_1", Name: "Acme"},
		}
		if err := json.NewEncoder(w).Encode(project); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}))
	defer server.Close()

	client := New(server.URL, "test-key")

	project, err := client.GetProject(context.Background(), "proj_1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if project.Name != "Production" {
		t.Fatalf("Name = %q, want %q", project.Name, "Production")
	}
	if project.Organization.Name != "Acme" {
		t.Fatalf("Organization.Name = %q, want %q", project.Organization.Name, "Acme")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestClient_GetProject_ReturnsProjectWithOrganization`
Expected: FAIL — build error, `Organization` and `GetProject` undefined.

- [ ] **Step 3: Add the `Organization` type and field**

In `internal/api/client.go`, replace the `Project` struct block:

```go
// Organization represents the organization a project belongs to.
type Organization struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
}

// Project represents a hookspot project accessible to the current user.
type Project struct {
	UID          string       `json:"uid"`
	Name         string       `json:"name"`
	Organization Organization `json:"organization"`
}
```

- [ ] **Step 4: Add the `GetProject` method**

In `internal/api/client.go`, after `ListProjects`:

```go
// GetProject returns a single project by uid, including its organization.
func (c *Client) GetProject(ctx context.Context, uid string) (*Project, error) {
	var project Project
	if err := c.get(ctx, "/cli/projects/"+uid, &project); err != nil {
		return nil, err
	}
	return &project, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/`
Expected: PASS (new test plus existing `TestClient_ListProjects_ReturnsProjects` still pass).

- [ ] **Step 6: Commit**

```bash
git add internal/api/client.go internal/api/client_test.go
git commit -m "Add GetProject API method returning project with organization"
```

---

### Task 2: Display `Organization/Project` in `listen` output

**Files:**
- Modify: `cmd/listen.go` (the `RunE` body, after the `cfg.Project == ""` check around line 35-48)

**Interfaces:**
- Consumes: `api.New(baseURL, cliKey) *Client` and `(*Client).GetProject(ctx, uid) (*Project, error)` from Task 1; existing `requireServerURL()` and `config.Load(v)`.
- Produces: no new exported symbols; changes the two `Listening for ...` messages to show `org/name`.

- [ ] **Step 1: Add the `api` import**

In `cmd/listen.go`, add `"hookspot/internal/api"` to the import block (alongside `"hookspot/internal/config"`):

```go
	"hookspot/internal/api"
	"hookspot/internal/config"
	"hookspot/internal/proxy"
	"hookspot/internal/ws"
```

- [ ] **Step 2: Resolve the project after `requireServerURL()`**

In `cmd/listen.go`, the existing code is:

```go
		srvURL, err := requireServerURL()
		if err != nil {
			return err
		}

		if len(sources) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Listening for all sources in project %s, forwarding to http://%s:%s%s\n", cfg.Project, forwardHost, port, listenPath)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Listening for sources %s in project %s, forwarding to http://%s:%s%s\n", strings.Join(sources, ", "), cfg.Project, forwardHost, port, listenPath)
		}
```

Replace it with:

```go
		srvURL, err := requireServerURL()
		if err != nil {
			return err
		}

		project, err := api.New(srvURL, cfg.CLIKey).GetProject(cmd.Context(), cfg.Project)
		if err != nil {
			return fmt.Errorf("resolve project: %w", err)
		}
		projectLabel := project.Organization.Name + "/" + project.Name

		if len(sources) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Listening for all sources in project %s, forwarding to http://%s:%s%s\n", projectLabel, forwardHost, port, listenPath)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Listening for sources %s in project %s, forwarding to http://%s:%s%s\n", strings.Join(sources, ", "), projectLabel, forwardHost, port, listenPath)
		}
```

Note: the `query.Set("project", cfg.Project)` line below stays unchanged — it still sends the uid.

- [ ] **Step 3: Verify it builds and the suite passes**

Run: `go build ./... && go test ./...`
Expected: build succeeds; all tests PASS.

- [ ] **Step 4: Manual sanity check (vet only, no live server needed)**

Run: `go vet ./cmd/`
Expected: no output (clean).

- [ ] **Step 5: Commit**

```bash
git add cmd/listen.go
git commit -m "Show organization/project name in listen output"
```

---

## Self-Review notes

- **Spec coverage:** Task 1 covers the `internal/api/client.go` changes (Organization type, field, GetProject) and its test; Task 2 covers the `cmd/listen.go` display change and abort-on-error path. Both spec components are implemented.
- **Type consistency:** `Organization{UID, Name}`, `Project.Organization`, and `GetProject(ctx, uid)` are used identically across the test (Task 1) and the listen command (Task 2: `project.Organization.Name`, `project.Name`).
- **Backend dependency:** `GET /cli/projects/{uid}` is assumed to exist server-side per the spec; not implemented here.
