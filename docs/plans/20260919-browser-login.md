# Browser Login (`hookspot login` via browser, Hookdeck-style)

## Overview
- `hookspot login` starts a login attempt on the Hookspot backend, opens the browser, the signed-in user picks an organization + project and submits, and the CLI (which has been polling) receives the CLI key and the chosen project and saves both.
- After login the CLI knows which organization/project to use — no separate `hookspot project use` step.
- Manual key entry stays available as `hookspot login -i`; `HOOKSPOT_CLI_KEY` / `--cli-key` keep working unchanged (CI).
- Spans two repos: **hookspot** (`~/projects/my/hookspot`, Phoenix + Inertia/Vue) and **hookspot-cli** (this repo, Go/cobra). Backend tasks come first because the CLI depends on them.

## Context (from discovery)

**hookspot-cli**
- `cmd/login.go` — current flow: resolve key via `resolveCommandConfig` or prompt (`readLoginKey`), validate with `client.Me`, `store.SaveCLIKey`.
- `internal/config/store.go` — `Resolve` falls back flag → env → **saved record**, and `config.Config` carries no source indicator. So `cfg.CLIKey != ""` does NOT mean "user supplied a key"; login must inspect the flag/env directly.
- `internal/config/store.go` — `SaveCLIKey`, `SaveProject`, `Import` (key+project, but refuses when config exists). No "save both, overwrite" method.
- `internal/api/client.go` — only `get`; auth via `X-CLI-KEY`; redirects disabled; 1 MiB success body limit; `api.Error` carries status code.
- `cmd/project.go` — `projectDisplayName` (reuse for output); `projectAPI`/`projectPrompt` injection style to mirror.
- `internal/endpoint/endpoint.go` — `Base.API(relativePath)` builds URLs (honours deployment path prefix); `validRelativePath` accepts unpadded base64url characters.
- `cmd/` tests mostly use `runCommandProcess` (a real subprocess); browser-flow tests must be in-process unit tests or they would really launch `open`.
- The per-OS file split in `cmd/password_*.go` exists only because of differing syscall packages; not needed for `exec.Command`.
- `scripts/smoke.sh` only runs `version --json`; `cmd/logout.go` has no prompt-related wording. Nothing to change there.
- No browser-opener dependency in `go.mod`; project tracks third-party notices, so avoid adding one.
- Tests: `make test`, `make vet`, `make npm-test`.

**hookspot backend**
- All backend commands run inside Docker per its `AGENTS.md`: `docker-compose exec app mix test`, and `docker-compose exec app mix precommit` before committing. Frontend typecheck: `bun run --cwd assets typecheck`. `AGENTS.md` requires reading `docs/browser-automation.md` before UI work (Playwright against `https://hookspot.localhost`). Transactions use `Repo.transact/1`, not `Repo.transaction/1`.
- `lib/app_web/routers/cli_router.ex` — `/cli` scope piped through `:put_cli_user` (`AppWeb.CLIPlug`, looks up `users.cli_key` from `X-CLI-KEY`).
- `users.cli_key` — single plaintext 50-char Nanoid per user; also used by `AppWeb.CLISocket`. **Decision: browser flow returns this existing key** (no per-device keys). `AppWeb.IAM.UserSerializer` already emits `cli_key`.
- `require_authenticated_user` stores `user_return_to` for GETs; both `SigninController.create` and `OAuthController.complete/4` read it before `log_in_user` clears the session — so password and OAuth sign-in both bounce back to the CLI page. (`SignupController` not checked.)
- `require_onboarding` lives only in `DashboardRouter` pipelines; a route outside them is reachable by an org-less user directly.
- `put_user_organizations` assigns `organizations` **without projects** (`OrganizationSerializer` has no relations) — not usable for the picker. Use `App.Studio.ProjectQuery.by_user/1` joined/preloaded on `:organization` + `ProjectSerializer.to_map(expand: [:organization])`, exactly as `lib/app_web/controllers/cli/project_controller.ex` does; the same query validates the submission.
- `config/dev.exs` sets `url: [host: "hookspot.localhost"]` only (scheme defaults to http) while the CLI dev endpoint is `https://hookspot.localhost` — server-built absolute URLs would be wrong in dev. **Decision: server returns only tokens; CLI builds the browser URL from its own endpoint.**
- Conventions: models in `lib/app/iam/models`; operations in `lib/app/iam/operations/<model>/<op>.ex`, called directly (e.g. `App.IAM.UserToken.Create.call!/3`), not re-exported from `lib/app/iam.ex`; one test file per operation mirroring lib (`test/app/iam/operations/user_token/create_test.exs`). `App.IAM.UserToken` is the closest analogue (just `hash/1`, no changeset).
- Cron: `App.Core.DailyJob` only enqueues `App.Core.Daily.PruneJob` (`lib/app/core/jobs/schedule/daily/prune_job.ex`), which does the deleting.
- Layout for standalone auth-ish pages: `assets/js/dashboard/components/layouts/AuthLayout.vue` (used by `views/user/sign-in/New.vue`). The settings layout nests `AppLayout` and needs dashboard context — don't use it.
- Test support gaps: `test/support/conn_case.ex` has no login helper and there are no authenticated controller tests; `test/support/fixtures/` has only `iam_fixtures.ex` — no project fixture.
- Vitest: existing `*.test.ts` are all under `components/`; views depend on global `$t`/`$page`, so only extracted components are practically testable.
- No rate-limiting library present.

## Development Approach
- **testing approach**: Regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - write unit tests for new and modified functions
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- maintain backward compatibility: existing `X-CLI-KEY` auth, `/cli/me`, `/cli/projects*`, env/flag key login all unchanged
- YAGNI: no per-device keys, no deny button, no localhost callback server, no new Go dependencies

## Testing Strategy
- **backend**: ExUnit via `docker-compose exec app mix test` for operations, JSON endpoints and the browser controller (asserting Inertia component + props). Vitest for the extracted project-picker component.
- **CLI**: `go test ./...` using `httptest.Server` for API calls; in-process tests of `runBrowserLogin` with injected fakes (API, browser opener, store, poll interval).
- **browser verification**: no committed e2e suite in either repo; the full loop is verified with the backend's Playwright tooling (`docs/browser-automation.md`) in Task 10.

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

Poll-based device flow, same shape as Hookdeck's `POST /cli-auth` + poll:

```
CLI                                Backend                           Browser
 │ POST /cli/auth {device_name}      │                                  │
 │──────────────────────────────────▶│ create attempt (TTL 10 min)      │
 │◀── {browser_token, poll_token,    │                                  │
 │     code, expires_in}             │                                  │
 │ print code, open                  │                                  │
 │ <endpoint>/cli/login/<token> ─────┼─────────────────────────────────▶│ GET /cli/login/:token
 │                                   │   (signin + return_to if needed) │ shows code, device, project picker
 │ POST /cli/auth/poll {poll_token}  │                                  │
 │──────────────────────────────────▶│ {status: "pending"}              │
 │            …every 2s…             │◀──── POST /cli/login/:token {project_uid?}
 │                                   │ verify project ∈ user's projects │
 │                                   │ mark approved                    │ "Return to your terminal"
 │ POST /cli/auth/poll               │                                  │
 │──────────────────────────────────▶│ {status:"approved", user,        │
 │                                   │  project|null}; claim once       │
 │ save key + project, print result  │                                  │
```

Key design decisions:
- **Two separate secrets per attempt.** `browser_token` travels in a URL (history, screen-share); `poll_token` never leaves the CLI process. Knowing the browser URL must not let anyone fetch the key. Both stored as SHA-256 hashes.
- **CLI builds the browser URL** as `activeEndpoint.API("cli/login/" + browser_token)`. The server never supplies a URL, so there is nothing to origin-check and dev/prod endpoint config differences don't matter.
- **Confirmation code** (e.g. `ABCD-1234`) is printed by the CLI and shown on the browser page so the user can verify they're approving their own terminal. Display-only; not a secret, not typed in.
- **One-shot claim** via a single atomic `DELETE … RETURNING`; any later poll gets 404.
- **Project optional.** Submit without a project is allowed (user with no projects); CLI saves only the key and hints at `hookspot project use`.
- **Poll via POST body**, not query string, to keep `poll_token` out of access logs.
- **Headless fallback.** The URL is always printed; failure to launch a browser is a warning, not an error, and polling continues.
- **Server data is not trusted blindly:** empty key in an approved response is an error; `expires_in` is clamped.

## Technical Details

### Backend: `cli_login_attempts` table (App.Repo)
| column | type | notes |
|---|---|---|
| `id` | bigserial | |
| `browser_token_hash` | binary, not null, unique | sha256 of token |
| `poll_token_hash` | binary, not null, unique | sha256 of token |
| `code` | string, not null | `XXXX-XXXX`, uppercase unambiguous alphabet |
| `device_name` | string, null | from CLI (hostname), truncated to 100 chars in `Create`, display only |
| `user_id` | references users, on_delete: delete_all, null | set on approval |
| `project_id` | references projects, on_delete: nilify_all, null | set on approval, optional |
| `approved_at` | utc_datetime_usec, null | repo convention is usec everywhere |
| `expires_at` | utc_datetime_usec, not null | inserted_at + 10 min |
| timestamps | | |

Tokens: `:crypto.strong_rand_bytes(32) |> Base.url_encode64(padding: false)` — unpadded so they are valid path segments for the CLI's `endpoint.validRelativePath`.

### Backend: JSON API (no `X-CLI-KEY`; new unauthenticated `/cli/auth` scope through `:api`)
- `POST /cli/auth` body `{"device_name": "mbp"}` → `201`
  `{"browser_token": "…", "poll_token": "…", "code": "ABCD-1234", "expires_in": 600}`
- `POST /cli/auth/poll` body `{"poll_token": "…"}` →
  - `200 {"status": "pending"}`
  - `200 {"status": "approved", "user": <UserSerializer, includes cli_key>, "project": <ProjectSerializer expand: [:organization]> | null}` — attempt deleted atomically. The key is read from `user.cli_key`; no duplicate top-level field.
  - `404 {"status": "not_found"}` — unknown, expired, or already claimed

### Backend: operations semantics
- `Approve`: conditional `update_all` where `browser_token_hash == ^hash and is_nil(approved_at) and expires_at > ^now`; only `{1, _}` is success.
- `Claim`: `from(a in CLILoginAttempt, where: a.poll_token_hash == ^hash and not is_nil(a.approved_at) and a.expires_at > ^now, select: a) |> Repo.delete_all()`; only `{1, [attempt]}` is approved. On `{0, _}`, a second read distinguishes pending (row exists, unexpired) from not found.

### Backend: browser pages (scope `[:browser, :require_authenticated_user]`, outside dashboard pipelines)
- `GET /cli/login/:token` → look up by browser-token hash:
  - pending + unexpired → Inertia `cli/login/Show` with props `code`, `device_name`, `projects` (from `ProjectQuery.by_user` + `ProjectSerializer expand: [:organization]`)
  - approved by the current user but not yet claimed/expired (refresh, second tab) → `cli/login/Success`
  - otherwise → `cli/login/Expired`
- `POST /cli/login/:token` params `project_uid` (optional) → `Approve` → `cli/login/Success`; inaccessible project re-renders `Show` with an error; `:not_found` → `Expired`.

### CLI
- `api.Client.post(ctx, path, in, out)` alongside `get`, sharing status/body-limit/error handling. Accepts 200 and 201.
- `api.LoginAttempt{BrowserToken, PollToken, Code, ExpiresIn}`; `api.LoginResult{Status, User, Project *Project}`; `api.User` gains `CLIKey string \`json:"cli_key"\``.
- `(*Client).StartLogin(ctx, deviceName)`, `(*Client).PollLogin(ctx, pollToken)`.
- `internal/browser.Open(url string) error` — single file, `command(goos, url)` helper.
- `store.SaveLogin(key, projectUID string) error` — one `persist` writing both; empty `projectUID` preserves the existing saved project.
- `cmd/login.go` decision order:
  1. key explicitly supplied — `cmd.Flags().Changed("cli-key")` or non-empty `os.LookupEnv("HOOKSPOT_CLI_KEY")` (use the actual flag name from `cmd/root.go`) → existing validate-and-save path. A key that is merely *saved in config* does not count.
  2. `-i/--interactive` → existing prompt path
  3. otherwise → browser flow
- Browser flow output:
  ```
  Confirmation code: ABCD-1234
  Opening https://…/cli/login/… in your browser.
  If it doesn't open, visit the URL manually.
  Waiting for approval… (Ctrl+C to cancel)
  Logged in as user@example.com
  Active project set to Acme | Payments
  ```
- `StartLogin` 404/405 → `newCommandError("this Hookspot server does not support browser login", "Run 'hookspot login -i' or set HOOKSPOT_CLI_KEY.")`.
- Poll every 2s until approved, context cancelled, deadline (`min(ExpiresIn, 15 min)`, default 10 min if ≤0), or `PollLogin` 404 → "login attempt expired, run `hookspot login` again". Transient network errors during polling are retried until the deadline; other API errors abort.
- Approved response with empty `user.cli_key` → error, nothing saved. No extra `Me` call.

## What Goes Where
- **Implementation Steps**: Tasks 1–5 are in `~/projects/my/hookspot`; Tasks 6–9 are in this repo. All are code + tests.
- **Post-Completion**: deploy ordering, cross-platform manual runs, security follow-ups.

## Implementation Steps

### Task 1 (hookspot): `CLILoginAttempt` schema, migration, test fixtures

**Files:**
- Create: `priv/repo/migrations/<timestamp>_create_cli_login_attempts.exs`
- Create: `lib/app/iam/models/cli_login_attempt.ex`
- Create: `test/support/fixtures/studio_fixtures.ex`
- Create: `test/app/iam/models/cli_login_attempt_test.exs`

- [x] add migration for `cli_login_attempts` per the table above, unique indexes on both hash columns, index on `expires_at`
- [x] create `App.IAM.CLILoginAttempt` schema with `belongs_to :user`, `belongs_to :project`, and `hash_token/1` (sha256), modelled on `App.IAM.UserToken` (no changeset, no struct predicates)
- [x] add `App.StudioFixtures.project_fixture/1` (needed by Tasks 2–4; none exists today)
- [x] write tests for `hash_token/1` (deterministic, differs per token) and that `project_fixture` yields a project visible through `ProjectQuery.by_user`
- [x] run `docker-compose exec app mix test` - must pass before task 2

### Task 2 (hookspot): login-attempt operations (create / approve / claim)

**Files:**
- Create: `lib/app/iam/operations/cli_login_attempt/create.ex`
- Create: `lib/app/iam/operations/cli_login_attempt/approve.ex`
- Create: `lib/app/iam/operations/cli_login_attempt/claim.ex`
- Modify: `test/support/fixtures/iam_fixtures.ex` (add `cli_login_attempt_fixture`)
- Create: `test/app/iam/operations/cli_login_attempt/create_test.exs`
- Create: `test/app/iam/operations/cli_login_attempt/approve_test.exs`
- Create: `test/app/iam/operations/cli_login_attempt/claim_test.exs`

- [ ] `Create`: generate unpadded url-safe tokens and code, truncate `device_name` to 100 chars, store hashes, `expires_at = now + 10 min`; return the attempt plus raw tokens
- [ ] `Approve`: given browser token, user, optional project uid — resolve project via `ProjectQuery.by_user(user)`, then the conditional `update_all`; `{:error, :not_found | :project_not_found}` otherwise
- [ ] `Claim`: atomic `delete_all … select:` as specified; returns `:pending`, `{:approved, user, project}` (preloads after delete; `Repo.transact/1` if a transaction is needed), or `:not_found`
- [ ] add fetch-by-browser-token returning the attempt state needed by the show page (pending / approved-by-user / gone)
- [ ] write create tests (hashes stored, raw tokens not persisted, tokens unpadded, expiry set, long device_name truncated)
- [ ] write approve tests (with project, without project, foreign project rejected and attempt stays pending, expired rejected, double approve rejected)
- [ ] write claim tests (pending, approved-once-then-not-found, expired, unknown token)
- [ ] run `docker-compose exec app mix test` - must pass before task 3

### Task 3 (hookspot): JSON endpoints `POST /cli/auth` and `POST /cli/auth/poll`

**Files:**
- Modify: `lib/app_web/routers/cli_router.ex`
- Create: `lib/app_web/controllers/cli/login_controller.ex`
- Modify: `test/app_web/controllers/cli_controller_test.exs`

- [ ] add a second `/cli` scope piped through `[:api]` only (no `:put_cli_user`) with `post "/auth"` and `post "/auth/poll"`
- [ ] `create`: call `Create`, respond 201 with `browser_token`, `poll_token`, `code`, `expires_in`
- [ ] `poll`: map `Claim` results to 200-pending / 200-approved / 404, reusing `UserSerializer` and `ProjectSerializer` (`expand: [:organization]`)
- [ ] write tests for `POST /cli/auth` (201 shape, works without `X-CLI-KEY`, overlong/missing device_name handled)
- [ ] write tests for poll (pending, approved returns user with `cli_key` + project, approved without project returns `project: null`, second poll 404, bad/missing token 404)
- [ ] verify existing `/cli/me` and `/cli/projects` tests still pass (still require `X-CLI-KEY`)
- [ ] run `docker-compose exec app mix test` - must pass before task 4

### Task 4 (hookspot): browser approval page (controller + Inertia views)

**Files:**
- Modify: `lib/app_web/routers/cli_router.ex` (new browser scope `[:browser, :require_authenticated_user]`; `get/post "/cli/login/:token"`)
- Create: `lib/app_web/controllers/cli/login_page_controller.ex`
- Create: `assets/js/dashboard/components/cli/LoginProjectPicker.vue`
- Create: `assets/js/dashboard/components/cli/LoginProjectPicker.test.ts`
- Create: `assets/js/dashboard/views/cli/login/Show.vue`
- Create: `assets/js/dashboard/views/cli/login/Success.vue`
- Create: `assets/js/dashboard/views/cli/login/Expired.vue`
- Modify: `assets/js/dashboard/locales/en_US.json`
- Modify: `assets/js/dashboard/types.ts` (if page prop types live there)
- Modify: `test/support/conn_case.ex`
- Create: `test/app_web/controllers/cli/login_page_controller_test.exs`

- [ ] read `docs/browser-automation.md` (required by backend `AGENTS.md` before UI work)
- [ ] add `log_in_user/2` to `AppWeb.ConnCase` using `App.IAM.UserToken.Create.call!(user, "session")` + `Phoenix.ConnTest.init_test_session(user_token: raw_token)` (no such helper exists yet)
- [ ] `show`: render `Show` / `Success` / `Expired` per the state rules in Technical Details; `projects` prop from `ProjectQuery.by_user` + `ProjectSerializer expand: [:organization]`
- [ ] `approve`: call `Approve` with `current_user` and optional `project_uid`; render `Success` (with chosen org/project names), re-render `Show` with error for an inaccessible project, `Expired` on `:not_found`
- [ ] `LoginProjectPicker.vue`: props-only component (no `$page`/`$t` globals): organization select derived by grouping `projects` by organization → project select; preselect when exactly one; emits selected `project_uid`
- [ ] `Show.vue` on `AuthLayout.vue`: confirmation code and device name shown prominently, the picker, submit button; empty state (no projects) still allows submit
- [ ] `Success.vue` / `Expired.vue` on `AuthLayout.vue`: "Return to your terminal" / "This login link has expired, run `hookspot login` again"; add locale strings
- [ ] write controller tests: unauthenticated GET redirects to `/users/signin` and session `user_return_to` is `/cli/login/<token>`; authenticated GET renders `cli/login/Show` with code + projects; org-less user reaches the page; unknown/expired token renders `Expired`; approved-unclaimed token renders `Success`
- [ ] write controller tests: POST with own project approves; POST without project approves; POST with another user's project is rejected and attempt stays pending; POST on expired attempt renders `Expired`
- [ ] write Vitest tests for `LoginProjectPicker` (grouping, dependent select, single-project preselect, empty list)
- [ ] run `docker-compose exec app mix test`, the assets Vitest command, and `bun run --cwd assets typecheck` - must pass before task 5

### Task 5 (hookspot): purge expired login attempts

**Files:**
- Modify: `lib/app/core/jobs/schedule/daily/prune_job.ex`
- Modify: the existing PruneJob test (create next to it if absent)

- [ ] delete `cli_login_attempts` where `expires_at < now()` inside `App.Core.Daily.PruneJob`, alongside its existing pruning (DailyJob itself only enqueues — leave it untouched)
- [ ] write test: expired rows removed, live rows kept
- [ ] run `docker-compose exec app mix precommit` (compile warnings-as-errors, format, unused deps, tests) - must pass before task 6

### Task 6 (hookspot-cli): `post` support and login API methods

**Files:**
- Modify: `internal/api/client.go`
- Modify: `internal/api/client_test.go`

- [x] refactor `get` into a shared `do(ctx, method, path, in, out)` with `get`/`post` wrappers; `post` JSON-encodes the body, sets `Content-Type: application/json`, accepts 200 and 201; `X-CLI-KEY` still only sent when non-empty
- [x] add `CLIKey` to `api.User`; add `LoginAttempt`, `LoginResult` types and `StartLogin(ctx, deviceName)`, `PollLogin(ctx, pollToken)`
- [x] write tests for `StartLogin` (request body, no `X-CLI-KEY` header when key empty, 201 decode, deployment path prefix preserved, 404 → `*api.Error`)
- [x] write tests for `PollLogin` (pending, approved with project, approved with `project: null`, 404 → `*api.Error` with status 404, body-size limit)
- [x] check `safeDisplayText`/JSON output paths don't start leaking `User.CLIKey` (e.g. any place that prints or marshals `api.User`)
- [x] confirm existing `get` tests are unchanged and pass
- [x] run `make test` - must pass before task 7

### Task 7 (hookspot-cli): browser opener

**Files:**
- Create: `internal/browser/browser.go`
- Create: `internal/browser/browser_test.go`

- [x] `command(goos, url string) (name string, args []string, ok bool)`: `open` (darwin), `xdg-open` (linux), `rundll32 url.dll,FileProtocolHandler` (windows), `ok=false` otherwise — no build tags needed
- [x] `Open(url string) error`: reject non-`http(s)` URLs, then `exec.Command` with the URL as a single argument (no shell); `Start` and don't wait
- [x] write table tests for `command` across all three GOOS values plus unsupported
- [x] write tests for scheme rejection (`file:`, `javascript:`, empty)
- [x] run `make test` and `make vet` - must pass before task 8

### Task 8 (hookspot-cli): `Store.SaveLogin`

**Files:**
- Modify: `internal/config/store.go`
- Modify: `internal/config/store_test.go`

- [x] add `SaveLogin(key, projectUID string) error`: single `persist` setting `CLIKey` and, when `projectUID` is non-empty, `Project`
- [x] write tests: new config gets both; existing config is overwritten with both; empty project preserves the previously saved project; persist failure leaves in-memory record unchanged
- [x] run `make test` - must pass before task 9

### Task 9 (hookspot-cli): browser flow in `hookspot login`

**Files:**
- Modify: `cmd/login.go`
- Create: `cmd/login_browser.go`
- Create: `cmd/login_test.go`

- [x] add `-i/--interactive` flag; restructure `RunE` into the decision order from Technical Details, detecting an explicit key via `cmd.Flags().Changed(...)` / `os.LookupEnv("HOOKSPOT_CLI_KEY")` — NOT via `cfg.CLIKey`; update `Short` to "Authenticate hookspot via the browser" and reword the "Pass a CLI key when prompted…" hint at `cmd/login.go:35`
- [x] implement `runBrowserLogin(ctx, deps)` in `cmd/login_browser.go`; `deps` carries `loginAPI` interface, `openBrowser func(string) error`, a store interface (`SaveLogin`), poll interval, in/out writers — no package-level `store` access, so it is testable in-process
- [x] device name from `os.Hostname()` (empty on error); browser URL from `activeEndpoint.API("cli/login/" + BrowserToken)`, erroring if that returns nil (malformed token)
- [x] map `StartLogin` 404/405 to the "server does not support browser login" command error
- [x] print code + URL (through `safeDisplayText`), attempt `openBrowser`, on failure print the manual-open notice and continue
- [x] poll loop: 2s ticker; stop on ctx cancel (Ctrl+C returns promptly), clamped deadline, or `PollLogin` 404 → `newCommandError("login attempt expired", "Run 'hookspot login' again.")`; retry transient network errors, abort on other API errors
- [x] on approval: reject empty `User.CLIKey`; `SaveLogin(key, project UID or "")`; print `Logged in as …`, then `Active project set to <projectDisplayName>` or a hint to run `hookspot project use`
- [x] write tests: happy path with project; happy path without project (hint shown, old project preserved); **saved key in config still starts the browser flow**
- [x] write tests: browser open failure still succeeds; malformed browser token aborts before opening; unsupported server (404 on start); expiry (404 on poll); empty key rejected; oversized `ExpiresIn` clamped; context cancellation returns promptly; transient poll error retried
- [x] write tests: `--cli-key`/env path and `-i` path behave exactly as before
- [x] run `make test` and `make vet` - must pass before task 10

### Task 10: Verify acceptance criteria
- [ ] `hookspot login` → browser → org/project select → submit → CLI has key + project, against the local backend (`https://hookspot.localhost`), driving the browser side with the Playwright tooling from `docs/browser-automation.md`
- [ ] same run on a machine/config that already has a saved key — browser flow still starts
- [ ] `hookspot login -i`, `HOOKSPOT_CLI_KEY=… hookspot login`, and `--cli-key` still work
- [ ] submit without project logs in and leaves project unset/preserved
- [ ] confirmation code shown in terminal matches the browser page; refresh after approve shows Success, not Expired
- [ ] second poll after claim returns 404; expired attempt shows `Expired` page and CLI error
- [ ] signed-out start: password sign-in and OAuth sign-in both return to `/cli/login/:token`
- [ ] headless run (`PATH` without `open`/`xdg-open`) prints URL and completes after manual approval
- [ ] run full suites: `docker-compose exec app mix precommit` (hookspot), `make test && make vet && make npm-test` (hookspot-cli)

### Task 11: [Final] Update documentation
- [ ] update `README.md` and `npm/README.md` login sections (browser default, `-i`, env key for CI)
- [ ] update `docs/releases/INSTALL.md` if it describes first login
- [ ] update hookspot's CLI key settings page copy (`assets/js/dashboard/views/user/settings/api-key/Show.vue`) to mention `hookspot login` as the primary path, if it currently instructs pasting the key
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion
*Items requiring manual intervention or external systems - informational only*

**Deploy ordering**
- Backend (Tasks 1–5) must be deployed before releasing the CLI version with browser login. Against an older backend the new CLI reports "server does not support browser login" and points to `login -i`.

**Manual verification**
- Full loop on Linux (incl. SSH session with no display) and Windows; macOS is covered in Task 10.
- New-user path: start `hookspot login` signed out and go through **signup** instead of signin — `SignupController`'s handling of `user_return_to` was not checked; if it drops the return path, the user just re-opens the printed URL.

**Security follow-ups (decide before public launch)**
- `POST /cli/auth` is unauthenticated and inserts a row per call. TTL + daily prune bound the damage, but there is no rate-limiting library in the backend; consider edge/WAF rate limiting or adding one.
- All devices share the single `users.cli_key`; rotating it logs out every machine. Per-device keys were explicitly deferred.
- Residual device-code phishing risk: the display-only confirmation code does not stop an attacker who convinces a victim to open the attacker's login URL and approve it — they would receive the victim's shared `cli_key`. The approval page should word the prompt clearly ("Only approve if you just ran `hookspot login` and the code matches").
- Pre-existing, unrelated: `ScopeSerializer` embeds `UserSerializer`, so every Inertia page already ships the user's `cli_key` to the browser.
