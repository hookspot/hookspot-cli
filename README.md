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
cannot be changed at runtime.

## Log in and select a project

Create a CLI key in the matching Hookspot environment, then use the minimal
interactive flow:

```sh
hookspot login
hookspot project use
hookspot listen
```

With one accessible project, `project use` selects it immediately. With more
than one, it opens an arrow-key picker in an interactive terminal and marks the
saved project as the default. Scripts and other noninteractive callers must use
an unambiguous form:

```sh
hookspot project list
hookspot project use PROJECT_UID
hookspot project use "Acme Inc."
hookspot project use "Acme Inc." Payments
```

Names match exactly without regard to case; quote names containing spaces. A
single path-safe argument is tried as a project UID first, then as an exact
organization name only when the UID lookup returns 404. Two arguments always
mean organization and project names.

For a noninteractive process, pass only the variables it needs:

```sh
export HOOKSPOT_CLI_KEY='...'
export HOOKSPOT_ORGANIZATION_SLUG='acme'
export HOOKSPOT_PROJECT_SLUG='payments'
```

Organization and project slug variables must be set together. A saved CLI key
and selected project are stored separately for each environment.

## Listen and forward

```sh
# Print deliveries for every source with a connection.
hookspot listen

# Select sources by name.
hookspot listen orders billing

# Preserve each delivery path while forwarding to a local server.
hookspot listen orders --forward-to http://localhost:3000
```

Inspect mode redacts authorization and cookie headers unless
`--show-sensitive-headers` is set. `--max-body-lines`, `--max-headers`, and
`--max-value-chars` bound terminal output; zero disables an individual display
limit. The deprecated `--log-level` flag is accepted for compatibility but has
no effect.

When forwarding from an interactive terminal, press Enter to replay the last
request. Replay requires a real terminal; piped input does not enable it. The
first response from the local server is reported as-is, including a redirect,
and redirects are not followed.

API redirects are also blocked so a Hookspot CLI key is never forwarded to a
different endpoint. Incoming delivery bodies may use padded or unpadded
standard Base64; responses sent back over Phoenix Channels use padded Base64.

## Configuration and logout

Default configuration lives at:

```text
~/.config/hookspot/prod/config.toml
~/.config/hookspot/dev/config.toml
```

Every command selects one complete config file in this order:

1. explicit `--config`;
2. `HOOKSPOT_CONFIG_FILE`;
3. `.hookspot/<environment>/config.toml` in the current directory;
4. the matching global file listed above.

The CLI checks only the current directory and never searches parents. Only an
absent local file falls back to the global file; a malformed, unreadable, or
unsafe local file is an error. An explicit missing `--config` path remains an
error for ordinary commands, while login may create a new file at an unused
selected path. A legacy shared file is never imported implicitly.

Use `project use --local` to create or update the current directory's complete
environment-specific record. It stores the selected project and may copy the
CLI key already persisted in the global record; flag and environment keys are
never copied. The local file contains plaintext credentials when a persisted
key is available and is ignored by this repository's `.gitignore`. `--local`
cannot be combined with `--config` or a `CONFIG_FILE` environment override.

“Current directory” is the CLI process directory, including inside Docker. Two
development shells that mount the same host checkout at `/src` and run there
therefore share the same host `.hookspot/<environment>/config.toml`. When
separate project directories are mounted in the container, enter each one
before invoking the shared binary:

```sh
cd "/workspaces/project A"
/src/tmp/hookspot project use --local "asd1" "Project 1"

cd "/workspaces/project B"
/src/tmp/hookspot project use --local "asd1" "Project 2"
```

Review the exact migration command first:

```sh
hookspot config migrate --help
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

All Go build and test commands run in the pinned Docker toolchain:

```sh
# api.example.invalid is reserved and intentionally not a live default.
make build SERVER_URL=https://api.example.invalid
make test
make vet

# Offline examples; use Compose below for the mapped developer backend.
make run SERVER_URL=https://api.example.invalid ARGS='version --json'
make run SERVER_URL=https://api.example.invalid ARGS='--help'
```

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
publishes the npm package from the same binaries, and then installs from every
channel on Linux, macOS, and Windows runners:

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
