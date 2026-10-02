# Hookspot CLI

Hookspot CLI connects to a Hookspot project, prints incoming webhook requests,
and can forward them to a local HTTP server.

Service URLs are embedded during release builds. Development or `.invalid`
example endpoints are not live Hookspot services.

## Install

Every release is published to npm, Homebrew, and GitHub Releases from one
build, so all three channels install the same `hookspot` binary.

npm (Node 18 or newer; the package bundles binaries for macOS, Linux, and
Windows on amd64 and arm64):

```sh
npm install -g hookspot
```

Homebrew on macOS or Linux:

```sh
brew install hookspot/hookspot/hookspot
```

GitHub release archive: download the archive for your platform and the checksum
file from the [latest release](https://github.com/hookspot/hookspot-cli/releases/latest),
verify the checksum, and install `hookspot` (`hookspot.exe` on Windows). Each
archive contains an `INSTALL.md` with shell and PowerShell commands, the macOS
Gatekeeper note for the unsigned binary, updating, and uninstalling; the same
guide is in the repository at
[docs/releases/INSTALL.md](https://github.com/hookspot/hookspot-cli/blob/main/docs/releases/INSTALL.md).

Confirm the installed binary before logging in:

```sh
hookspot version --json
```

The JSON identifies the version, source commit, environment, compiled endpoint,
Go version, and target platform. The endpoint is part of the executable and
cannot be changed at runtime. `version --json` never contacts the network;
`hookspot version` prints the version and, for release builds, asks GitHub for
the newest release with a 5-second timeout and prints an upgrade notice when
there is one (an offline machine sees no notice and no error). `--version` and
`-v` print only the version.

## Log in and select a project

`hookspot login` opens your browser, where you confirm the printed code and
choose an organization and project. The CLI saves both the CLI key and the
selected project:

```sh
hookspot login
hookspot listen
```

On a headless or SSH machine the browser cannot open; visit the printed URL
manually. To paste a key instead, run `hookspot login -i` (piping a key on
stdin requires `-i`). For CI, pass `HOOKSPOT_CLI_KEY` or `--cli-key` and skip
login. Browser login requires a Hookspot server with browser login support.

To change the selected project, run `hookspot project use`. With one accessible
project it selects it immediately; with more than one it opens an arrow-key
picker in an interactive terminal and marks the saved project as the default.
Scripts and other noninteractive callers must use an unambiguous form:

```sh
hookspot project list
hookspot project use PROJECT_UID
hookspot project use "Acme Inc."
hookspot project use "Acme Inc." Payments
```

Names match exactly without regard to case; quote names containing spaces. A
single path-safe argument is tried as a project UID first, then as an exact
organization name only when the UID lookup returns 404. Two arguments always
mean organization and project names. Project UIDs are prefixed (`proj_…`). A
config saved by an older CLI may hold an unprefixed UID, which the server no
longer accepts; run `hookspot project use` or `hookspot login` to select the
project again.

For a noninteractive process, pass only the variables it needs:

```sh
export HOOKSPOT_CLI_KEY='...'
export HOOKSPOT_ORGANIZATION_SLUG='acme'
export HOOKSPOT_PROJECT_SLUG='payments'
```

Organization and project slug variables must be set together. A saved CLI key
and selected project are stored separately for each config prefix. To listen on
another project for one run, pass `--project PROJECT_UID`; it overrides the
slug variables and the saved project.

## Listen and forward

```sh
# Print deliveries for every source with a route.
hookspot listen

# Select sources by name.
hookspot listen orders billing

# Preserve each delivery path while forwarding to a local server.
hookspot listen orders --forward-to http://localhost:3000
```

Sensitive header values (authorization, cookies, API keys) are hidden on
screen, in cURL commands and in fixtures unless `--show-sensitive-headers` is
set. `--max-body-lines`, `--max-headers`, and `--max-value-chars` bound
terminal output; zero disables an individual display limit. The deprecated
`--log-level` flag is accepted for compatibility but has no effect.

`--forward-to 3000` means `http://localhost:3000`, and `3000/webhooks` means
`http://localhost:3000/webhooks`. A delivery to `/` goes to the `--forward-to`
URL itself, ending in `/` only when you typed one (`8000/webhooks/` for
Django's `APPEND_SLASH`). The local hop ignores `HTTP_PROXY` and
`HTTPS_PROXY`, which still apply to the connection to Hookspot.

In plain mode, `listen` prints `Ready. Waiting for requests (Ctrl-C to quit)`
once deliveries can flow; scripts should wait for it. When a dropped
connection comes back, `Reconnected after <time> offline` says requests from
the gap were not delivered and links to the dashboard to retry them. Plain
mode writes `Ready` and request blocks to stdout; connection notices, source
warnings, and the root-404 and test hints go to stderr.

The first response from the local server is reported as-is, including a
redirect, and redirects are not followed; a 3xx shows its `Location`, since
webhook senders don't follow redirects either. A refused `localhost` inside a
container suggests the service name or `host.docker.internal`, and the first
404 or 405 from the bare `--forward-to` root suggests adding the webhook path.
API redirects are also blocked so a Hookspot CLI key is never forwarded to a
different endpoint. Incoming delivery bodies may use padded or unpadded
standard Base64; responses sent back over Phoenix Channels use padded Base64.

### Output modes

`listen` picks its output from where it runs:

- **Full-screen** (stdin and stdout are terminals): a request list with a
  detail pane, a filter, and a Sources page.
- **Stream** (`--stream`, or a terminal stdout with non-terminal stdin): each
  request prints as it arrives, above a pinned status line. When stdin is a
  terminal, a `›` prompt below it takes commands.
- **Plain** (stdout is piped or redirected, or `TERM=dumb`): text with no
  color or cursor control, one block per request, for scripts. When stdin is
  still a terminal (`hookspot listen | tee log`), the same commands work and
  their replies go to stderr; a background job (`&`) gets none.

`NO_COLOR` turns color off in every mode. A route shows as its name, or as its
destination path when it has none.

Requests are numbered from #1 in each run. The run keeps the newest 1000
requests and 64 MiB of bodies; naming an older number says it was dropped.

### Full-screen keys

| Key | Action |
|---|---|
| `↑` `↓` | select a request; stops following |
| `←` `→` | detail tabs: Overview, Request, Response, Timing |
| `pgup` `pgdn` | scroll the detail; stops following |
| `f` | follow the newest request |
| `/` | filter; `↵` applies, `esc` clears |
| `r` | replay the selected request |
| `w` | wait until the local server accepts connections, then replay (transport failures only); `esc` stops waiting |
| `c` | copy as cURL |
| `e` | export a fixture |
| `t` | send a test event to the selected request's source (before any request, the first source) |
| `s` | Sources page |
| `?` | full help |
| `q`, `ctrl-c` | stop listening; a second `ctrl-c` forces exit |

Filter terms must all match: `status:error`, `status:2xx` (also `3xx`, `4xx`,
`5xx`, or a code such as `status:422`), `source:<name>`, `path:<prefix>`, and
free text matched against the path and the event summary.

### Stream commands

Type at the `›` prompt, or in plain mode at the terminal, and press Enter:

| Command | Action |
|---|---|
| `↵` | replay the last request |
| `r N` | replay request #N |
| `c N` | copy request #N as cURL |
| `e N` | export request #N as a fixture |
| `t [source]` | send a test event |
| `?` | help |

Anything else lists the commands.

### Replay

A replay resends a request to the `--forward-to` server and records it as a new
request, with a summary such as `#46 ↻ #45  422 → 200  9ms → 41ms` and up to 6
lines of response diff. Replays are local: they never change the delivery's
status in Hookspot. Without `--forward-to` there is nothing to replay; `c`,
`e`, and `t` still work.

### Copy as cURL

`c` builds a `curl` command for a POSIX shell that sends the request to the URL
it was forwarded to. Without `--forward-to` it targets the source's public URL,
so it resends the request through Hookspot. The full command goes to the
clipboard through OSC 52, which needs a terminal that supports it (Terminal.app
does not). Full-screen copies the command only; the stream also prints it, and
plain mode prints it instead (to stderr). Printed commands hide sensitive
header values unless `--show-sensitive-headers` is set. Without OSC 52, use
`c N` in the stream or in plain mode.

A body over 64 KiB, or with control characters other than newlines, is written
to `hookspot-fixtures/<name>.body` and passed as `--data-binary @<path>`. Headers
with control characters are written to `hookspot-fixtures/<name>.headers` and
passed as `-H @<path>` (curl 7.55 or newer); that file holds unredacted values.
Both paths are absolute, so the command works from any directory.

### Fixtures

`e` writes two files under `hookspot-fixtures/` in the current directory:
`<name>.json` with the method, path, query, and headers, and `<name>.body` with
the raw body. `<name>` is the request UID, followed by `_<route ID>` when a
route matched, or `entry-<N>` when that isn't a plain file name (letters,
digits, `_`, and `-`). Exporting the same request or its replay again
overwrites its files. Sensitive header values are written as
`[redacted]` unless `--show-sensitive-headers` is set, and the confirmation
says when they were. On macOS and Linux, fixture files are readable only by
their owner.

### Test event

`t` checks the whole path: it posts `{"type":"hookspot.test","sent_at":…}` to
a source's public URL with an `X-Hookspot-Test` header. The request goes
through Hookspot and shows in the dashboard like any other. Each delivery it
produces is marked `test` with a "path works" line; a source with several
routes produces several. Only sources this run listens to are accepted. In the
stream, `t` needs no name with one source; with several, name one
(`t stripe`).

Until the first request arrives, `listen` shows a `curl` command per source
that sends a test request from anywhere.

### Sources page

`s` in full-screen lists every source and route this run listens to: public
URL, route, destination, and live counts since `listen` started (requests, OK,
failed, p50 latency, last request; without `--forward-to`, only requests and
the last one). The totals row also counts requests no route matched.

The selected route's detail has six numbered fields. Press `c`, then a number,
to copy one:

1. public URL
2. source ID
3. destination URL (the path alone without `--forward-to`)
4. route ID
5. the `hookspot listen` command for the source
6. a test `curl` command

An activity panel shows the status breakdown, latency, requests per minute over
the last 15 minutes, and the last request. `t` sends a test event to the
selected source; `esc` or `s` returns to the request list.

## Configuration and logout

Default configuration lives at `~/.config/hookspot/<prefix>/config.toml`. The
prefix is set at build time: releases have none (`~/.config/hookspot/config.toml`),
development builds use `dev`, and `scripts/build-stage.fish` uses `stage`.

Every command selects one complete config file in this order:

1. explicit `--config`;
2. `HOOKSPOT_CONFIG_FILE`;
3. `.hookspot/<prefix>/config.toml` in the current directory;
4. the matching global file above.

The CLI checks only the current directory and never searches parents. Only an
absent local file falls back to the global file; a malformed, unreadable, or
unsafe local file is an error. An explicit missing `--config` path remains an
error for ordinary commands, while login may create a new file at an unused
selected path.

Use `project use --local` to create or update the current directory's complete
record. It stores the selected project and may copy the
CLI key already persisted in the global record; flag and environment keys are
never copied. The local file contains plaintext credentials when a persisted
key is available and is ignored by this repository's `.gitignore`. `--local`
cannot be combined with `--config` or a `HOOKSPOT_CONFIG_FILE` environment override.

“Current directory” is the CLI process directory, including inside Docker. Two
development shells that mount the same host checkout at `/src` and run there
therefore share the same host `.hookspot/<prefix>/config.toml`. When
separate project directories are mounted in the container, enter each one
before invoking the shared binary:

```sh
cd "/workspaces/project A"
/src/tmp/hookspot project use --local "asd1" "Project 1"

cd "/workspaces/project B"
/src/tmp/hookspot project use --local "asd1" "Project 2"
```

`hookspot logout` removes the saved key but does not unset an active
`HOOKSPOT_CLI_KEY`.

## Client limits and cancellation

The CLI enforces a 32 MiB WebSocket frame limit, a 16 MiB local response-body
limit, and a 1 MiB successful API JSON limit. These are CLI safeguards, not
claims about deployed backend or infrastructure limits. WebSocket join and
write operations have 10-second bounds, heartbeats run every 30 seconds, and
90 seconds without qualifying receive activity closes the session. A valid
serial delivery renews the receive window after it finishes processing.

Output and forwarding remain synchronous to preserve order. The first Ctrl-C
starts graceful cancellation of dialing, TLS/HTTP upgrade, proxy CONNECT,
joining, and in-flight network work. An operating-system write to an unread
pipe can still block graceful completion. After normal signal handling is
restored, a later Ctrl-C can force exit and may interrupt cleanup.

## Development

The `make` targets run Go in the pinned Docker toolchain:

```sh
# api.example.invalid is reserved and intentionally not a live default.
make build SERVER_URL=https://api.example.invalid
make test
make vet

# Offline examples; use Compose below for the mapped developer backend.
make run SERVER_URL=https://api.example.invalid ARGS='version --json'
make run SERVER_URL=https://api.example.invalid ARGS='--help'
```

Golden files under `testdata/` pin rendered output. After an intended output
change, regenerate them with
`go test ./cmd ./internal/cards ./internal/tui -update` (other packages reject
the flag) and review the diff. CI also runs the pseudo-terminal tests natively
on macOS and Windows: `go test -run '^TestTerminal' ./cmd`.

`make npm-test` runs the npm launcher tests with the host `node` (18 or newer),
and `scripts/smoke_test.sh` exercises the post-release smoke script against
local fixtures; neither needs Docker.

`scripts/build-dev.fish` and `scripts/build-stage.fish` build an unpublished
macOS binary for that server with GoReleaser into `tmp/dist/hookspot_dev` or
`tmp/dist/hookspot_stage`.

`make run` keeps stdin open for interactive or piped login and allocates a TTY
only when stdin and stdout are terminals. `make run` and `make dev` use the
dedicated `hookspot-dev-config` volume, so development login and project
selection survive disposable containers. Build, test, and release containers
do not mount that application configuration volume.

Docker Compose provides the same development-only image and maps
`hookspot.localhost` and `host.docker.internal` back to the host:

```sh
docker compose --env-file release/toolchain.env build cli
export HOOKSPOT_CLI_KEY='...'
docker compose --env-file release/toolchain.env run --rm cli login
docker compose --env-file release/toolchain.env run --rm cli project list
: "${PROJECT_UID:?set PROJECT_UID to one UID listed above}"
docker compose --env-file release/toolchain.env run --rm cli project use "$PROJECT_UID"
docker compose --env-file release/toolchain.env run --rm cli listen \
  --forward-to http://host.docker.internal:3000
```

## Releasing

Pushing a `v*` tag publishes the release. The tag-triggered workflow builds the
six platform archives with GoReleaser in the locked Docker image, creates the
GitHub Release, pushes the Homebrew formula to `hookspot/homebrew-hookspot`,
publishes the npm package from the same binaries, and then installs from
GitHub Releases and npm on Linux, macOS, and Windows runners and from Homebrew
on macOS:

```sh
git tag -a v1.2.3 -m "v1.2.3"
git push origin v1.2.3
gh run watch
```

To exercise the pipeline locally, build an unpublished snapshot (version
`0.0.0-snapshot.<sha>`). It requires a clean working tree because the build
embeds VCS metadata:

```sh
make release-tools
make release-check
make release-snapshot
```

The [release runbook](https://github.com/hookspot/hookspot-cli/blob/main/docs/releases/RUNBOOK.md)
covers version choice, secrets, re-running a failed job, and yanking a release.
