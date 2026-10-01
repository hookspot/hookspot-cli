# Local Webhook Fixes (listen, forwarding, delivery, login)

## Overview
- **Scope: fixes and improvements to functionality that already exists.** Nothing here is a new feature: no source creation from the CLI, no `hookspot sources` command, no new write API.
- **What changes for the user:**
  - **`listen` tells the truth about readiness.**
    - `Ready` is printed only after the channel join, which is when the server starts delivering.
    - A reconnect says how long the CLI was offline and that requests in the gap weren't delivered, with a link to retry them in the dashboard.
    - Today "Waiting for requests..." appears before the join, so a first webhook can be silently lost as `cli_offline`.
  - **`listen stripe` receives only stripe deliveries.** Today every CLI on a project receives every source's deliveries and forwards them to its own target.
  - **Webhooks are no longer dropped at ingest because of their `Accept` header.** Today a `406` is returned before the request is stored.
  - **`hookspot login` works for brand-new accounts.** Sign-up in the browser returns to the CLI approval page, and a user without a project can create the first organization and project there. Today a new user gets stuck.
  - **Forwarding fixes:**
    - `--forward-to 3000` means `localhost:3000`. Today it's parsed as a hostname and fails at the first delivery.
    - The local hop ignores `HTTP_PROXY`, which Docker injects into containers.
    - A root destination path no longer adds a stray trailing slash, but a typed one is kept.
    - A 3xx response shows its `Location`.
    - "Connection refused" inside a container explains `localhost`.
    - A 404 at the root URL suggests adding the route to `--forward-to`.
  - **Clearer `listen` output and errors:**
    - The banner names the project and warns about disabled sources.
    - "no matching routes found" becomes an actionable message.
    - A named source with no route is reported instead of silently skipped.
    - A case-only name mismatch suggests the right name.
  - **Backend robustness:** an unknown project on `/cli/projects/:uid/sources` returns 404 (was 500), and a concurrent duplicate source name in the dashboard returns a validation error (was 500).
- **Spans two repos:** **hookspot** (`~/projects/my/hookspot`, Phoenix + Inertia/Vue) and **hookspot-cli** (this repo). Backend tasks come first.
- **Evidence:** `docs/research/20260924-local-webhook-needs.md` (§4a and §4d).
- **Follow-ups and dropped features:** `docs/backlog/`.

## Context (from discovery)

**hookspot-cli**
- **`cmd/listen.go`:**
  - `printListenInfoWithReplay` prints `Waiting for requests...` before `superviseListen` connects and joins.
  - `superviseListen` prints `connection lost: …; reconnecting in 2s...` to stderr (`:192`), but never says it reconnected.
  - `resolveSources` matches names exactly and silently skips a named source with no routes (`continue`). It errors with `no matching routes found` or `source %q is not present in project …`, asserted in `cmd/listen_test.go:95-122` and `cmd/project_test.go:742`.
  - The banner doesn't name the project.
  - `forwardSession` handles forwarding and replay.
- **`internal/ws/client.go`:**
  - No hook for "joined".
  - Forwards serially in the read loop (`:383-393`).
  - Delivery payloads carry `source_uid` and `request_uid`.
- **`internal/proxy/proxy.go`:**
  - `New` prefixes `http://` and calls `endpoint.Parse`, which trims trailing slashes. So `--forward-to 3000` becomes host `3000`.
  - `DestinationURL` concatenates the base URL and the destination path, so destination `/` plus `--forward-to …/webhooks` gives `…/webhooks/`.
  - The client uses the default transport, so `HTTP(S)_PROXY` applies to the local hop. The timeout is 30 s, and redirects aren't followed.
- **`internal/printer/printer.go`:**
  - The connection-refused hint is at `:333-334`.
  - A redirect prints `→ 308` without the `Location`.
- **`internal/api/client.go`:** `Base.API` builds URLs on the app origin. The browser-login URL is built this way, and so is the dashboard (`/:org/:project/requests`).
- **`cmd/login_browser.go`:** 10-minute default wait. The timeout message doesn't mention new accounts.
- **Tests:** `make test` / `make vet` run in Docker.
- **Prerequisite landed:** the CLI prefixed-uids plan is complete (`docs/plans/completed/20260924-prefixed-uids.md`, `7c5418f`…`1e6e5ea`). It touched `internal/printer` and `cmd/project_test.go`, so line numbers cited here may have shifted.

**hookspot backend**
- **Ingest:** `lib/ingest/router.ex` pipes the catch-all through `plug :accepts, ["json"]`. `Accept: text/plain`, `application/xml`, and `text/html` get **406** and aren't stored (reproduced on Phoenix 1.8.13, research T3).
- **Delivery:**
  - `DeliverJob` delivers only if `CLIPresence.connected?`, which is true once a CLI has **joined** with `[]` or that source. Otherwise the delivery is `cli_offline` and never retried.
  - It broadcasts to every CLI on `project:proj_…`. `ProjectChannel` has no `intercept`.
- **CLI sources endpoint:** `cli/project/source_controller.ex` `put_project` assigns `nil` for an unknown or inaccessible project, which gives a 500. `ProjectController.show` returns `404 {"status":"not_found"}`.
- **Source validation:** `SourceValidator.name` uses only `unsafe_validate_unique`, with no `unique_constraint` on `sources_project_id_name_index`. A concurrent duplicate raises `Ecto.ConstraintError`, which gives a 500.
- **Sign-up and CLI login (research note 02, code-read, not run):**
  - `SignupController.create` doesn't log in; it redirects to "check your email".
  - OAuth returns via `UserAuth.return_to`.
  - `Approve.call` requires a project (`approve.ex:52`), and the approval page only offers a picker (`cli/login/Show.vue`).
  - Onboarding redirects to the dashboard, not back to `/cli/login/:token`.
- **Prerequisite landed:** the backend prefixed-uids plan is complete (`hookspot/docs/plans/completed/20260924-prefixed-uids.md`, `d834cc2`), and channel joins are access-checked. A follow-up server change removes the legacy fallbacks: only prefixed uids are accepted and emitted, and deliveries are broadcast on `project:proj_…` only. Build the delivery filter on that code.
- ⚠️ **Uncommitted changes that aren't this plan's** are in the backend working tree (`docker-compose.yml`, `lib/ingest/models/request.ex`). Never stage or commit them with this plan's tasks: stage exact paths only.
- **Conventions (`AGENTS.md`):**
  - Run commands through `docker-compose exec app …`; run `mix precommit` before committing.
  - Use `Repo.transact/1`.
  - Tests follow `.agents/skills/elixir-tests/SKILL.md`.
  - UI work follows `docs/browser-automation.md` (Playwright), with Vitest for extracted components.

## Development Approach
- **testing approach**: Regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change: backend `docker-compose exec app mix test` (plus `mix precommit` before each backend commit); CLI `make test` and `make vet`
- **maintain backward compatibility.** The only behavior changes are the documented fixes:
  - root path without a stray trailing slash;
  - `Ready` after the join;
  - new notices and hints on stderr;
  - bare-port parsing.
- **YAGNI:** no new commands, flags, environment variables, or API routes beyond the approval-page form action.

## Testing Strategy
- **backend:**
  - ExUnit for the ingest `Accept` fix, the unique constraint, the sources-endpoint 404, the channel filter (`Phoenix.ChannelTest`), and the sign-up round trip and first-project action.
  - Vitest for the extracted first-project component.
  - Playwright walkthrough of sign-up from a CLI login.
- **CLI:**
  - `go test ./...`: a fake `ws` listener for readiness and reconnect output; `httptest` servers for forwarding (including an `HTTP_PROXY` set to a failing proxy); printer tests for the hints; `listen` tests for the new messages.
  - Each new line's stream (stdout or stderr) is asserted.
- **e2e:** no committed suite in either repo. Compose, WSL2, and real-provider checks are in Post-Completion.

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

`listen` after the fixes (stdout and stderr interleaved as a terminal shows them):
```
$ hookspot listen stripe --forward-to 3000/webhooks/stripe
Listening in Acme | Payments on 1 source • 1 route                            (stdout)

stripe
│  Requests to → https://in.hookspot.io/src_8f2k…
└─ Forwards to → http://localhost:3000/webhooks/stripe

Connecting…
Ready. Waiting for requests (Ctrl-C to quit)
…
connection lost: …; reconnecting in 2s...                                     (stderr, unchanged)
Reconnected after 14s offline. Requests that arrived meanwhile were not       (stderr)
delivered; retry them from https://app.hookspot.io/acme/payments/requests
```

### Key design decisions
1. **Readiness is a contract.**
   - `Ready. Waiting for requests (Ctrl-C to quit)` is printed only after the channel join reply, which is when presence starts and deliveries flow.
   - CI harnesses and agents can wait for this line.
   - Every rejoin prints the offline duration and the dashboard requests URL, because deliveries in the gap became `cli_offline`.
2. **Filter deliveries on the server, not the client.** `ProjectChannel` intercepts `delivery` and pushes only to sockets that joined with `[]` or with that source. Older CLIs benefit without an upgrade.
3. **Fix sign-up rather than add guest mode.** `hookspot login` → sign up in the browser → back to the approval page → create the first organization and project if none exist → approve. No guest mode, and no sign-up in the terminal (research §4c).
4. **The stream rule for lines this plan adds:**
   - stdout carries the banner, `Connecting…`, and `Ready`.
   - stderr carries `Reconnected …`, warnings (disabled source, route-less source), and forwarding hints (redirect, Docker, root 404).
   - Existing lines keep their stream.
5. **Forwarding fixes stay inside `--forward-to`.** No new flags: a bare port is parsed, the typed trailing slash is honored, and the proxy is bypassed for the local hop only.

## Technical Details

### Readiness and reconnects
- `ws.Client` gets an `OnJoined func()` option, called after a successful `phx_join` reply and never on a rejected join.
- `listen` prints the banner (now ending with `Connecting…`) before connecting. `superviseListen` then prints:
  - on the first join: `Ready. Waiting for requests (Ctrl-C to quit)` (stdout), plus `↵ replay last request` when replay is enabled, moved from the banner;
  - on every later join: `Reconnected after <d> offline. Requests that arrived meanwhile were not delivered; retry them from <URL>` (stderr). `<d>` is measured from the first failed session of the gap. `<URL>` is `activeEndpoint.API(org.Slug + "/" + project.Slug + "/requests")`, with the slugs through `endpoint.Segment`.
- The existing `connection lost …` notice and the initial-connect failure behavior are unchanged.

### Delivery filter (backend)
- `ProjectChannel` adds `intercept ["delivery"]`.
- `handle_out("delivery", payload, socket)` pushes when `socket.assigns.sources == []`, or when `payload.source_uid` is among the socket's sources. Both are prefixed. Otherwise it does `{:noreply, socket}`.
- Presence semantics don't change: a delivery is `sent` if any CLI listens to its source.
- Intercepting disables fastlane for `delivery`. The cost is one extra channel-process message per joined CLI, which is acceptable at CLI scale.

### Ingest `Accept` fix (backend)
- Remove `plug :accepts, ["json"]` from the ingest catch-all pipeline in `lib/ingest/router.ex`. Keep it only on JSON-only routes, if any (e.g. `/up`).
- Webhooks with any `Accept` value are stored and answered as today: 200, or the source's custom response.

### Sign-up from `hookspot login` (backend)
- **Walkthrough first:** use Playwright to walk password and OAuth sign-up from `/cli/login/:token`. Record where `user_return_to` is lost, and whether an unconfirmed user can reach the approval page.
- **Return path:** keep `user_return_to` through sign-up → email confirmation → sign-in, so the user lands on `/cli/login/:token`.
- **First project:**
  - When `projects_for(user)` is empty, `cli/login/Show.vue` renders an extracted `LoginFirstProject` component (organization name, project name).
  - It posts to `POST /cli/login/:token/project`.
  - That action runs `OnboardingForm.submit` and then `Approve.call` with the new project uid, in one `Repo.transact`. Errors re-render the form.
- **Expiry:**
  - The Expired page says `If you just created your account, run hookspot login again.`
  - The CLI's login timeout message says the same.
  - The attempt TTL is unchanged.

### Forwarding (`internal/proxy`, `internal/printer`, `cmd/listen.go`)
- **Bare port:** before parsing, `proxy.New` maps `^[0-9]+(/.*)?$` to `http://localhost:<value>` (`3000`, `3000/webhooks`). Port range validation stays in `endpoint.Parse`.
- **No proxy on the local hop:** the forwarder's own `http.Transport` is a clone of the default with `Proxy: nil`. Redirects are still not followed, and the 30 s timeout is unchanged. `HTTPS_PROXY` still applies to the API and WebSocket connections.
- **Trailing slash:**
  - `proxy.New` records whether the raw `--forward-to` path ended in `/`.
  - When the delivery path is exactly `/` and the base has a path, the result is the base path, plus `/` only if it was typed.
  - Examples:
    - `…/webhooks/stripe` + `/` → `…/webhooks/stripe`
    - `localhost:8000/webhooks/` + `/` → `…/webhooks/` (Django `APPEND_SLASH`, FastAPI)
    - bare host + `/` → `…/` (unchanged)
    - `/api` + `/hooks` → `/api/hooks` (unchanged)
- **Redirects:** a 3xx response prints `Location: <url>` and the hint `webhook senders don't follow redirects; point --forward-to at the final URL` (stderr).
- **Docker-aware "connection refused":**
  - When `/.dockerenv` or `/run/.containerenv` exists and the target host is `localhost`/`127.0.0.1`/`::1`, the hint says `inside a container, localhost is the container itself; use the service name (http://app:3000) or host.docker.internal`.
  - The container check is injectable for tests.
- **Root 404 hint:** the first 404 or 405 from a forward whose final URL path is `/` prints a one-time hint (stderr): `http://localhost:3000/ returned 404. If your webhook route is elsewhere, include it in --forward-to, e.g. --forward-to 3000/webhooks`. It isn't printed in print-only mode.

### `listen` messages (`cmd/listen.go`)
- **Banner:** starts with `Listening in <Org | Project> on N sources • M routes`.
- **Disabled source:** a selected source with `active == false` gets `⚠ stripe is disabled: requests to it are rejected. Enable it in the dashboard.` (stderr).
- **Named source with no routes:** instead of a silent skip, `⚠ shopify has no route and is skipped. Add one in the dashboard.` (stderr). If every named source is skipped, the command errors (next item).
- **No sources with routes:**
  - Replaces `no matching routes found`.
  - With no args: `no sources with routes in <Org | Project>`, with the hint `Add a route in the dashboard: <new-route URL>`.
  - With names: `none of the named sources has a route`, plus the same hint.
- **Unknown name:** `source "Stripe" is not present in <project>` gains `; did you mean "stripe"?` when exactly one case-insensitive match exists.

### Backend robustness
- **`put_project`** in `cli/project/source_controller.ex` returns `404 {"status":"not_found"}` and halts when the project isn't found or accessible, matching `ProjectController.show`.
- **`SourceValidator.name`** adds `unique_constraint(:name, name: :sources_project_id_name_index)`, so a concurrent duplicate becomes a changeset error in the dashboard.

### Edge-case matrix
| # | Case | Behavior |
|---|---|---|
| 1 | Webhook sent before the CLI joined (startup) | `Ready` appears only after the join; harnesses wait for it |
| 2 | Connection drops mid-session | `connection lost …`, then `Reconnected after Ns offline …` with the dashboard URL |
| 3 | Initial connect keeps failing | unchanged: bounded attempts, exit 1 |
| 4 | Join rejected (e.g. no access) | no `Ready`; the existing fatal error |
| 5 | `listen stripe` while another CLI listens to github | receives only stripe deliveries |
| 6 | Old CLI joined with `[]` | receives everything (unchanged) |
| 7 | Raw (unprefixed) or `conn_` uid in the topic or join sources | not found: the join is rejected |
| 8 | Provider sends `Accept: text/plain` / `application/xml` / `text/html` | stored and relayed (was 406) |
| 9 | Brand-new user runs `hookspot login` and signs up (password) | after confirmation and sign-in, lands on the approval page; creates the first project; the CLI saves the key and project |
| 10 | Same via OAuth | returns to the approval page (unchanged), then creates the first project |
| 11 | Email confirmation outlasts the 10-minute attempt | Expired page and CLI timeout both say to run `hookspot login` again |
| 12 | Existing user with projects | picker unchanged; the first-project form never shows |
| 13 | Invalid organization or project name in the first-project form | form re-rendered with errors; the attempt stays pending |
| 14 | `--forward-to 3000` / `3000/webhooks` / `99999` | `http://localhost:3000[/webhooks]` / port error |
| 15 | `HTTP_PROXY` set (Docker-injected), forwarding to `http://app:3000` | local hop bypasses the proxy |
| 16 | Destination `/` + `--forward-to localhost:3000/webhooks` | no trailing slash |
| 17 | Destination `/` + `--forward-to localhost:8000/webhooks/` | typed slash kept |
| 18 | Local app answers 3xx | `Location` and hint printed |
| 19 | `listen` in a container with `--forward-to localhost:…`, refused | Docker-aware hint |
| 20 | Root forward returns 404/405 | one-time hint; not repeated; not in print-only mode |
| 21 | Stale local config pointing at another project | the banner names the project |
| 22 | Selected source disabled | warning on stderr |
| 23 | Named source without routes | warning; skipped; error if all are skipped |
| 24 | `listen Stripe` when only `stripe` exists | error with "did you mean" |
| 25 | Unknown or inaccessible project on `/cli/projects/:uid/sources` | 404 (was 500) |
| 26 | Two dashboard users create the same source name at once | validation error (was 500) |

### Out of scope
- **Dropped new features:**
  - source auto-creation in `listen`;
  - the `hookspot sources` TUI and subcommands;
  - `/cli` write endpoints and pagination;
  - dashboard links in the banner and on request lines;
  - the next-steps block;
  - name validation and the creation guard.

  Their designs are in the research report, and the items are tracked in `docs/backlog/`.
- **Follow-ups:** `docs/backlog/` (requests retry, grace window before `cli_offline`, fan-out correctness, …).
- **Skipped** (research §4c): guest mode, sign-up in the terminal, `--json`, an MCP server, and the rest of that list.

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): tasks achievable within this codebase - code changes, tests, documentation updates
- **Post-Completion** (no checkboxes): items requiring external action - manual testing, changes in consuming projects, deployment configs, third-party verifications

## Implementation Steps

### Task 1: Backend: ingest accepts any `Accept` header

**Files:**
- Modify: `hookspot/lib/ingest/router.ex`
- Modify/Create: `hookspot/test/ingest/…` (router or request controller test)

- [ ] remove `plug :accepts, ["json"]` from the ingest catch-all pipeline; keep it only on JSON-only routes
- [ ] write tests: `Accept: text/plain`, `application/xml`, `text/html` are stored and answered 200 (or the custom response); JSON and `*/*` unchanged; unknown source still 405
- [ ] run `docker-compose exec app mix test` - must pass before next task

### Task 2: Backend: sources-endpoint 404 and unique-name constraint

**Files:**
- Modify: `hookspot/lib/app_web/controllers/cli/project/source_controller.ex`
- Modify: `hookspot/lib/app/studio/validators/source_validator.ex`
- Create: `hookspot/test/app_web/controllers/cli/project/source_controller_test.exs`
- Modify/Create: `hookspot/test/app/studio/validators/source_validator_test.exs`

- [ ] `put_project` returns `404 {"status":"not_found"}` and halts when the project is missing or inaccessible
- [ ] add `unique_constraint(:name, name: :sources_project_id_name_index)` to `SourceValidator.name`
- [ ] write tests: unknown and other-org project → 404 (was 500); an accessible project → the unchanged array
- [ ] write tests: a duplicate inserted past `unsafe_validate_unique` (simulated race) returns `{:error, changeset}` with a `name` error
- [ ] run tests - must pass before next task

### Task 3: Backend: filter channel deliveries by joined sources

**Files:**
- Modify: `hookspot/lib/app_web/channels/project_channel.ex`
- Modify/Create: `hookspot/test/app_web/channels/project_channel_test.exs`

- [ ] add `intercept ["delivery"]` and `handle_out/3` per Technical Details
- [ ] write tests: a socket joined with `[src_a]` gets an `a` delivery but not a `b` delivery; a socket joined with `[]` gets both
- [ ] run tests and `mix precommit` - must pass before next task

### Task 4: Backend: sign-up return path and expiry copy

**Files:**
- Modify (per walkthrough): `hookspot/lib/app_web/controllers/user/signup_controller.ex`, `…/confirmation_controller.ex`, `hookspot/lib/app_web/user_auth.ex`
- Modify: `hookspot/assets/js/dashboard/views/cli/login/Expired.vue`
- Modify: the matching controller tests

- [ ] walk the flow with Playwright (`docs/browser-automation.md`): start a CLI login, sign up with password (confirm via the dev mailbox) and with OAuth; record findings in this plan (➕/⚠️)
- [ ] fix the lost return path so the user lands on `/cli/login/:token` after confirmation and sign-in
- [ ] update the Expired page copy (`If you just created your account, run hookspot login again.`)
- [ ] write tests: sign-up → confirm → sign-in redirects to `/cli/login/:token`; OAuth sign-up still returns there; the Expired page shows the new copy
- [ ] run tests - must pass before next task

### Task 5: Backend: create the first organization and project on the approval page

**Files:**
- Modify: `hookspot/lib/app_web/controllers/cli/login_page_controller.ex`
- Modify: `hookspot/lib/app_web/routers/cli_router.ex`
- Modify: `hookspot/assets/js/dashboard/views/cli/login/Show.vue`
- Create: `hookspot/assets/js/dashboard/components/cli/LoginFirstProject.vue` (+ `.test.ts`)
- Modify: `hookspot/test/app_web/controllers/cli/login_page_controller_test.exs`

- [ ] add `POST /cli/login/:token/project` (browser scope): in one `Repo.transact`, `OnboardingForm.submit` then `Approve.call` with the new project uid; errors re-render the form
- [ ] render `LoginFirstProject` in `Show.vue` when `projects` is empty
- [ ] write controller tests: no projects → org + project created and the attempt approved with them; invalid names → errors, attempt still pending; expired token → Expired page; a user with projects never sees the form
- [ ] write Vitest for `LoginFirstProject`; run the typecheck; Playwright check of the page
- [ ] run tests and `mix precommit` - must pass before next task

### Task 6: CLI: readiness and reconnect notice

**Files:**
- Modify: `internal/ws/client.go`, `internal/ws/client_test.go`
- Modify: `cmd/listen.go`, `cmd/listen_test.go`

- [x] add `OnJoined` to `ws.Client` (called after a successful join reply only)
- [x] banner ends with `Connecting…`; `superviseListen` prints `Ready …` (and the replay hint) on the first join, and `Reconnected after <d> offline …` with the dashboard requests URL on later joins
- [x] write tests: `OnJoined` fires once per successful join and never on a rejected join
- [x] write tests (fake listener): `Ready` only after the join (stdout); the reconnect line with a measured duration and escaped URL (stderr); initial-connect failure unchanged
- [x] run tests - must pass before next task
- ⚠️ `OnJoined` is `func() error`: a failed `Ready` write ends `listen` as a fatal handler error, like other output failures.
- ➕ Connection output lives in `connectionNotices` (`joined`, `lost`); `superviseListen` takes it instead of `errOut`.
- ➕ The banner keeps its `Requests ───` divider; `Connecting…` replaces `Waiting for requests...`. `printListenInfoWithReplay` became `printListenInfo`; the replay hint prints after the `Ready` line.
- ➕ A slug that fails `endpoint.Segment` makes the reconnect line say `retry them from the dashboard`.
- ➕ E2E: `listen` against a fake server prints `Ready` once after `Connecting…`, then `Reconnected …` with the URL on stderr; a rejected join prints no `Ready`.

### Task 7: CLI: `listen` banner and source messages

**Files:**
- Modify: `cmd/listen.go`
- Modify: `cmd/listen_test.go` (incl. `:95-122`)
- Modify: `cmd/project_test.go` (`:742`)

- [x] banner starts with `Listening in <Org | Project> …`; disabled-source warning; route-less named source warning instead of a silent skip
- [x] replace `no matching routes found` with the two messages and the dashboard hint; add "did you mean" for a single case-insensitive match
- [x] write tests: the project in the banner; disabled and route-less warnings on stderr; the all-skipped error; no-args error text and hint URL; "did you mean" only for exactly one fold match
- [x] update the existing assertions that expected the old messages or the silent skip
- [x] run tests - must pass before next task
- ➕ The new-route URL is `<app>/<org>/<project>/routes/new` (built like the requests URL); with an unsafe slug the hint is `Add a route in the dashboard.`
- ➕ Source warnings go to stderr before the banner. An unknown name errors before any warning; route-less warnings still print before the all-skipped error.
- ➕ The unknown-name error keeps its `in project acme/payments` slug label; only the `; did you mean …?` suffix is new.

### Task 8: CLI: forwarding fixes

**Files:**
- Modify: `internal/proxy/proxy.go`, `internal/proxy/proxy_test.go`
- Modify: `internal/printer/printer.go`, `internal/printer/printer_test.go`
- Modify: `cmd/listen.go` (`forwardSession` root 404 hint), `cmd/listen_test.go`

- [ ] bare-port mapping; typed-trailing-slash flag and the root-path rule; a dedicated transport with `Proxy: nil`
- [ ] printer: 3xx `Location` + hint; Docker-aware refused hint; `forwardSession`: one-time root 404/405 hint
- [ ] write proxy tests: `3000`, `3000/webhooks`, `99999`; the trailing-slash table (incl. query kept, `?`/`#` rejected); with `HTTP_PROXY` pointing at a failing proxy, a forward to an `httptest` server still succeeds
- [ ] write printer and listen tests: 308 shows `Location`; refused hint differs inside and outside a container; the root hint prints once, not for non-root URLs, and never in print-only mode
- [ ] run tests - must pass before next task

### Task 9: CLI: login timeout hint for new accounts

**Files:**
- Modify: `cmd/login_browser.go`, `cmd/login_test.go`

- [ ] add `If you just created your account, run 'hookspot login' again.` to the timeout message
- [ ] write a test for the timeout text
- [ ] run tests - must pass before next task

### Task 10: Verify acceptance criteria
- [ ] locally: `listen stripe --forward-to 3000/webhooks/stripe` shows the project, `Connecting…`, then `Ready` after the join; killing and restoring the backend shows `Reconnected after …`
- [ ] a webhook with `Accept: text/plain` reaches the CLI; a second CLI listening to another source doesn't receive it
- [ ] a 3xx and a root 404 show their hints; `--forward-to 3000` works
- [ ] a brand-new account completes `hookspot login` with the first-project form
- [ ] full suites: backend `mix precommit` and `bun run --cwd assets test`; CLI `make test`, `make vet`, `make npm-test`

### Task 11: [Final] Update documentation
- [ ] README "Listen and forward":
  - the `Ready` line (wait for it in scripts) and the reconnect notice;
  - bare port;
  - the trailing-slash rule;
  - redirect, Docker, and root-404 hints;
  - which new lines go to stderr.
- [ ] README "Delivery":
  - deliveries go only to CLIs listening to that source;
  - requests that arrive while no CLI is joined aren't retried (use the dashboard);
  - only 5xx/408/429 are retried, up to 5 times;
  - history is kept 14 days;
  - several listeners on the same source each receive every delivery.
- [ ] README "Signatures": the body and provider headers are forwarded unchanged; verify with the secret the provider shows for the Hookspot URL; replays older than about 5 minutes fail timestamp checks; providers that need a URL handshake (Slack, Meta, Zoom, …) aren't supported yet.
- [ ] README "Log in": sign-up works from `hookspot login`; new accounts create their first project on the approval page.
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Release order**
- Both prefixed-uids plans have landed.
- Deploy backend Tasks 1–5, then release the CLI.
- Task 1 (ingest 406) and Task 3 (delivery filter) can ship as independent backend hotfixes. Older CLIs benefit immediately.

**Manual verification**
- **Docker Compose:**
  - `listen` with `tty: true` and without it: the `Ready` line appears after the join.
  - A Docker-injected `HTTP_PROXY` doesn't break forwarding to `http://app:3000`.
  - Refusing `localhost` inside the container shows the Docker hint.
- **WSL2:** `hookspot login` (including sign-up) and forwarding to an app inside WSL2 and to one on the Windows host.
- **A real provider** that sends a non-JSON `Accept` header, if one is known, or `curl -H 'Accept: application/xml'`.
- **Brand-new account:**
  - Password and OAuth sign-up from `hookspot login`.
  - An email confirmation slower than 10 minutes (both expiry messages appear, and the rerun succeeds).
- **Frameworks:** Django or FastAPI with `--forward-to 8000/webhooks/` (typed slash kept).

**External system updates**
- Backend commits for Tasks 1–5 (`mix precommit` before each).
