# Phoenix Channels support in the websocket client

## Problem

`internal/ws/client.go` dials a websocket, sets `X-CLI-KEY`, and reads raw text
frames. The server now exposes the CLI stream as a Phoenix Channel, which speaks
a framed protocol (topic join, heartbeats, reply matching). The client must speak
that protocol to receive forwarded webhook events.

## Protocol parameters

- **Serializer:** Phoenix V2 — every frame is a JSON array
  `[join_ref, ref, topic, event, payload]`. `join_ref` and `ref` are strings (or
  `null` for server pushes); `payload` is an arbitrary JSON object.
- **Version query:** `?vsn=2.0.0` on the websocket URL.
- **Auth:** `X-CLI-KEY` header (unchanged), read server-side via
  `connect_info: [:x_headers]`.
- **Heartbeat:** client sends `[null, "<ref>", "phoenix", "heartbeat", {}]`
  every 30s.
- **Socket path:** `/cli/websocket` (unchanged base), so the full URL is
  `ws://host/cli/websocket?vsn=2.0.0`.
- **Topic:** `project:<project_uid>`.
- **Join payload:** `{"sources": ["<source>", ...]}` (empty list = all sources).
- **Event carrying webhooks:** `delivery_attempt.created`.

## Design

### Connection flow (`Listen`)

1. Dial `ws://host/cli/websocket?vsn=2.0.0` with the `X-CLI-KEY` header.
2. Send join: `["1", "1", "project:<uid>", "phx_join", {"sources": [...]}]`
   (join_ref `"1"`, ref `"1"`).
3. Read frames until the matching `phx_reply` (ref `"1"`, event `phx_reply`).
   If `payload.status != "ok"`, return an error.
4. Spawn a heartbeat goroutine: every 30s send
   `[null, "<n>", "phoenix", "heartbeat", {}]` with an incrementing ref.
5. Read loop — dispatch each frame by event:
   - `delivery_attempt.created` → `handler(payload)` where `payload` is the raw
     JSON bytes of frame index 4.
   - `phx_error` / `phx_close` on our topic → return an error.
   - anything else (heartbeat `phx_reply` acks, etc.) → ignore.

The handler contract `func(message []byte) error` is unchanged but now receives
the **event payload bytes**, not the whole frame.

### Concurrency

gorilla/websocket permits one concurrent reader and one concurrent writer. The
join write completes before the read loop begins; afterward the heartbeat
goroutine is the only writer, so no write mutex is required. The heartbeat
goroutine and the existing ctx-cancellation goroutine stop via the `done`
channel / ctx, matching the current cleanup pattern.

### Components

- **`internal/ws/message.go`** (new): a `message` struct
  `{JoinRef, Ref *string; Topic, Event string; Payload json.RawMessage}` with
  `encode() ([]byte, error)` and `decode([]byte) (message, error)` for the V2
  array format. Pure and unit-tested.
- **`internal/ws/client.go`**: `New(url, cliKey, topic string, sources []string)`
  (now carries join config) and the `Listen` orchestration above.
- **`cmd/listen.go`**: build the URL as `…/cli/websocket?vsn=2.0.0` (project and
  source are no longer query params), set `topic := "project:" + cfg.Project`,
  and call `ws.New(wsURL, cfg.CLIKey, topic, sources)`.

### Error handling

Join failure, `phx_error`/`phx_close`, and read errors all return from `Listen`.
`cmd/listen.go`'s existing reconnect loop sleeps 2s and retries. Context
cancellation returns `ctx.Err()`, as today. The client is reusable across
reconnects: each `Listen` call opens a fresh connection and re-joins.

## Testing

- **`internal/ws/message_test.go`**: round-trip a client frame (string refs)
  through `encode`/`decode`; decode a server push with `null` refs and assert
  topic/event/payload extraction.
- **`internal/ws/client_test.go`** (rewrite): httptest server that upgrades to a
  websocket and speaks Phoenix V2 —
  - asserts it receives a `phx_join` on `project:proj_1` with the expected
    `sources`,
  - replies `phx_reply` with `status: "ok"`,
  - pushes a `delivery_attempt.created` and asserts the handler receives the
    payload bytes,
  - a separate case where the join reply is `status: "error"` and `Listen`
    returns an error.

## Out of scope

- The internal shape of the `delivery_attempt.created` payload and how it maps
  to the forwarded HTTP request. The handler receives the payload bytes and
  `proxy` posts them as today; only the transport changes.
- Configurable heartbeat interval or serializer version (hardcode 30s / V2).
