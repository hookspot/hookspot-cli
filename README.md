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
hookspot project use <project-id>
```

Or set `HOOKSPOT_PROJECT=<project-id>`.

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
variables (`HOOKSPOT_CLI_KEY`, `HOOKSPOT_PROJECT`, `HOOKSPOT_LOG_LEVEL`), the
config file (`~/.config/hookspot/config.toml` by default, override with
`--config`), then built-in defaults.

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
  -e HOOKSPOT_PROJECT=proj_... \
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
  -e HOOKSPOT_PROJECT=proj_... \
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
export HOOKSPOT_PROJECT=proj_...

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
HOOKSPOT_CLI_KEY=hk_... HOOKSPOT_PROJECT=proj_... make dev
make dev ARGS="listen --help"
```
