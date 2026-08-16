# hookspot

A companion CLI for hookspot. Connects to your hookspot project over a
websocket, prints incoming webhook events to the terminal, and optionally
forwards them to a local server.

## Usage

### Authenticate

Generate a CLI key from the hookspot UI, then either:

```bash
hookspot login
```

or set it via environment variable (recommended for Docker / CI):

```bash
export HOOKSPOT_CLI_KEY=hk_...
```

### Select a project

```bash
hookspot project list
hookspot project use               # select interactively
hookspot project use <project-id>  # optional non-interactive form
```

Or select it by organization and project slug:

```bash
export HOOKSPOT_ORGANIZATION_SLUG=acme
export HOOKSPOT_PROJECT_SLUG=payments
```

### Listen for events

```bash
# Print all deliveries in the active project to the terminal
hookspot listen

# Only specific sources, including request bodies
hookspot listen my-source --print-body

# Forward deliveries to a local server; each delivery keeps its own path,
# e.g. /webhooks/stripe -> http://localhost:3000/webhooks/stripe
hookspot listen --forward-to localhost:3000
```

## Configuration

Settings are resolved in this order: command-line flags, environment
variables (`HOOKSPOT_CLI_KEY`, `HOOKSPOT_ORGANIZATION_SLUG`,
`HOOKSPOT_PROJECT_SLUG`, `HOOKSPOT_LOG_LEVEL`), the config file
(`~/.config/hookspot/config.toml` by default, override with `--config`), then
built-in defaults. The organization and project slug variables must be set
together.

The hookspot server URL is not user-configurable: it is baked into the
binary at build time via `-ldflags "-X hookspot/cmd.serverURL=https://..."`
and is required — a binary built without it will error on any command that
talks to the hookspot server. See [Development](#development) for how to
set it when building.

## Running in Docker

Build the image with the hookspot server URL baked in:

```bash
docker build -t hookspot:dev --build-arg SERVER_URL=https://api.hookspot.dev .
```

Then run it:

```bash
docker run --rm \
  -e HOOKSPOT_CLI_KEY=hk_... \
  -e HOOKSPOT_ORGANIZATION_SLUG=acme \
  -e HOOKSPOT_PROJECT_SLUG=payments \
  --network host \
  hookspot:dev listen --forward-to localhost:3000
```

`--network host` is Linux-only and isn't available on Docker Desktop for
Mac/Windows. On those platforms, omit `--network host` and instead forward
to `host.docker.internal` so the container can reach a server running on
your host machine:

```bash
docker run --rm \
  -e HOOKSPOT_CLI_KEY=hk_... \
  -e HOOKSPOT_ORGANIZATION_SLUG=acme \
  -e HOOKSPOT_PROJECT_SLUG=payments \
  --add-host host.docker.internal:host-gateway \
  hookspot:dev listen --forward-to host.docker.internal:3000
```

## Running with Docker Compose

A `docker-compose.yml` is provided for building and running `hookspot`
without a local Go toolchain.

```bash
docker compose build

# Set credentials via env vars (or a .env file) before running
export HOOKSPOT_CLI_KEY=hk_...
export HOOKSPOT_ORGANIZATION_SLUG=acme
export HOOKSPOT_PROJECT_SLUG=payments

docker compose run --rm hookspot login
docker compose run --rm hookspot project list
docker compose run --rm hookspot listen --forward-to host.docker.internal:3000
```

`host.docker.internal` (mapped via `extra_hosts` in `docker-compose.yml`)
lets the container reach services running on your host machine — use it in
`--forward-to` when the target server (e.g. `localhost:3000`) runs outside
the container.

## Development

All builds/tests run via Docker through the Makefile. `build` and `run`
require `SERVER_URL`, which is baked into the binary at build time:

```bash
make build SERVER_URL=https://api.hookspot.dev   # compile-check
make test                                        # run tests
make run SERVER_URL=https://api.hookspot.dev ARGS="listen --help"

# Live reload (runs `listen` by default)
HOOKSPOT_CLI_KEY=hk_... HOOKSPOT_ORGANIZATION_SLUG=acme \
  HOOKSPOT_PROJECT_SLUG=payments make dev
make dev ARGS="listen --help"
```

### Stage releases

Stage releases are run from a local checkout using the pinned official
GoReleaser Docker image; they do not require a local Go or GoReleaser install.
The command tests the project, builds Linux, macOS, and Windows archives, and
publishes a GitHub prerelease with SHA-256 checksums:

```bash
git switch stage
git pull --ff-only
GITHUB_TOKEN=github_pat_... \
  make stage-release SERVER_URL=https://api.hookspot.dev
```

`GITHUB_TOKEN` must have permission to create releases in this repository. The
working tree must be clean, and the stage branch and its tags must be up to
date. The command creates and pushes a SemVer-compatible tag from the repository
commit count and short commit hash, for example `v0.0.42-stage.g1a2b3c4`, then
GoReleaser builds and publishes the release from inside its Docker container.

Validate `.goreleaser.yaml` without publishing anything with:

```bash
make release-check
```
