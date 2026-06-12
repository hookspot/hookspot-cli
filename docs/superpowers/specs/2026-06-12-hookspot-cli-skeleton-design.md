# hookspot-cli skeleton design

## Purpose

`hookspot-cli` is a companion CLI for the hookspot project. Its core job: open a
websocket connection to the hookspot server, receive webhook events, and proxy
(forward) each event as an HTTP request to a local `host:port` the developer
specifies. This mirrors the workflow of tools like the Hookdeck CLI's `listen`
command.

This skeleton focuses on command structure, config handling, and project
layout, with placeholder implementations for the websocket client and HTTP
proxy — real wiring happens once the hookspot server exposes the corresponding
endpoints.

## Auth approach: Personal Access Token (PAT)

hookspot currently only has UI-based auth (Phoenix session login). Rather than
build a browser-pairing/device flow now, the CLI uses a PAT model:

- hookspot UI gains an "API Tokens" section where a logged-in user generates a
  token (shown once, stored hashed server-side). **Out of scope for this repo**
  — tracked as a follow-up on the hookspot (Phoenix) side.
- `hookspot-cli login` prompts for the token interactively (or reads
  `--token` / `HOOKSPOT_TOKEN`), validates it against an `/api/me`-style
  endpoint, and saves it to the config file.
- `hookspot-cli logout` clears the stored token.

**Why PAT over browser-pairing:** the CLI must run inside Docker containers,
where there's no browser to open. PAT auth works trivially in containers via
`HOOKSPOT_TOKEN` env var with zero interactive steps. A browser-pairing flow
(like Hookdeck/Heroku) can be added later as an *additional* login method
without changing the `listen`/`project` command surface.

## Command structure

- **`hookspot-cli login`** — prompt for / validate / store a PAT
- **`hookspot-cli logout`** — clear stored token
- **`hookspot-cli project list`** — list projects accessible to the token
- **`hookspot-cli project use <project>`** — set the active project (persisted
  in config)
- **`hookspot-cli listen <port> [source...] [flags]`** — open websocket to
  hookspot, receive events, proxy each to `http://localhost:<port>/<path>`
  - No `source` args → listen to **all sources in the active project**
  - One or more `source` args → scoped to those sources only
  - `--path` (default `/`) — forwarding path
  - `--project` — override active project for this invocation only
- **`hookspot-cli version`** — print CLI version

## File layout

```
hookspot-cli/
├── go.mod
├── main.go
├── cmd/
│   ├── root.go       # root cmd, persistent flags: --config, --token, --server-url, --log-level
│   ├── login.go
│   ├── logout.go
│   ├── project.go    # `project list` / `project use` subcommands
│   ├── listen.go
│   └── version.go
├── internal/
│   ├── config/        # Viper config load/save
│   ├── api/            # REST client: auth check, list projects/sources
│   ├── ws/              # websocket client (event stream) — placeholder
│   └── proxy/           # forwards received events to local target — placeholder
├── Dockerfile           # multi-stage build → static binary, CGO_ENABLED=0
└── README.md
```

## Config & Docker-friendliness

- Config file: `~/.config/hookspot-cli/config.toml` (Viper-managed)
- Path override via `--config` flag or `HOOKSPOT_CONFIG_FILE` env var
- Precedence: flags > env vars (`HOOKSPOT_TOKEN`, `HOOKSPOT_PROJECT`,
  `HOOKSPOT_SERVER_URL`) > config file > defaults
- In Docker: `docker run -e HOOKSPOT_TOKEN=... -e HOOKSPOT_PROJECT=... image
  listen 3000` works with no `login` step or mounted config
- `Dockerfile`: multi-stage build producing a static binary
  (`CGO_ENABLED=0`) on an `alpine` or `scratch` final image

## Data flow (listen command, placeholder behavior)

1. Resolve token (flag/env/config) and active project (flag/config)
2. `internal/api` validates the token and resolves source list (all sources
   in project, or the ones named as args)
3. `internal/ws` opens a websocket connection to the hookspot server scoped to
   those sources (placeholder: connects to a configurable `--server-url`,
   logs received messages)
4. `internal/proxy` takes each received event and forwards it as an HTTP
   request to `http://localhost:<port><path>` (placeholder: simple
   `net/http` POST with the event body)
5. Errors (connection drop, proxy failure) are logged; `listen` retries the
   websocket connection with backoff (placeholder: simple retry loop, no
   exponential backoff tuning yet)

## Testing

- `internal/config`: unit tests for precedence (flag > env > file > default)
- `internal/proxy`: unit test forwarding a mock event to an `httptest.Server`
- `cmd/`: smoke test that `--help` works for all commands and flags are wired
  correctly (cobra's built-in testing patterns)
- `internal/ws` and `internal/api`: skeleton interfaces only, no real network
  tests yet (mocked in unit tests where logic exists)

## Out of scope (future work)

- Browser-pairing/device-flow login (v2 auth method)
- hookspot server-side API endpoints (`/api/me`, project/source listing,
  websocket event stream) — tracked separately on the hookspot repo
- Real websocket protocol details (message format, reconnection semantics)
- Output formatting modes (interactive/compact/quiet) like Hookdeck's
