# hookspot-cli

A companion CLI for hookspot. Connects to your hookspot project over a
websocket and forwards incoming webhook events to a local host and port.

## Usage

### Authenticate

Generate a personal access token from the hookspot UI, then either:

```bash
hookspot-cli login
```

or set it via environment variable (recommended for Docker / CI):

```bash
export HOOKSPOT_TOKEN=hk_...
```

### Select a project

```bash
hookspot-cli project list
hookspot-cli project use <project-id>
```

Or set `HOOKSPOT_PROJECT=<project-id>`.

### Forward events to a local server

```bash
# All sources in the active project, forwarded to http://localhost:3000/webhooks
hookspot-cli listen 3000 --path /webhooks

# Only specific sources
hookspot-cli listen 3000 my-source --path /webhooks
```

## Configuration

Settings are resolved in this order: command-line flags, environment
variables (`HOOKSPOT_TOKEN`, `HOOKSPOT_PROJECT`, `HOOKSPOT_SERVER_URL`,
`HOOKSPOT_LOG_LEVEL`), the config file (`~/.config/hookspot-cli/config.toml`
by default, override with `--config`), then built-in defaults.

## Running in Docker

```bash
docker run --rm \
  -e HOOKSPOT_TOKEN=hk_... \
  -e HOOKSPOT_PROJECT=proj_... \
  --network host \
  hookspot-cli:dev listen 3000 --path /webhooks
```

`--network host` is Linux-only and isn't available on Docker Desktop for
Mac/Windows. On those platforms, omit `--network host` and instead pass
`--forward-host host.docker.internal` (default `localhost`) so the
container can reach a server running on your host machine:

```bash
docker run --rm \
  -e HOOKSPOT_TOKEN=hk_... \
  -e HOOKSPOT_PROJECT=proj_... \
  --add-host host.docker.internal:host-gateway \
  hookspot-cli:dev listen 3000 --path /webhooks --forward-host host.docker.internal
```

## Running with Docker Compose

A `docker-compose.yml` is provided for building and running `hookspot-cli`
without a local Go toolchain.

```bash
docker compose build

# Set credentials via env vars (or a .env file) before running
export HOOKSPOT_TOKEN=hk_...
export HOOKSPOT_PROJECT=proj_...

docker compose run --rm hookspot-cli login
docker compose run --rm hookspot-cli project list
docker compose run --rm hookspot-cli listen 3000 --path /webhooks --forward-host host.docker.internal
```

`host.docker.internal` (mapped via `extra_hosts` in `docker-compose.yml`)
lets the container reach services running on your host machine — use it as
the `--forward-host` when the target server (e.g. `localhost:3000`) runs
outside the container.

## Development

All builds/tests run via Docker through the Makefile:

```bash
make build   # compile-check
make test    # run tests
make run ARGS="listen --help"
```
