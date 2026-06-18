# Human-readable project in `listen` output

## Problem

`HOOKSPOT_PROJECT` (config key `project`) stores a project **uid**. The `listen`
command echoes that uid back to the user:

```
Listening for all sources in project proj_1, forwarding to http://localhost:3000/
```

A raw uid is not human-readable. We want to show the organization and project
names instead:

```
Listening for all sources in project Organization/Project, forwarding to http://localhost:3000/
```

## Constraints

- The websocket query sends the project **uid** (`query.Set("project", cfg.Project)`
  in `cmd/listen.go`). The uid must remain the stored value.
- `HOOKSPOT_PROJECT` / `project` stays a uid — fully backward compatible. No
  config schema change.

## Design

### Resolution strategy

Resolve the uid to its organization + project names **at `listen` time** via the
API. Config storage is unchanged.

If resolution fails (network error, unknown uid), `listen` **aborts** before
connecting — the failure is fatal, not cosmetic.

### `internal/api/client.go`

Add a nested organization type and an `Organization` field to the existing
`Project` struct, plus a method to fetch a single project:

```go
type Organization struct {
    UID  string `json:"uid"`
    Name string `json:"name"`
}

type Project struct {
    UID          string       `json:"uid"`
    Name         string       `json:"name"`
    Organization Organization `json:"organization"`
}

// GetProject returns a single project by uid.
func (c *Client) GetProject(ctx context.Context, uid string) (*Project, error) {
    var project Project
    if err := c.get(ctx, "/cli/projects/"+uid, &project); err != nil {
        return nil, err
    }
    return &project, nil
}
```

The `Organization` field lives on the shared `Project` struct. `ListProjects`
leaves it empty if the list endpoint omits it; this is harmless.

### Backend contract (dependency)

`GET /cli/projects/{uid}` returns:

```json
{
  "uid": "proj_1",
  "name": "Project",
  "organization": { "uid": "org_1", "name": "Organization" }
}
```

Non-200 responses become an error via the existing `get` helper. This endpoint
is assumed to exist / be added server-side.

### `cmd/listen.go`

After the existing `cli_key` / `project` checks and `requireServerURL()`:

1. Build `api.New(srvURL, cfg.CLIKey)`.
2. Call `GetProject(cmd.Context(), cfg.Project)`.
3. On error: `return fmt.Errorf("resolve project: %w", err)` (aborts).
4. On success: build label `project.Organization.Name + "/" + project.Name` and
   substitute it for `cfg.Project` in the two `Listening for ...` messages.

The query still sends `cfg.Project` (the uid), unchanged.

Resulting output:

```
Listening for all sources in project Organization/Project, forwarding to http://localhost:3000/
```

## Testing

- `internal/api/client_test.go`: add a `GetProject` test using an httptest
  server asserting the request path `/cli/projects/proj_1` and decoding the
  nested `organization` object (uid + name).
- `listen` display: the only new command logic is the label construction and the
  abort-on-error path. No existing `cmd/listen` unit test exists today; the
  display is verified via the manual run path.

## Out of scope

- Changing the config schema or `project use` behavior.
- Changing what the websocket query sends.
- Caching resolved names.
