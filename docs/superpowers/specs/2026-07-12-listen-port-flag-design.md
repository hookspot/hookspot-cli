# `hookspot listen`: move port from positional arg to `--port` flag

Date: 2026-07-12
Status: approved

## Problem

`hookspot listen <port> [source...]` requires a port. Users should be able
to forward to the host on its default HTTP port (80) without naming one,
e.g. when `--forward-host` points at a host serving on port 80.

## Design

- Usage becomes `listen [source...]`. All positional args are source
  names (`Args: cobra.ArbitraryArgs`); zero args means all sources.
- New `--port` string flag, default empty.
  - `--port 3000` → forward target `http://<forward-host>:3000`
  - omitted → forward target `http://<forward-host>` (plain HTTP, port 80)
- `--forward-host` (default `localhost`) is unchanged and composes with
  `--port`.
- The "Listening … forwarding to …" startup line prints the actual target
  URL, built once and shared with the proxy.
- Auth checks, project resolution, ws client, delivery handler, and the
  reconnect loop are untouched.

## Breaking change

`listen 3000` no longer means "port 3000" — `3000` is now treated as a
source name, and since sources are matched server-side the symptom is
silently receiving no events. README examples are updated to
`listen --port 3000`.

## Testing

`make test` / `make vet`. The target-URL construction is exercised via a
unit test on the extracted helper.
