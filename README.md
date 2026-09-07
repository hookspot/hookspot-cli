# Hookspot CLI

Hookspot CLI connects to a Hookspot project, prints incoming webhook requests,
and can forward them to a local HTTP server. Production releases use the
`hookspot` executable. Staging releases use the separate `hookspot-stage`
executable, configuration, and credentials.

Approved public stage and production service URLs have not been committed yet,
so publication remains blocked. Do not treat a development or `.invalid`
example endpoint as a live Hookspot service.

## Install

Choose a production archive from the stable-latest GitHub release, or a staging
archive from its explicit reviewed stage tag/release page. `darwin` means
macOS, `amd64` means Intel/AMD 64-bit, and `arm64` means Apple Silicon or
another ARM64 system.

Every release contains exactly six platform archives and one checksum file.
Verify the exact archive entry before extracting it, then install `hookspot`
or `hookspot.exe` for production, or `hookspot-stage` or
`hookspot-stage.exe` for staging. In a downloaded archive, follow the adjacent
`INSTALL.md`. In the repository, see the [installation guide](https://github.com/bgr11n/hookspot-cli/blob/main/docs/releases/INSTALL.md)
for shell and PowerShell commands, updating, macOS verification, and uninstalling.

Confirm the installed binary before logging in:

```sh
hookspot version --json
# or
hookspot-stage version --json
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

Listing projects and selecting one explicit UID are optional alternatives:

```sh
hookspot project list
hookspot project use PROJECT_UID
```

Use `hookspot-stage` for the same staging flow. For a noninteractive production
process, pass only the scoped variables it needs:

```sh
export HOOKSPOT_PROD_CLI_KEY='...'
export HOOKSPOT_PROD_ORGANIZATION_SLUG='acme'
export HOOKSPOT_PROD_PROJECT_SLUG='payments'
```

Staging uses `HOOKSPOT_STAGE_*`; development builds use `HOOKSPOT_DEV_*`.
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
~/.config/hookspot/stage/config.toml
~/.config/hookspot/dev/config.toml
```

Resolution order is an explicit flag, then an environment-scoped variable,
then the matching configuration file. An explicit missing `--config` path is
an error for ordinary commands; login may create a new file at an unused path.
Legacy generic variables require a matching `HOOKSPOT_ENVIRONMENT` assertion.
A legacy shared file is never imported implicitly. Review the exact migration
command first:

```sh
hookspot config migrate --help
hookspot-stage config migrate --help
```

`hookspot logout` removes the saved production key but does not unset an active
`HOOKSPOT_PROD_CLI_KEY`. Staging behaves the same with its scoped variable.

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
export HOOKSPOT_DEV_CLI_KEY='...'
docker compose --env-file release/toolchain.env run --rm cli login
docker compose --env-file release/toolchain.env run --rm cli project list
: "${PROJECT_UID:?set PROJECT_UID to one UID listed above}"
docker compose --env-file release/toolchain.env run --rm cli project use "$PROJECT_UID"
docker compose --env-file release/toolchain.env run --rm cli listen \
  --forward-to http://host.docker.internal:3000
```

## Release operators

Bootstrap the locked release tool image with the supported command; do not
maintain a second Docker build recipe in documentation:

```sh
make release-tools
```

`make release-verify ENV=stage DIST=/absolute/retained-parent` proves retained
artifact and immutable receipt integrity. It does not prove complete native
evidence or publication eligibility. The [release runbook](https://github.com/bgr11n/hookspot-cli/blob/main/docs/releases/RUNBOOK.md)
contains the token-free build flow, native requirements/reports/manual checks,
stage acceptance, trusted publisher boundary, and exact recovery commands.
