# Prefixed UIDs in the CLI

## Overview
- The Hookspot platform now exposes every entity uid with a type prefix (`usr_`, `org_`, `mem_`, `inv_`, `proj_`, `src_`, `dst_`, `conn_`, `req_`, `dlv_`, `att_`) followed by a 26-char alphanumeric nanoid. See `~/projects/my/hookspot/docs/plans/completed/20260924-prefixed-uids.md`; the platform work is committed on hookspot `main`, and prod is configured for Phase 1 (`emit_prefix: false`).
- The server keeps raw uids in the DB and accepts both forms everywhere: API paths, ingest URLs, the WebSocket topic `project:<uid>`, the `sources` join param, and `attempt_uid` in `delivery_response`. It broadcasts every delivery to both `project:<raw>` and `project:proj_<raw>`. So the CLI keeps working in every phase **without** protocol, config, or API changes.
- This plan fixes the three places where the platform change shows through to CLI users:
  1. `hookspot listen` looks up a delivery's `source_uid` in a name map built from the API at startup. A raw/prefixed mismatch prints "● unknown" and changes the source color.
  2. `hookspot project use` marks the saved project "(current)" by exact equality. A saved raw uid in `config.toml` never equals the prefixed uids that Phase 2 lists, so the marker and default selection are lost.
  3. The channel join now rejects projects the user can't access with `not_found`. The CLI reports that as an invalid server message and tells the user to retry and report it.
- Everything else treats uids as opaque strings passed back to the server. It is covered by the server's compatibility and needs no change (see Technical Details > No change needed).

## Context (from discovery)
- **Repo:** Go/cobra CLI. `make test` (`go test ./...`) and `make vet` run in Docker with Go 1.26.8; both pass on `main`. CI (`.github/workflows/ci.yml`) also runs release checks, `make npm-test`, and `scripts/smoke_test.sh`, none of which touch uids. There is no `AGENTS.md`/`CLAUDE.md`; conventions come from `docs/plans/completed/`.
- **uid comparisons (need a change):**
  - `internal/printer/printer.go:370-392`: `sourceToken(uid)` looks up `p.options.Sources[uid]` (falls back to "unknown") and colors with `sourceColor(uid)` (an FNV hash of the full uid string).
  - `cmd/listen.go:261-266`: `sourceNamesByUID` builds that map once from `ListProjectSources`. Reconnects reuse it, so a session keeps the uid form it saw at startup.
  - `cmd/project.go:175`: `selectProject` checks `project.UID == currentUID`, where `currentUID` is `store.SavedProject()` (the raw uid in existing configs).
- **Join rejection (needs a change):** `internal/ws/client.go:542-548` maps the join reasons `unauthorized`/`forbidden` to `SessionAuthentication` and every other rejection to `SessionProtocol`. `cmd/errors.go:102` presents `SessionProtocol` as "The server sent an invalid WebSocket message. Retry the command; if it continues, report the error." Neither kind is retryable, so `listen` exits (`cmd/listen.go:176-178`).
- **uid pass-through (no change):** the config store, `--project`, `project use <uid>`, browser login, `config migrate`, the WebSocket topic and join `sources`, `attempt_uid`, the frame topic filters, the printer's request id, and `project list`. See the No change needed table.
- **Nanoid alphabet** (`hookspot/config/config.exs:119-121`) is alphanumeric and has been since Nanoid was added (hookspot `6b56d68`). A raw uid never contains `_`, so trimming a known prefix is unambiguous.
- **Test fixtures** already use prefixed-looking uids (`src_stripe`, `proj_payments`, `att_1`, `req_1`). The config store tests use opaque strings (`global-project`). Neither needs to change for the pass-through paths.

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
- run tests after each change: `make test`
- maintain backward compatibility: raw uids in saved configs, `--project`, `project use <raw uid>`, and `config migrate` keep working unchanged
- YAGNI: no uid parsing/validation layer, no config migration, no shared uid package, no prefix registry

## Testing Strategy
- **unit tests**: required for every task (see Development Approach above)
- **form matrix**: each comparison is tested with raw↔raw, prefixed↔prefixed, raw map/saved + prefixed incoming, and prefixed map/saved + raw incoming. A uid with another type's prefix (`dst_…` against a source) still doesn't match.
- **join rejection**: a test WebSocket server answers the join with `not_found`, like the existing `unauthorized` test in `internal/ws/client_test.go`. `TestClientJoinRejectionReportsOnlyShortParsedDetails` (`internal/ws/session_test.go`) already guards that other reasons stay `SessionProtocol`. `superviseListen`'s logic is unchanged (only its doc comment gains not-found) and `Retryable()` is its only reconnect gate. `TestSuperviseListenStopsWhenProjectNotFoundAfterReconnect` (`cmd/listen_test.go`) covers that `listen` stops instead of reconnecting when the join after a reconnect is rejected with not-found.
- **e2e**: there is no e2e suite in this repo. Live verification against the platform's phases is listed in Post-Completion.

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview
- **Compare by the raw form, locally.** At each comparison site, `strings.TrimPrefix` removes the entity's own prefix before comparing. This mirrors the server's `PrefixedUID.raw/2`. Each site owns one unexported prefix constant (`src_` in `printer`, `proj_` in `cmd`). A shared package would hold one line of logic for two callers, so it was rejected (YAGNI).
- **Display stays as received.** The CLI prints and stores uids exactly as the server sends them. Only the lookup keys and the color hash use the raw form, so a source keeps one label and one color whichever form arrives.
- **Name the new rejection.** A `not_found` join reply becomes its own non-retryable `ws.SessionNotFound` kind, which `cmd/errors.go` explains in project terms. Retrying can't help: the project was deleted or the user lost access.
- **No config migration.** Existing raw `project` values keep working because the server accepts raw uids. They become prefixed naturally the next time `project use` or browser login saves an API-returned uid.
- **Independent of platform phase.** Trimming a prefix that isn't present is a no-op, and a server without the channel access check never sends `not_found`. So every change behaves correctly before Phase 1, in Phase 1, in the mixed-version window, and in Phase 2.

## Technical Details

### Printer source lookup (`internal/printer/printer.go`)
- Add `const sourceUIDPrefix = "src_"` and `func sourceKey(uid string) string { return strings.TrimPrefix(uid, sourceUIDPrefix) }`.
- `New` builds a new map keyed by `sourceKey(uid)` and stores it in a `sources` field on `Printer`, leaving `options` and the caller's map untouched (the `sourceLen` computation is unchanged). `sourceToken` looks up the name and computes `sourceColor` with `sourceKey(uid)`.
- `Options.Sources` keeps its meaning (uid → name, either form), so `cmd/listen.go` `sourceNamesByUID` is unchanged.
- Colors stay the same for real users: today's uids are raw, and trimming changes nothing for them. Without this change, every source's color would shift once Phase 2 sends prefixed uids. Only the existing test's expected color changes, because its fixture uses the prefixed-looking `src_stripe`.

### Saved-project marker (`cmd/project.go`)
- Add `const projectUIDPrefix = "proj_"` and a predicate `sameProjectUID(a, b string) bool` that compares both uids with `projectUIDPrefix` trimmed. `selectProject` uses it instead of `==`.
- No empty-string guard is needed: with no saved project `currentUID` is `""`, and no real project uid trims to `""`.

### `not_found` join rejection (`internal/ws/client.go`, `cmd/errors.go`)
- Append `SessionNotFound` to `SessionErrorKind`. In the join reply handling, a `not_found` reason (case-insensitive, like `unauthorized`/`forbidden`) returns `sessionError(SessionNotFound, false, err)`. `Retryable()` is unchanged, so the new kind isn't retried.
- `fatalErrorMessage` gets a `ws.SessionNotFound` case with the message `project not found: the WebSocket channel join was rejected` and the hint `The project may have been deleted or your access removed. Select another with 'hookspot project use', --project, or HOOKSPOT_ORGANIZATION_SLUG and HOOKSPOT_PROJECT_SLUG.` The hint names every way `listen` gets its project, because `--project` and the slug variables override the saved project. The existing no-project hint (`cmd/listen.go:69`) omits `--project` and is left unchanged.
- When users see it: `listen` resolves the project through the API first, so a stale config fails there with a 404. The join rejection shows up when a running `listen` reconnects (a network drop or a server deploy) after the project was deleted or the user's access was removed.

### No change needed (server compatibility covers it)
| Area | Why no change |
|---|---|
| Config store (`internal/config/store.go`) | `project` is opaque; the server resolves raw and prefixed. New saves store whatever the API returns (prefixed after Phase 2). No schema version bump. |
| `project use <uid>` / `--project` (`cmd/project.go`, `cmd/root.go`) | `endpoint.Segment` accepts `_`. `GetProject` works for raw and prefixed input in Phase 1+. A wrong-prefix uid gets a 404, falls back to the organization-name match, and fails with the existing "no projects match organization" message. |
| Browser login (`cmd/login_browser.go`) | Saves `result.Project.UID` as returned. |
| `config migrate` (`cmd/config.go:49-52`) | Validates the legacy project uid with `GetProject` and stores it as given; the server accepts raw uids. |
| `listen` topic (`cmd/listen.go:126`) | Built from the API's `project.UID`: raw before Phase 2, prefixed after. Phase 1+ nodes accept both join topics, and the dual broadcast reaches either one. |
| `listen` `sources` join param | Built from API source uids; the server matches raw and prefixed. |
| `delivery_response.attempt_uid` (`internal/ws/client.go:447`) | Echoed verbatim; the server accepts both forms. |
| WS frame topic filters (`internal/ws/client.go:386,412,530`) | Each Phoenix channel receives only its own topic, so the frame topic always equals the joined topic. |
| `internal/endpoint` | `Segment` and `validRelativePath` already allow `_`. |
| Printer request id, ingest URLs, `project list` | `request_uid` ends the line unpadded, the ingest `source.URL` is printed as received, and `project list` uses `tabwriter`. Longer uids just render longer. |
| `listen [source...]` | Matches sources by **name**, not uid. |
| Help text (`project use PROJECT_UID`, `--project "active hookspot project ID"`) | Form-agnostic placeholders. |

### Platform phase dependencies and release order
- None of the CLI changes depends on a platform phase, so all are safe to release now. The `not_found` case stays dormant until the platform's channel access check (its Task 6, hookspot `ec62868`) is deployed.
- **Recommended: release the CLI before platform Phase 2.** Without it, `listen` sessions that span the Phase 2 deploy print "● unknown" until restarted, and the first `project use` after Phase 2 loses the "(current)" marker for raw configs. Neither blocks delivery or forwarding, so Phase 2 does **not** hard-depend on this release, and older CLIs keep working.

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): tasks achievable within this codebase - code changes, tests, documentation updates
- **Post-Completion** (no checkboxes): items requiring external action - manual testing, changes in consuming projects, deployment configs, third-party verifications

## Implementation Steps

### Task 1: Match listen source labels by raw source uid

**Files:**
- Modify: `internal/printer/printer.go`
- Modify: `internal/printer/printer_test.go`

- [x] add `sourceUIDPrefix` and `sourceKey` in `internal/printer/printer.go`
- [x] re-key the sources map by `sourceKey` in `New`, and look up and color by `sourceKey(uid)` in `sourceToken`
- [x] write tests for the form matrix: map `{"stripe01": "stripe"}` with delivery `src_stripe01`, map `{"src_stripe01": "stripe"}` with delivery `stripe01`, and both same-form cases all print `● stripe`; a raw and a prefixed delivery for one source get the same color (unset `NO_COLOR` as `TestSourceTokensAlignAndUseStableColor` does, and assert the exact ANSI token)
- [x] write tests for edge cases: `dst_stripe01`, an unmapped uid, and an empty `source_uid` print `● unknown`
- [x] update the existing color assertion (`printer_test.go:392`, `sourceColor("src_stripe")`) to the raw-keyed hash
- [x] run tests - must pass before next task

### Task 2: Mark the saved project regardless of uid prefix

**Files:**
- Modify: `cmd/project.go`
- Modify: `cmd/project_test.go`

- [x] add `projectUIDPrefix` and `sameProjectUID` in `cmd/project.go`, and use `sameProjectUID` in `selectProject`
- [x] write table-driven tests (extend `TestSelectProjectMarksAndDefaultsSavedProject`): saved `payments` against listed `proj_payments`, saved `proj_payments` against listed `payments`, and both same-form cases mark "(current)" with default index 1
- [x] write tests for edge cases: an empty saved project and a saved `org_payments` mark nothing (default index 0)
- [x] run tests - must pass before next task

### Task 3: Explain a `not_found` channel join rejection

**Files:**
- Modify: `internal/ws/client.go`
- Modify: `internal/ws/client_test.go`
- Modify: `cmd/errors.go`
- Modify: `cmd/errors_test.go`
- Modify: `cmd/listen.go`

- [x] add `SessionNotFound` to `SessionErrorKind` and map a `not_found` join reason to it in `internal/ws/client.go`
- [x] add the `ws.SessionNotFound` message and hint to `fatalErrorMessage` in `cmd/errors.go`
- [x] add not-found to the fatal failures listed in the `superviseListen` doc comment (`cmd/listen.go:153-156`)
- [x] write a ws test next to the `unauthorized` one: a join reply `{"status":"error","response":{"reason":"not_found"}}` yields `SessionNotFound`, not connected and not retryable; add `{SessionNotFound, false}` to `TestSessionErrorRetryPolicy`
- [x] write an errors test (table case in `cmd/errors_test.go`): a `SessionNotFound` error wrapped as `listen` returns it (`fmt.Errorf("listen: %w", …)`) prints the new message and hint
- [x] run tests - must pass before next task

### Task 4: Verify acceptance criteria
- [x] verify both comparison sites handle raw and prefixed forms in either direction, and that a `not_found` join rejection stops `listen` with the new message and hint
- [x] re-grep `cmd/` and `internal/` for other uid equality/map lookups (`UID ==`, `[...UID]`, `SourceUID`, `AttemptUID`) and confirm the No change needed table is still accurate (only the `internal/ws/client.go` line refs shifted by one after Task 3; updated)
- [x] run full test suite: `make test`
- [x] run `make vet`

### Task 5: [Final] Update documentation
- [x] README.md "Log in and select a project": add one sentence that holds before and after Phase 2, e.g. "Project UIDs may be prefixed (`proj_…`); older unprefixed UIDs, including those in saved configs, keep working."
- [x] move this plan to `docs/plans/completed/` (skipped - the harness moves the plan after all phases finish)

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Release order**
- Ship a CLI release with Tasks 1–3 before the platform's Phase 2 deploy (recommended, not required; see Technical Details > Platform phase dependencies).

**Manual verification** (against the platform's dev stack, per its rollout phases)
- The dev stack emits prefixes by default (`emit_prefix` defaults to `true`), so it behaves like Phase 2. For Phase 1, set `config :app, App.Common.PrefixedUID, emit_prefix: false` in the dev config and recompile.
- Phase 1 server (`emit_prefix: false`): with a config holding a raw project uid, run `hookspot project list`, `project use` (the picker shows "(current)"), and `listen` with and without `--forward-to`; `listen` receives a delivery and reports its response. Also run `project use proj_<raw>`, which resolves the project.
- Phase 2 server: repeat the above. `project list` shows `proj_…`, the picker still marks the raw saved project "(current)", and `listen` labels sources correctly.
- Mixed window: start `listen` against Phase 1, switch the server to Phase 2 without restarting the CLI, send a webhook, and confirm the source label and color are unchanged (not "● unknown").
- Access check (platform Task 6 deployed): with `listen` running, remove the user from the project's organization, restart the server to force a reconnect, and confirm `listen` exits with the `project not found` message and hint.

**External system updates**
- None required. The platform plan's "Known cosmetic transient" note already refers to this release.
