# Terminal Redesign: Cards Output, Full-Screen Listen, Request Tools

## Overview
- Redesign every terminal screen in the **Cards** style chosen on the design canvas (https://claude.ai/artifact/FJSZr22vurzVRuZPSg7XWR, boards "B · Cards" and "D · Full-screen").
- **`hookspot listen` opens full-screen by default when stdin and stdout are both terminals:**
  - a request list with a detail pane;
  - a filter;
  - replay, and retry that waits for the local server to come back;
  - a **Sources page** (`s`) listing every source, route and destination with live stats and numbered copy mode.
- **`listen --stream`, a terminal stdout with a non-terminal stdin, and piped stdout all print Cards as a stream.** In a terminal the stream has a pinned status line, plus a `›` prompt for request commands when stdin is a terminal.
- **New request tools in both modes:**
  - replay any request by number, with a result diff;
  - copy as cURL;
  - export a request as a fixture;
  - send a test event.
- **Other commands get the Cards look:** login, logout, version, project list/use, and errors. The hand-written project picker is replaced.
- **Routes display** as the route name if set, otherwise the destination path.
- **Prerequisite:** the CLI tasks (6+) of `docs/plans/20260924-local-webhook-setup.md` land first. This plan restyles their final messages (`Ready`, `Reconnected after …`, project in the banner, source warnings, forwarding hints) instead of redoing them. Task 1 re-reads the code once they land.
- **Two repos:** one backend change in **hookspot** (`route_uid` in the delivery payload); everything else is this repo.

## Context (from discovery)
*Re-baselined in Task 1, after the prerequisite's CLI tasks landed.*
- **Status:**
  - The prerequisite's CLI Tasks 6–9 (readiness and reconnect, banner and source messages, forwarding fixes, login timeout hint) are merged on this branch. Its Tasks 10–11 (manual verification, README) are still open and out of scope here.
  - Tasks 2, 4 and 15 of this plan were pulled forward and are done. **Task 15 landed before Tasks 5–14**, so login, logout, version, project list and errors already print Cards (`internal/cards/commands.go`); only `listen` and the project picker still use the old output.
- **Output today:**
  - `internal/cards` (Tasks 4 and 15): palette, badges, `Terminal`, `Width` (`DefaultWidth` 100), `Sanitize`/`Line`, the command cards and their goldens. `go.mod` pins `charm.land/lipgloss/v2` and `github.com/charmbracelet/x/exp/golden`.
  - `internal/printer/printer.go`:
    - inspect and forward blocks, hand-written ANSI, display limits, the sensitive-header list (`sensitiveHeader`), `transportHint`; escaping already goes through `cards`;
    - `forwardNotice` writes the Docker `localhost` and 3xx `Location` hints, and `PrintNotice` the root-404 hint, to `Options.Notices` (stderr); the container check is the injectable `container` field;
    - `SupportsColor`, now used only by `cmd/listen.go`.
  - `cmd/listen.go`:
    - `resolveSources` writes the source warnings to stderr before the banner;
    - `printListenInfo` writes the banner to stdout, ending with `Connecting…`;
    - `connectionNotices` (`joined`, hooked to `ws.Client.OnJoined`, and `lost`) writes the connection states; `superviseListen` takes it instead of `errOut`;
    - `forwardSession` (with the one-time root-404 hint), the single-entry `replayCache`, and the line-based `startReplayInput`;
    - `routeLabel` returns the route name or `""`.
  - `cmd/errors.go`: `HandleError` renders `cards.Error` when stderr is a terminal, plain text otherwise. `safeDisplayText` is a `cards.Line` wrapper kept only for `cmd/listen.go`.
  - `cmd/project_picker*.go`: ~540 lines of raw-mode picker, including Windows console setup.
- **Messages the prerequisite added** (`<…>` values pass through `cards.Line`):
  - stdout:
    - the banner starts with `Listening in <Org | Project> on N source(s) • M route(s)` and ends with `Connecting…`;
    - on the first join, `Ready. Waiting for requests (Ctrl-C to quit)`, then `↵ replay last request` when replay is on.
  - stderr:
    - on each later join, `Reconnected after <d> offline. Requests that arrived meanwhile were not delivered; retry them from <URL>`. `<URL>` is the dashboard requests page, or `the dashboard` when a slug isn't a safe path segment;
    - before the banner, `⚠ <source> is disabled: requests to it are rejected. Enable it in the dashboard.` and `⚠ <source> has no route and is skipped. Add one in the dashboard.`;
    - after a 3xx with a `Location` header, `Location: <url>` and `webhook senders don't follow redirects; point --forward-to at the final URL`;
    - after a refused `localhost` target inside a container, `inside a container, localhost is the container itself; use the service name (http://app:3000) or host.docker.internal`, besides the stdout `└─ target … is not reachable` line;
    - once per run, after a 404 or 405 at the bare `--forward-to` root, `<URL> returned <status>. If your webhook route is elsewhere, include it in --forward-to, e.g. --forward-to <URL>webhooks`; never in inspect mode.
  - errors through `HandleError`: `no sources with routes in <Org | Project>` and `none of the named sources has a route`, both hinting `Add a route in the dashboard: <URL>`; the unknown-source error's `; did you mean "<match>"?` suffix; the login-expiry hint `If you just created your account, run 'hookspot login' again.`
  - unchanged: `connection lost: <err>; reconnecting in 2s...` (stderr).
- **`ws` and `proxy`:** `ws.Client.OnJoined func() error` runs after each accepted join, before that session's deliveries; its error is a fatal `SessionHandler` failure. `internal/proxy` (bare port, typed trailing slash, `Proxy: nil` transport) prints nothing itself.
- **The `Ready` contract:** plain mode keeps `Ready. Waiting for requests (Ctrl-C to quit)\n` byte-identical on stdout, printed once after the first join; harnesses wait for it. The `↵` hint line after it isn't part of the contract.
- **Data:**
  - `api.Source{UID, Name, URL, Active, Routes}`.
  - `api.Route{UID, Name *string, Destination{Path}, DisplayName}`. The API's `display_name` is `name` or `"source -> path"`, so the CLI computes its own label.
  - The CLI sources endpoint returns delivering routes only.
  - `ws.Delivery` carries the source and path but no route, and `request_uid` isn't validated.
- **Backend:** since Task 2, `deliver/2` (`lib/ingest/jobs/deliveries/deliver_job.ex`) sends `route_uid`; older servers omit it.
- **Signals:**
  - `main.go` wraps the command in `signal.NotifyContext`. The first SIGINT cancels; the watcher then calls `stop()`, so a second SIGINT kills the process.
  - In raw mode, Ctrl-C produces no SIGINT.
- **Release:**
  - `make release-check` only validates the goreleaser config. `make release-snapshot` builds all six targets and needs a clean tree.
  - The archives ship `docs/releases/THIRD_PARTY_NOTICES.txt`, which has a documented refresh procedure.
- **`make dev`** runs `listen` under air.
- **Tests run in Docker:** `make test`, `make vet`. Command tests run the binary as a subprocess (`runCommandProcess`).
- **Guarantees to keep:**
  - output stays in order, with one whole block per delivery;
  - control characters in server data are escaped;
  - `NO_COLOR` is respected;
  - sensitive headers stay redacted unless `--show-sensitive-headers`;
  - `--max-body-lines`, `--max-headers` and `--max-value-chars` still apply;
  - write failures surface through the websocket handler error, and a failed `Ready` write through `OnJoined`;
  - the first Ctrl-C is graceful and the second forces exit.

## Development Approach
- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - prefer command-level tests (the cobra command against fake API and websocket servers, as in `cmd/*_test.go` and `internal/ws/session_test.go`) and golden files
  - unit tests only for edge cases: eviction, percentiles, escaping, route matching, diffing, file names
  - cover success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- piped `listen` output stays usable by scripts (plain text, no color, no cursor control)

## Testing Strategy
- **Golden files:**
  - Use `golden.RequireEqual` from `github.com/charmbracelet/x/exp/golden` everywhere, with its `-update` flag. Never define a second `-update` flag.
  - Each Cards component gets one no-color golden at width 80. One color golden pins the palette. Width 120 is added only where the layout changes with width.
  - lipgloss v2 always emits ANSI and leaves downsampling to the writer, so no-color goldens pass output through `ansi.Strip` (or a NoTTY `colorprofile` writer) first.
- **teatest** (`github.com/charmbracelet/x/exp/teatest/v2`):
  - golden only `FinalModel(t).View().Content`, ANSI-stripped;
  - check streamed cards with `teatest.WaitFor` on ANSI-stripped output, never by goldening the raw output stream;
  - inject the clock, toast duration and dial interval so frames are deterministic.
- **Subprocess command tests:**
  - mask timestamps and latencies before comparing;
  - end `listen` runs with an invalid delivery payload after the expected output. It's fatal right away with no reconnect, as `TestSuperviseListenDoesNotReconnectAfterInvalidDelivery` shows.
- **Backend:** ExUnit through `docker-compose exec app mix test` and `docker-compose exec app mix precommit`.

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview
- **Packages:**
  - `internal/cards`: lipgloss renderers for every Cards component, as pure functions of data and width. It also owns escaping and the plain stream writer.
  - `internal/session`: the UI-free core. It holds the numbered, bounded request history; forwarding and replay (moved from `cmd/listen.go`); per-route stats; the cURL, fixture and test-event builders; and the ordered event stream.
  - `internal/tui`: bubbletea models for full-screen listen, the Sources page, the terminal stream, and the project picker.
- **`listen` modes:**

  | stdin | stdout | flag | mode |
  |---|---|---|---|
  | terminal | terminal | — | full-screen (alt screen) |
  | terminal | terminal | `--stream` | inline stream: cards above, status line + `›` prompt below |
  | not a terminal | terminal | any | inline stream with `tea.WithInput(nil)`: status line, no prompt |
  | any | not a terminal | any | plain writer: no color, width 100; line commands only when stdin is a terminal (e.g. `listen \| tee log`) |

  Programs always get `tea.WithInput(cmd.InOrStdin())` or `nil`, plus `tea.WithOutput(cmd.OutOrStdout())`.
- **`session.Sink` contract:**
  - `Emit(Event) error` is called in record order. An event is one of:
    - `ConnState`: `Connecting`, `Ready`, `ConnectionLost{err, retryIn}`, or `Reconnected{offline}`. Plain mode keeps today's wording and streams: `Connecting…` and `Ready …` on stdout; the other two on stderr, `Reconnected` with the dashboard requests URL;
    - `Notice`: the disabled and route-less source warnings (from `RunE`, before the banner) and the one-time root-404 hint. Plain mode writes them to stderr with today's wording;
    - `Entry`: a request, replay or test-event result. Its card holds the per-request hints: the 3xx `Location` and redirect hint in the HTTP failure card, the Docker `localhost` hint in the transport failure card.
  - Each event carries an immutable snapshot: the entry, its route's stats, the totals, and the numbers history just evicted.
  - Every `Emit` goes through the session under its emit mutex, including connection states and notices from `superviseListen` and `RunE`. **Nothing writes to `os.Stdout`/`os.Stderr` directly while a program runs.**
- **Ordering and locking:**
  - The websocket handler still forwards synchronously.
  - It then takes the session's emit mutex, records the entry (assigning its number under the mutex), and calls `sink.Emit` before releasing it. Numbers and output order therefore always match, even when a replay races a live delivery.
  - The plain writer's `Emit` writes and returns the write error, so it reaches the handler error as today.
  - The bubbletea sinks use the program's single ordered message queue.
    - The inline sink sends cards with `Program.Println` and state with `Program.Send`.
    - The full-screen sink uses `Program.Send` only, because `Println` prints nothing on the alt screen.
    - They never use `tea.Println` as a returned `Cmd`, because Cmds run in parallel goroutines.
  - `Program.Println` blocks forever once the program stops reading, so the sink runs it in a goroutine and returns `ErrClosed` as soon as `Run` has returned:

    ```go
    go func() { p.Println(card); close(done) }()
    select {
    case <-done:
        return nil
    case <-finished: // closed when Run returns for any reason
        return ErrClosed
    }
    ```

    `Program.Send` already stops after exit.
  - Sinks and `Update` never call back into the session. UI-started work (replay, cURL, export, test event, stats reads) runs only inside `tea.Cmd`s, so the event loop never waits on a session lock.
- **Shutdown and Ctrl-C:**
  - Programs run with `tea.WithoutSignalHandler()`, so `main.go` stays the only signal handler (SIGTERM, `kill -INT`, non-raw input).
  - In raw mode:
    - the first `ctrl+c` key cancels the listen context (the same cancel the signal uses) and shows "stopping…";
    - once `superviseListen` returns and the session is closed, the model quits;
    - a second `ctrl+c` calls `Program.Kill()` (which restores the terminal), then exits with code 130 through an injectable exit function.
  - `q` in full-screen uses the same sequence as the first `ctrl+c`.
  - **The program can exit before the listener** (startup or TTY error, a panic, `Kill`).
    - Whenever `Run` returns, `tui.Program` cancels the listen context first and closes `finished` second, so an in-flight `Handle` never sees `ErrClosed` while the context is live.
    - `listen` then waits for `superviseListen`, and `Handle` treats `ErrClosed` as non-fatal once the context is cancelled.
    - When both fail, `listen` returns `Run`'s error.
  - Map to nil only `ErrInterrupted`, and `ErrProgramKilled` when it isn't `ErrProgramPanic`. A panic stays an error.
- **Route attribution:**
  - Use `route_uid` when it names a known route.
  - Otherwise (older servers, or a route the sources list doesn't include) use the source's first route whose destination path equals the delivery path.
  - Otherwise the request is "unmatched".

## Technical Details
- **History:**
  - Numbered from #1 for each `listen` run.
  - Capped at 1000 entries and 64 MiB of bodies, evicting the oldest. Naming an evicted number says so.
  - Each entry holds: number, delivery, route UID, forwarded-to URL, outcome (status, headers, body, latency, or transport failure), received time, `ReplayOf`, and `Test`.
- **Stats per route:**
  - count, ok (2xx) and failed;
  - latency p50/p95/max: transport failures other than timeouts are excluded, and timeouts count as their duration;
  - per-minute counts for the last 15 minutes;
  - the last entry.
  - Local replays are excluded from route stats; test events count.
  - Unmatched requests count only in the totals.
  - "Since" means since `listen` started.
- **Replay:**
  - It's local only. It creates a new entry with `ReplayOf` and never changes the delivery's status in Hookspot; the detail pane says so.
  - `w` is "wait for the local target, then Replay". There's no separate retry.
  - The summary looks like `#46 ↻ #45  422 → 200  9ms → 41ms`.
  - The response diff shows the first differing lines, at most 6.
- **Stream commands** (stdin is a terminal):
  - `↵` replays the last request (today's behavior).
  - `r N` replays #N; `c N` copies it as cURL; `e N` exports a fixture.
  - `t [source]` sends a test event; `?` shows help.
  - Anything else prints a one-line help.
- **Full-screen keys:**
  - Requests view:
    - `↑↓` select, `←→` detail tabs, `f` follow newest;
    - `r` replay, `c` copy cURL, `e` export fixture, `t` test event;
    - `/` filter, `w` wait and replay (transport failures only);
    - `s` Sources, `?` help, `q` quit.
  - Sources page:
    - `↑↓` select;
    - `c` then `1`–`6` copies a field;
    - `t` test event;
    - `esc` or `s` goes back.
- **cURL (POSIX shell only):**
  - The command is `curl -X <method> '<URL + query>'` with every header except hop-by-hop headers, `Host` and `Content-Length`.
  - The URL is the forwarded-to URL. In inspect mode (no `--forward-to`) it's the source's public URL, labelled "resends through Hookspot".
  - A body goes inline (`--data-binary '<escaped>'`) only when it's valid UTF-8 with no control characters other than `\t\n\r`. Any other body is written to the fixture's `.body` file and passed as `--data-binary @<absolute path>`.
  - Headers with any control character are written to `<name>.headers` and passed as `-H @<absolute path>` (curl ≥ 7.55). The file holds unredacted values, so it's created with mode `0600` and the toast says it contains credentials. The command never contains raw control bytes, so pasting it can't end bracketed paste or break argv.
  - **The displayed or printed command** redacts sensitive headers (unless `--show-sensitive-headers`) and passes through `cards.Sanitize`.
  - **The full command** goes only to the clipboard, through `tea.SetClipboard` (OSC 52) in the terminal modes. The toast says the copy needs a terminal with OSC 52 support (Terminal.app has none), and that `--show-sensitive-headers` shows the full command.
  - In plain mode `c N` prints the redacted command.
- **Inspect mode** (no `--forward-to`):
  - there's nothing local to replay, so `↵`, `r` and `w` are hidden from hints and refused with a one-line reason;
  - `c`, `e` and `t` work.
- **Fixture:**
  - Written as `hookspot-fixtures/<name>.json` (`{method, path, query, headers}`) plus `hookspot-fixtures/<name>.body` (raw bytes).
  - `<name>` is the `request_uid` when it matches `^[A-Za-z0-9_-]+$`, otherwise `entry-<N>`.
  - An existing file for the same name is overwritten; same `request_uid` means the same request.
  - Sensitive header values are redacted unless `--show-sensitive-headers`, and the confirmation line says when they were.
- **Test event:**
  - A `POST` to the source's public URL with body `{"type":"hookspot.test","sent_at":…}` and the header `X-Hookspot-Test: <random id>`.
  - Every delivery whose `x-hookspot-test` header (matched case-insensitively) equals that id gets a `test` badge and a "path works" line. A source with several routes produces several.
  - `t` only accepts sources this run is listening to.
  - It goes through Hookspot, so it shows in the dashboard like any other request.
- **Sources page copy fields:**
  1. public URL
  2. source ID
  3. destination URL (the path alone in inspect mode)
  4. route ID
  5. `hookspot listen <source>`, plus `--forward-to <target>` when forwarding
  6. the test cURL
- **Color and width:**
  - Render with lipgloss v2 and write through its color-profile writers, which downsample and handle `NO_COLOR` and non-terminal output. Don't hand-roll detection.
  - Width is the actual terminal width, or 100 when unknown.
- **Escaping:** all server and delivery text goes through `cards.Sanitize` before styling. `cards.Line` also escapes `\t` (as `safeDisplayText` does today) so box widths stay correct. One implementation replaces the helpers in `printer` and `cmd/errors.go`.
- **Out of scope:**
  - provider-aware summaries (idea 5);
  - pausing a source (its meaning isn't decided);
  - mouse support.

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): code, tests and docs in this repo, plus Task 2 in `~/projects/my/hookspot`
- **Post-Completion** (no checkboxes): manual terminal checks, deploy order, release notes

## Implementation Steps

### Task 1: Re-baseline after the prerequisite

**Files:**
- Modify: `docs/plans/20261001-terminal-redesign.md`

- [x] confirm `20260924-local-webhook-setup.md` CLI tasks are done and the working tree is committed
- [x] re-read `cmd/listen.go`, `internal/printer`, `internal/ws/client.go` and `internal/proxy`; list every message and hint the prerequisite added (wording, stdout or stderr)
- [x] update Context and the `Notice` list in Solution Overview to match; record the exact `Ready …` line that plain mode must keep byte-identical
- [x] decide which hints are a `Notice` and which belong in the transport-failure card (e.g. the Docker `localhost` hint); each lives in one place only
- [x] run `make test` as the baseline - must pass before next task
- ⚠️ the Docker `localhost` hint moves into the transport failure card (below the refused-target hint), and the 3xx `Location` and redirect hint into the HTTP failure card's response section: each describes one request, so the full-screen detail shows it too. In plain mode both move from stderr to stdout with their card, which departs from the prerequisite's stream rule
- ⚠️ the source warnings stay `Notice`s on stderr, so Task 5's banner box doesn't repeat them; the root-404 hint stays a once-per-run `Notice`

### Task 2: Backend: add `route_uid` to the delivery payload

**Files:**
- Modify: `~/projects/my/hookspot/lib/ingest/jobs/deliveries/deliver_job.ex`
- Modify: `~/projects/my/hookspot/test/ingest/jobs/deliveries/deliver_job_test.exs`

- [x] add `route_uid: delivery.route_uid` to the `deliver/2` payload
- [x] in "broadcasts prefixed uids to the project's topic", set `route_uid` on the in-memory `%Delivery{}` and assert it is broadcast
- [x] run `docker-compose exec app mix test` and `docker-compose exec app mix precommit` - must pass before next task
- [x] stage exact paths only (the backend tree may be dirty)

### Task 3: CLI: route attribution and labels

**Files:**
- Modify: `internal/ws/client.go`
- Modify: `internal/ws/client_test.go`
- Create: `internal/session/routes.go`
- Create: `internal/session/routes_test.go`
- Modify: `cmd/listen.go`
- Modify: `cmd/listen_test.go`

- [x] add the optional `RouteUID string \`json:"route_uid"\`` to `ws.Delivery`
- [x] `session.RouteFor(sources, delivery)` with the rule from Solution Overview, including the fallback when `route_uid` is set but unknown
- [x] `session.RouteLabel(route)`: the name when non-empty, else the destination path; replaces `routeLabel` in `cmd/listen.go`
- [x] write tests: decoding with and without `route_uid`; unknown `route_uid` falls back to the path; no match; label rule; update `TestRouteLabel` and the banner assertions
- [x] run `make test` - must pass before next task
- ⚠️ `RouteFor` returns `(api.Route, bool)` and looks only at the delivery's source, for `route_uid` as for the path, so a `route_uid` of another source falls back to the path
- ⚠️ the plain banner now labels every route, so an unnamed one reads `→ http://localhost:3000/webhooks/orders (/webhooks/orders)` until Task 7's Cards banner; `TestRouteLabel` moved to `internal/session/routes_test.go`

### Task 4: lipgloss and the `cards` foundation

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/cards/style.go`
- Create: `internal/cards/sanitize.go`
- Create: `internal/cards/sanitize_test.go`

- [x] add `charm.land/lipgloss/v2` and `github.com/charmbracelet/x/exp/golden`, pinned (check the module paths). Each later Charm module is added in the task that first imports it (bubbletea and teatest in Task 8, bubbles in Task 12), so `make tidy` never drops one
- [x] `cards` palette: source colors by UID hash as today, status colors, badges; the width rule
- [x] move `escapeText`/`singleLine` from `internal/printer` into `cards.Sanitize`/`cards.Line`; `cards.Line` escapes `\t`
- [x] write tests: control characters (C0, C1, `\r`, `\t`), invalid UTF-8; port the escaping cases from `printer_test.go` and `errors_test.go`
- [x] run `make test` and `make vet` - must pass before next task
- ➕ `internal/cards/style_test.go` with `testdata/TestPalette.golden`: the color golden that pins the palette (it also keeps `golden` imported, so `make tidy` keeps it) and the width fallback
- ⚠️ the palette uses the 16 ANSI colors (source colors keep today's codes and hash), not the canvas's hex values, so cards follow light and dark terminal themes
- ⚠️ `printer` single-line fields now escape `\t` through `cards.Line`; any `singleLine`/`escapeText` call added to `printer` on another branch becomes `cards.Line`/`cards.Sanitize` at merge

### Task 5: Cards components for `listen`

**Files:**
- Create: `internal/cards/listen.go`
- Create: `internal/cards/listen_test.go`
- Create: `internal/cards/testdata/`

- [x] banner box: project, one line per route (source, public URL, → destination, label), key hints, and the prerequisite's warnings; connection state isn't part of the box
- [x] success row: method badge, source, path, summary, status badge, latency, time, and `↻ replay` / `test` marks
- [x] HTTP failure card (request and response sections, highlighted JSON, `--max-*` limits, redaction); transport failure card (hints moved from `printer.transportHint`); inspect card (query, headers, body); notice and connection-state chips
- [x] port the `printer_test.go` behaviours as golden cases: the redaction list, limits and omitted counts, MIME fallback, summary key priority (`type`, `event`, `event_type`, `action`), binary bodies
- [x] write golden tests per the Testing Strategy, plus hostile input (control characters in names, headers, bodies)
- [x] run tests - must pass before next task
- ➕ API for Tasks 7–12: `cards.Banner(project, []BannerRoute, hints, width)`; `cards.Listen{Sources, Limits, ShowSensitiveHeaders, Container}.Request(cards.Request, width)`, which picks the row or card; `Connecting`, `Ready`, `ConnectionLost`, `Reconnected`; `DisabledSource`, `SkippedSource`, `RootNotFound`. Route labels and destinations come in as plain data. `box` now draws through the shared `frame` (titled section rules, a footer, title truncation); `TestPalette` also pins the JSON highlight colors
- ⚠️ the prerequisite's warnings stay out of the box (Task 1): they're the `DisabledSource`/`SkippedSource` notices. Connection states and notices color today's wording instead of adding padded badges, so their ANSI-stripped text is today's line, `Ready …` byte for byte
- ⚠️ rows and card titles show `#N`, so `r N`, `c N` and `e N` have numbers to refer to; `cards.Request.Replay` is a bool for the `↻ replay` mark until Task 9
- ⚠️ the HTTP failure card shows request and response bodies, not headers, as today and on the canvas; redaction applies to the inspect card's headers, which now read `hidden`, and limits end in `…`, `… N more lines` and `… N more headers`
- ⚠️ banner routes move below their source when a line doesn't fit; an unnamed route's label (its path) is hidden when the destination already ends with it; inspect mode shows `→ terminal only`
- ⚠️ transport hints name the full forwarded-to URL (`Request.Target`); `Listen.Container` is data, so Task 7 moves `printer.runningInContainer` to fill it

### Task 6: `session` core: history, forwarding, ordered events

**Files:**
- Create: `internal/session/session.go`
- Create: `internal/session/history.go`
- Create: `internal/session/stats.go`
- Create: `internal/session/session_test.go`
- Modify: `cmd/listen.go`
- Modify: `cmd/listen_test.go`

- [x] move `forwardSession`, `readLocalResponseBody` and `latencyMilliseconds` from `cmd/listen.go` into `session`; replace `replayCache` with the numbered history
- [x] `Session.Handle` (the websocket handler) and `Replay(n)`, with the emit-mutex ordering and the `Sink`/event types from Solution Overview; per-route `Stats` snapshots
- [x] `superviseListen` emits connection states and notices through the sink instead of writing to `errOut`
- [x] wire `cmd/listen.go` to `session` through a temporary sink adapter over the existing printer (deleted in Task 7) so the build and tests stay green
- [x] write tests: numbering; eviction by count and by bytes; replaying an evicted number; `ReplayOf`; a replay racing live deliveries keeps number order equal to emit order; percentiles with timeouts and other transport failures; replays excluded from stats; a sink error is returned from `Handle`; inspect mode still answers 200
- [x] run tests - must pass before next task
- ➕ API for Tasks 7–14: `session.New(ctx, sources, forwarder, sink)` (`forwarder` nil in inspect mode); `Handle`; `Replay(n)`, which fails with `ErrUnknown`, `ErrEvicted` or `ErrNoTarget` (inspect mode); `ReplayLast`, a no-op before the first request; `Emit` for connection states and notices. Events: `Connecting`, `Ready`, `ConnectionLost{Err, RetryIn}`, `Reconnected{Offline}` (the sink knows the requests URL), `RootNotFound{Root, Status}` and `Recorded{Entry, Route, Totals, Evicted}`; only `Recorded` carries a snapshot. `Entry` has `cards.Request`'s fields (`Target`, `Response`, `Latency`, `Failure`, `Received`) plus `RouteUID` and `ReplayOf`. `RunE` emits `Connecting` after the banner, and `connectionNotices` emits the other states
- ⚠️ the source warnings still go straight to stderr from `resolveSources`, before the session exists (the printer adapter needs the resolved sources), so Task 7 adds their `Notice` events; `Entry.Test` is left to Task 11, which sets it
- ⚠️ `OK`, `Failed` and latencies count forwarded requests only, so inspect-mode stats are counts; replays are left out of the totals as well as route stats; p50/p95/max cover the newest 1000 samples per route and in the totals, so memory stays bounded
- ⚠️ `Entry.Target` is the full forwarded-to URL, as in `cards.Request`, so until Task 7 the plain transport hint names it rather than the `--forward-to` base; `ReplayLast` replays the newest entry, which may itself be a replay of the same delivery

### Task 7: Plain stream writer

**Files:**
- Create: `internal/cards/writer.go`
- Create: `internal/cards/writer_test.go`
- Modify: `cmd/listen.go`
- Modify: `cmd/listen_test.go`
- Modify: `cmd/version.go`
- Modify: `cmd/version_test.go`
- Delete: `internal/printer/`

- [x] `cards.Writer` implements `session.Sink`: banner, one write per event, notices on stderr, the `Ready …` line byte-identical on stdout; color only when stdout is a terminal
- [x] use it for all `listen` output, terminal and piped, until Task 8; keep line commands when stdin is a terminal (`↵` and `r N` now; `c`, `e` and `t` come in Tasks 10–11)
- [x] delete `internal/printer` and the temporary adapter; fold `startReplayInput`/`replayInputSession` into the line-command reader; move `cmd/version.go`'s `SupportsColor` use to `cards`
- [x] write command-level tests (masked times, run ended by an invalid delivery payload): piped `listen` in inspect and forward modes matches the golden stream; interleaved deliveries stay ordered; write errors surface through the handler as today
- [x] update the `cmd/listen_test.go` assertions on the old text
- [x] run tests - must pass before next task
- ➕ Task 15 already moved `cmd/version.go`'s `SupportsColor` use to `cards`, so that item is done. It also left `safeDisplayText` in `cmd/errors.go` as a `cards.Line` wrapper used only by `cmd/listen.go`: replace those calls and delete it
- ➕ `session.DisabledSource` and `SkippedSource` events: `resolveSources` returns them, and `RunE` emits them to the writer before the session exists (nothing else writes yet), even when no source is left
- ⚠️ the line-command hints (`↵ replay last · r N replay #N · ctrl-c quit`) moved into the banner footer, so `Ready …` is no longer followed by `↵ replay last request`. `r N` on an unknown or evicted number answers with the reason, and any other line, `?` included, with the command list. These replies (`Writer.Reply`, under the writer's own mutex because they don't come through the session) go to stderr, so piped stdout keeps only the stream
- ⚠️ line commands still start only when forwarding; inspect mode gets them with `c`, `e` and `t` (Tasks 10–11), which work there
- ⚠️ each stream gets its own color profile, detected once with `colorprofile.Detect` (now a direct dependency), so stderr notices are colored when stderr is a terminal. Each event is rendered into a buffer and written once, so short writes still surface as `io.ErrShortWrite`
- ⚠️ the golden stream (`cmd/testdata/TestListenStream`) masks clock times, local ports and latencies; a line with a latency keeps only two-space gaps, since the latency's width moves its padding. Interleaving is pinned by the forward golden (one burst of rows and cards from two sources). Write errors are tested in `writer_test.go` plus the session's sink-error test, because a subprocess whose stdout breaks dies of SIGPIPE

### Task 8: Terminal stream: status line and prompt

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/tui/stream.go`
- Create: `internal/tui/stream_test.go`
- Create: `internal/tui/program.go`
- Modify: `cmd/listen.go`
- Modify: `main.go`
- Modify: `main_signal_unix_test.go`

- [x] add `charm.land/bubbletea/v2` and `github.com/charmbracelet/x/exp/teatest/v2`, pinned
- [x] `tui.Program` helper: `WithoutSignalHandler`; the input/output options from Solution Overview; the sinks (the `Println` wrapper that returns `ErrClosed`, `Send`); the Ctrl-C, `q` and program-exits-first sequences with an injectable exit function
- [x] inline stream model: the view is a status line (connection, project, counts, p50, hints) plus the `›` prompt when stdin is a terminal; it becomes the default when stdout is a terminal
- [x] `↵` replays the last request, `r N` replays #N, `?` shows help; replays run in `tea.Cmd`s
- [x] the error mapping from Solution Overview (a panic stays an error)
- [x] write teatest tests:
  - whole cards in order under a burst and with a racing replay;
  - counts update; reconnect states;
  - `r N` on missing and evicted numbers;
  - first Ctrl-C stops gracefully; second Ctrl-C calls `Kill` and the injected exit with 130, without hanging the handler;
  - the program exiting first (startup error, panic) stops the listener with no "delivery could not be processed" error;
  - non-terminal stdin shows no prompt.
- [x] keep `main_signal_unix_test.go` passing: a second SIGINT still kills the process when input isn't raw
- [x] run tests - must pass before next task
- ➕ API for Tasks 9–14: `tui.NewProgram(input, output, stop)`, `Run(model)` (returns the final model), `Println`, `Send`, `Quit` (after listen returns), `StreamSink(cards.Listen, requestsURL)`, the `tui.Stop` Cmd for full-screen `q`, and `tui.Stream{Replayer, Project, Forwarding, Prompt}`. `cmd/listen.go`'s `runInTerminal` runs listen under a program. `cards.Status{State, Err, Project, Totals, Hints}.Line(width)` and `cards.Prompt(input, hints, width)` render the status and prompt lines (golden `TestStatus`); `cards.Listen.Entry` renders a recorded entry
- ➕ `github.com/charmbracelet/ultraviolet` moved to `eb96d541d798` (2026-09-06): the version lipgloss v2.0.6 pulled in clamps the inline cursor row when a frame shrinks, leaving the taller frame's top lines on screen (e.g. after `?` help)
- ⚠️ `main.go` is unchanged: the first raw-mode Ctrl-C cancels the listen context, a child of the signal context, so `main.go` keeps handling SIGTERM and `kill -INT` alone. The signal test gained a `program` mode that runs the stream with non-raw input
- ⚠️ only a bare `ErrProgramKilled` (from `Kill`) maps to nil; wrapped, it carries a panic or an input read failure, which stay errors. `Handle` needs no `ErrClosed` check: the program cancels the listen context before `Println` fails, and the websocket client returns the cancelled context for a handler that ends after it (tested with a real `ws.Client`)
- ⚠️ the commands and `ctrl-c quit` sit at the right of the prompt line (the status line has no room at 80 columns); without a prompt the status line names `ctrl-c quit`, and after the first Ctrl-C `ctrl-c force quit`. The terminal banner has no hints. Replies (a typo, an evicted number, multi-line `?` help, a failed replay) show above the status line until the next command; a failed replay doesn't end listen as in plain mode
- ⚠️ `ConnectionLost` only updates the status line (`○ reconnecting · … · <error>`), since every retry would print a line; `Reconnected` and the root-404 hint print above it; source warnings still go to stderr before the program starts. The final frame is a `■ stopped` summary line, kept by ending the view with an empty line (bubbletea erases the frame's last line on exit)
- ⚠️ the prompt draws a reverse-video block cursor instead of the terminal cursor. Command behaviour is tested on the model (`Update`, then the returned Cmd), because the inline renderer redraws only changed cells and a reply can't be read off the output reliably; printed cards are checked with `teatest.WaitFor` and final views are goldened

### Task 9: Replay summary and response diff

**Files:**
- Create: `internal/session/diff.go`
- Create: `internal/session/diff_test.go`
- Modify: `internal/cards/listen.go`
- Modify: `internal/cards/listen_test.go`

- [x] `session.Compare(original, replay)`: status, latency, and the first differing response lines (6 at most)
- [x] the replay card shows `#46 ↻ #45  422 → 200  9ms → 41ms` plus the diff in every mode
- [x] write tests: same status, changed status, a transport failure on either side, binary bodies (size change only)
- [x] run tests - must pass before next task
- ➕ API for Tasks 12–14: `Session.Replay` sets `Entry.Replay`, a `*session.Comparison`: the original's number, status, failure, latency and body size, then `Removed` and `Added` lines (6 together; `More` counts the rest) or `Binary`. `cards.Request.Replay` is that comparison, so `Listen.Request`/`Listen.Entry` render any replay the same way in plain and terminal streams; Task 12's detail pane should reuse it
- ⚠️ a replay renders as the summary line (then source · method path and the time) with the diff lines under it, never as a row or failure card; a replay that failed in transport adds the transport hint. The `↻ replay` mark is gone, so `internal/cards/writer.go` (`request`) and the replay-mark assertions in `writer_test.go`, `internal/tui/stream_test.go` and `cmd/listen_test.go` changed too
- ⚠️ the diff drops the lines both bodies share at either end and shows the rest, removed first, so two changes far apart also show the lines between them. JSON is indented first, as cards show it; each side keeps at least 3 of the 6 lines when both have more; kept lines are copied and capped at 512 bytes so a comparison never pins a body. Bodies compare only when both got a response; a non-UTF-8 body compares by size (`binary body 5 B → 7 B`); equal bodies show no lines

### Task 10: Copy as cURL and export fixture

**Files:**
- Create: `internal/session/curl.go`
- Create: `internal/session/fixture.go`
- Create: `internal/session/curl_test.go`
- Create: `internal/session/fixture_test.go`
- Modify: `internal/tui/stream.go`
- Modify: `internal/cards/writer.go`

- [x] `session.Curl(entry, redact)` and `session.ExportFixture(entry, redact)` per Technical Details; file names validated
- [x] `c N` copies the full command with `tea.SetClipboard` and shows the redacted, sanitized command (plain mode prints it); `e N` shows the written paths and whether headers were redacted
- [x] write tests:
  - running the full curl against `httptest` reproduces method, path, query, headers and body, including the binary `@file` path (skip when `curl` is missing);
  - quotes and newlines stay inline; bodies with NUL or ESC, and headers with control characters, go to `@` files with absolute paths, and the pasted command works from another directory;
  - redaction in the displayed command and the fixture, and none with `--show-sensitive-headers`;
  - a traversal `request_uid` falls back to `entry-<N>`;
  - an unwritable directory is reported;
  - inspect mode targets the public URL.
- [x] run tests - must pass before next task
- ➕ API for Tasks 12–14: `Session.Curl(n, redact)` returns `session.Curl{Command, Shown, Redacted, HeadersFile, Resend}` (`Command` for the clipboard, `Shown` to display); `Session.ExportFixture(n, redact)` returns `session.Fixture{JSON, Body, Redacted}`; `tui.Exporter` and `Stream.Exporter`/`ShowSensitiveHeaders`; `cards.CurlNotes` and `cards.Exported` word the results; `Writer.Print` is a reply that keeps its lines. `session.SensitiveHeader` replaces the list in `cards`, and `proxy.Headers` (what `Forward` sends) picks the curl headers
- ⚠️ both take an entry number, since inspect mode needs the session's sources for the public URL. The command puts each argument on its own ` \` line; an empty header is passed as `Name;` (curl drops `Name:`), a body starting with `@` goes to the `.body` file (curl would read it as a file name), and a method or URL with control characters is refused. Every fixture file, not just `.headers`, is written `0600`, since requests carry credentials and personal data
- ⚠️ in the stream, `c N` prints the shown command above the status line, so it stays in scrollback where OSC 52 is missing, and its notes are the reply; plain mode prints notes and command to stderr like other replies. The prompt's hints are unchanged (with `c` and `e` they wouldn't fit at 80 columns); `?` and the one-line help name every command
- ⚠️ plain line commands now start in inspect mode too: `c` and `e` work there, and `↵` and `r N` answer `nothing to replay without --forward-to` and are left out of the hints

### Task 11: Test event and first-run hint

**Files:**
- Create: `internal/session/testevent.go`
- Create: `internal/session/testevent_test.go`
- Modify: `internal/cards/listen.go`
- Modify: `internal/tui/stream.go`
- Modify: `internal/cards/writer.go`

- [ ] `session.SendTest(ctx, source)` per Technical Details; badge every matching delivery
- [ ] after `Ready` with no requests yet: a hint with `t` and the test curl
- [ ] `t` with several sources and none named asks which one; `t <source>` works directly; a source this run isn't listening to is refused
- [ ] write tests: request shape against a fake source URL; case-insensitive matching and a source with two routes; an unknown or unlistened source; a non-2xx from Hookspot is reported
- [ ] run tests - must pass before next task

### Task 12: Full-screen listen: request list and detail

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/tui/fullscreen.go`
- Create: `internal/tui/detail.go`
- Create: `internal/tui/fullscreen_test.go`
- Modify: `cmd/listen.go`
- Modify: `Makefile`, `docker-compose.yml`, `.air.toml`

- [ ] add `charm.land/bubbles/v2`, pinned
- [ ] the default when stdin and stdout are both terminals; add `--stream` to keep the inline stream; `make dev`, the compose `dev` service and the `.air.toml` comment default to `listen --stream` (air's rebuilds would break the alt screen)
- [ ] the model drops rows for the numbers each event says were evicted, so its memory stays within the history caps
- [ ] header (connection, project, counts, clock); source line; request list (number, time, source, method, path, status, latency, `↻`/test marks); detail tabs Overview / Request / Response / Timing showing the source, route label and destination, and noting that replays are local
- [ ] follow newest vs. paused selection, `↑↓`, `←→`, `f`, `r`, `c`, `e`, `t`, `?`, `q`; toasts for results; the empty state shows a routes summary and the `t` hint
- [ ] write teatest golden-view tests: arrival while following and while paused; tab switching; replay adds a marked row; copy toast; a narrow terminal; evicted entries disappear from the list; `q` stops the listener like the first Ctrl-C
- [ ] run tests - must pass before next task

### Task 13: Full-screen filter and failure workflow

**Files:**
- Create: `internal/tui/filter.go`
- Create: `internal/tui/filter_test.go`
- Modify: `internal/tui/fullscreen.go`
- Modify: `internal/tui/detail.go`

- [ ] `/` filter: `status:error|2xx|4xx|5xx|<code>`, `source:<name>`, `path:<prefix>`, and free text across path and summary; `esc` clears; matches shown in the list title
- [ ] failure detail: what happened, local target state, last success; `r` replays now
- [ ] `w` dials the local target at an injectable interval until it answers or `esc`, then calls `Replay`; the result replaces the "waiting" line
- [ ] write tests: filter parsing (unknown keys are plain text); combined terms; `w` with a fake listener that starts late; `esc` stops waiting
- [ ] run tests - must pass before next task

### Task 14: Sources page

**Files:**
- Create: `internal/tui/sources.go`
- Create: `internal/tui/sources_test.go`
- Modify: `internal/tui/fullscreen.go`

- [ ] `s` opens the page; `esc` or `s` returns to the list with the selection kept
- [ ] table: source, public URL, route label, destination, REQS/OK/FAIL/P50/LAST, and a totals row (unmatched requests count here only); it updates live from event snapshots
- [ ] route detail: the six copy fields; `c` then a number copies one, with a toast showing the value; an activity panel (counts, status breakdown, latency, per-minute sparkline, last request)
- [ ] `t` sends a test event to the selected source
- [ ] write teatest tests: open, move, copy mode, a live update while open, inspect-mode fields, back to the list
- [ ] run tests - must pass before next task

### Task 15: Cards for login, logout, version, project list, errors

**Files:**
- Create: `internal/cards/commands.go`
- Create: `internal/cards/commands_test.go`
- Modify: `cmd/login.go`, `cmd/login_browser.go`, `cmd/logout.go`, `cmd/version.go`, `cmd/project.go`, `cmd/errors.go`
- Modify: `cmd/login_test.go`, `cmd/version_test.go`, `cmd/project_test.go`, `cmd/errors_test.go`

- [x] login: the browser box with the code, a static "waiting for approval" line and the ✓ lines; the `-i` prompt; logout with the environment-key warning; version with the update box; the project list table with an active badge
- [x] `version --json` output stays byte-identical (the release smoke test and the Homebrew test read it)
- [x] `HandleError` renders a red-bordered box with the hint, or plain text when stderr isn't a terminal; replace `safeErrorText`/`safeDisplayText` with `cards.Sanitize`/`cards.Line`
- [x] write golden and command-level tests for each command, including no-color and piped output
- [x] run tests - must pass before next task
- ➕ `cmd/password.go` writes the `-i` prompt (`cards.KeyPrompt`); the logout command test lives in `cmd/config_test.go`; `cards.Terminal` picks the error box or plain text
- ⚠️ the login URL and the waiting line sit below the box, unwrapped, so the URL can be copied where no browser opens; the update box shows `1.2.3 → 1.3.0` without an upgrade command, since the install channel (npm, Homebrew, archive) is unknown
- ⚠️ the error box is titled `✗ Error` with the whole message in its body, because messages carry no separate summary; the version line stays `hookspot version X`, matching `--version`
- ⚠️ `project list` badges the saved project (`store.SavedProject()`), the one `project use` marks current
- ⚠️ `safeDisplayText` remains as a `cards.Line` wrapper for `cmd/listen.go` only, which the prerequisite's Tasks 6–8 edit concurrently; Task 7 removes it

### Task 16: Project picker on bubbletea

**Files:**
- Create: `internal/tui/picker.go`
- Create: `internal/tui/picker_test.go`
- Modify: `cmd/project.go`
- Modify: `cmd/project_test.go`
- Delete: `cmd/project_picker.go`, `cmd/project_picker_unix.go`, `cmd/project_picker_windows.go`

- [ ] replace `promptProject` with a list model in the Cards picker box: `↑↓`, `↵`, `esc`, current project marked, scrolling. It uses only `tui.Program`'s options (`WithoutSignalHandler`, input, output), not the listen shutdown sequence: Ctrl-C or Esc returns `context.Canceled` and exits 0, as today
- [ ] keep the non-terminal error and the selection forms
- [ ] replace the deleted picker tests (arrow keys and cancel, escape parsing, row bounds) with teatest tests: move and select; esc cancels; a long list scrolls
- [ ] run tests - must pass before next task

### Task 17: Verify acceptance criteria

**Files:**
- Modify: `docs/releases/THIRD_PARTY_NOTICES.txt`

- [ ] every Overview item is implemented, including both repos
- [ ] refresh `THIRD_PARTY_NOTICES.txt` with the procedure in its header (all Charm modules are imported by now); cross-compile the six release targets in the pinned toolchain
- [ ] piped `listen` output has no ANSI codes and follows the golden stream; `NO_COLOR` is respected everywhere
- [ ] hostile delivery data (control characters, huge bodies, binary) can't break any layout or the terminal
- [ ] run the full suite: `make test`, `make vet`, `make npm-test`, and backend `mix test`
- [ ] after committing, `make release-snapshot` builds all six targets

### Task 18: [Final] Update documentation
- [ ] README: `listen` modes (full-screen default, `--stream`, piped), keys and commands, fixtures (redaction, file names), test event, Sources page
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Manual verification:**
- terminals: macOS Terminal and iTerm2, Windows Terminal, Linux over SSH (OSC 52 copy), `make run` in Docker with and without `-i`
- a terminal narrower than 80 columns, resizing while streaming, a burst of 100 deliveries, light and dark themes, Ctrl-C once and twice

**Deploy order:**
- deploy the backend `route_uid` change before the CLI release; older servers still work through the path fallback

**Release notes:**
- `listen` opens full-screen in a terminal; `--stream` restores a scrolling log; scripts reading piped output keep plain text

**Follow-ups:**
- provider-aware summaries (idea 5); pausing a source; on the design canvas, idea 3's `s 45` becomes `e 45`
