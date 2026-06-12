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

## Development

All builds/tests run via Docker through the Makefile:

```bash
make build   # compile-check
make test    # run tests
make run ARGS="listen --help"
```
