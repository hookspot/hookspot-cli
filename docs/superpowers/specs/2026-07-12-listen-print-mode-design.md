# `hookspot listen`: print-only default, forwarding via `--forward-to`

Date: 2026-07-12
Status: approved
Supersedes: 2026-07-12-listen-port-flag-design.md

## Problem

Inspecting incoming webhooks currently requires a local server to forward
to. Like `stripe listen`, the CLI should print deliveries to the terminal
by default and treat forwarding as opt-in.

## CLI interface

- `hookspot listen [source...]` — print each delivery to stdout, ack the
  server with 200. No local server needed.
- `hookspot listen --forward-to <url> [source...]` — print each delivery
  AND forward it; the response line shows the local server's status.
- `--forward-to` is a base URL: the delivery's own path is appended to it
  (existing `proxy.Forwarder` behavior). A missing scheme defaults to
  `http://`, so `--forward-to localhost:3000` works.
- `--print-body` (bool) — also dump the request body; summary lines only
  by default.
- `--port` and `--forward-host` are removed. Both were added earlier today
  and never released, so no deprecation shim.

Output format (stripe-like), one line per event:

```
12:34:56 --> POST /webhooks/stripe [attempt-uid]
12:34:57 <-- [200] POST /webhooks/stripe
```

Print-only mode emits only the `-->` line and acks 200 with empty body.

## Components

- `internal/ws`: add `type Handler func(Delivery) (Response, error)`;
  `Client.Listen` takes it (same signature, now named).
- `internal/printer` (new): terminal renderer.
  - `New(out io.Writer, printBody bool) *Printer` — time source is a
    struct field defaulting to `time.Now`, overridable in tests.
  - `Handle(d ws.Delivery) (ws.Response, error)` — prints the `-->` line
    (plus body when enabled), returns `Response{Status: 200}`.
  - `Wrap(next ws.Handler) ws.Handler` — prints `-->` before and
    `<-- [status]` after the wrapped handler.
- `cmd/listen.go`: builds `printer.Handle` (no `--forward-to`) or
  `printer.Wrap(forwardHandler)` (with it). The forward handler keeps the
  existing 502-on-error behavior; its `forwarded event -> %d` print moves
  into the printer's `<--` line. Startup message says either
  "printing deliveries (pass --forward-to to forward)" or
  "forwarding to <url>".

## Docs

README usage/Docker/compose examples and `.air.toml` switch from
`--port 3000 --forward-host X` to `--forward-to http://X:3000`; the
phantom `--path` flag disappears from examples. Add a print-only example.

## Testing

`make test` / `make vet`. Unit tests: printer output for Handle and Wrap
(with and without `--print-body`), scheme normalization for
`--forward-to`. Manual check via `make run ARGS="listen --help"`.
