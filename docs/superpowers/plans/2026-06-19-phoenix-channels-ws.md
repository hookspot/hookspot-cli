# Phoenix Channels websocket support Implementation Plan

> **Superseded:** This document preserves the original implementation history. See the current [README](../../../README.md) and governing [CLI releases plan](2026-09-05-cli-releases.md).

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `internal/ws` speak the Phoenix Channels V2 protocol so `hookspot listen` receives forwarded webhook events from the Phoenix server.

**Architecture:** A new `message.go` encodes/decodes the V2 array frame format. `client.go` gains join config and orchestrates dial → `phx_join` → heartbeat → read loop, dispatching `delivery_attempt.created` payloads to the handler. `cmd/listen.go` builds the `vsn=2.0.0` URL and passes topic + sources instead of query params.

**Tech Stack:** Go, `github.com/gorilla/websocket`, `encoding/json`, `net/http/httptest` for tests.

## Global Constraints

- Module path is `hookspot`. Imports use `hookspot/internal/...`.
- Phoenix V2 serializer: every frame is a JSON array `[join_ref, ref, topic, event, payload]`. `join_ref`/`ref` are JSON strings or `null`; `payload` is a JSON object.
- Websocket URL: `ws://host/cli/websocket?vsn=2.0.0`. Auth via `X-CLI-KEY` header.
- Topic: `project:<project_uid>`. Join payload: `{"sources": [...]}` (empty list allowed).
- Webhook-carrying event name: `delivery_attempt.created`. Heartbeat: every 30s, `[null, "<ref>", "phoenix", "heartbeat", {}]`.
- The handler contract stays `func(message []byte) error`; it now receives the event payload bytes (frame index 4).
- Run tests/vet/build via the Makefile (runs in Docker): `make test`, `make vet`. `make build` requires `SERVER_URL=...`. Do NOT call `go` directly.
- Follow existing code style: errors wrapped with `%w`, `fmt.Errorf`, table-free.

---

### Task 1: V2 frame encode/decode (`message.go`)

**Files:**
- Create: `internal/ws/message.go`
- Test: `internal/ws/message_test.go`

**Interfaces:**
- Consumes: nothing (pure functions).
- Produces:
  - `type message struct { JoinRef *string; Ref *string; Topic string; Event string; Payload json.RawMessage }`
  - `func encode(m message) ([]byte, error)` — marshals to `[join_ref, ref, topic, event, payload]`. A nil `JoinRef`/`Ref` serializes as JSON `null`. A nil `Payload` serializes as `{}`.
  - `func decode(data []byte) (message, error)` — parses a V2 array frame back into `message`.

- [ ] **Step 1: Write the failing tests**

Create `internal/ws/message_test.go`:

```go
package ws

import (
	"encoding/json"
	"testing"
)

func strPtr(s string) *string { return &s }

func TestEncode_ClientFrameWithStringRefs(t *testing.T) {
	m := message{
		JoinRef: strPtr("1"),
		Ref:     strPtr("1"),
		Topic:   "project:proj_1",
		Event:   "phx_join",
		Payload: json.RawMessage(`{"sources":["stripe"]}`),
	}

	data, err := encode(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	want := `["1","1","project:proj_1","phx_join",{"sources":["stripe"]}]`
	if string(data) != want {
		t.Fatalf("encode = %s, want %s", data, want)
	}
}

func TestEncode_NilRefsAndPayload(t *testing.T) {
	m := message{Topic: "phoenix", Event: "heartbeat"}

	data, err := encode(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	want := `[null,null,"phoenix","heartbeat",{}]`
	if string(data) != want {
		t.Fatalf("encode = %s, want %s", data, want)
	}
}

func TestDecode_ServerPushWithNullRefs(t *testing.T) {
	data := []byte(`[null,null,"project:proj_1","delivery_attempt.created",{"id":"da_1"}]`)

	m, err := decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if m.JoinRef != nil || m.Ref != nil {
		t.Fatalf("refs = %v/%v, want nil/nil", m.JoinRef, m.Ref)
	}
	if m.Topic != "project:proj_1" {
		t.Fatalf("topic = %q, want %q", m.Topic, "project:proj_1")
	}
	if m.Event != "delivery_attempt.created" {
		t.Fatalf("event = %q, want %q", m.Event, "delivery_attempt.created")
	}
	if string(m.Payload) != `{"id":"da_1"}` {
		t.Fatalf("payload = %s, want %s", m.Payload, `{"id":"da_1"}`)
	}
}

func TestDecode_ReplyWithStringRef(t *testing.T) {
	data := []byte(`["1","1","project:proj_1","phx_reply",{"status":"ok","response":{}}]`)

	m, err := decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if m.Ref == nil || *m.Ref != "1" {
		t.Fatalf("ref = %v, want \"1\"", m.Ref)
	}
	if m.Event != "phx_reply" {
		t.Fatalf("event = %q, want phx_reply", m.Event)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `make test`
Expected: FAIL — build error, `message`, `encode`, `decode` undefined in package `ws`.

- [ ] **Step 3: Implement `message.go`**

Create `internal/ws/message.go`:

```go
package ws

import (
	"encoding/json"
	"fmt"
)

// message is a Phoenix Channels V2 frame:
// [join_ref, ref, topic, event, payload].
// JoinRef and Ref are nil for server pushes and serialize as JSON null.
type message struct {
	JoinRef *string
	Ref     *string
	Topic   string
	Event   string
	Payload json.RawMessage
}

// encode marshals m to the V2 array frame. A nil Payload becomes "{}".
func encode(m message) ([]byte, error) {
	payload := m.Payload
	if payload == nil {
		payload = json.RawMessage("{}")
	}
	frame := []interface{}{m.JoinRef, m.Ref, m.Topic, m.Event, payload}
	return json.Marshal(frame)
}

// decode parses a V2 array frame into a message.
func decode(data []byte) (message, error) {
	var frame []json.RawMessage
	if err := json.Unmarshal(data, &frame); err != nil {
		return message{}, fmt.Errorf("decode frame: %w", err)
	}
	if len(frame) != 5 {
		return message{}, fmt.Errorf("decode frame: got %d elements, want 5", len(frame))
	}

	var m message
	if err := json.Unmarshal(frame[0], &m.JoinRef); err != nil {
		return message{}, fmt.Errorf("decode join_ref: %w", err)
	}
	if err := json.Unmarshal(frame[1], &m.Ref); err != nil {
		return message{}, fmt.Errorf("decode ref: %w", err)
	}
	if err := json.Unmarshal(frame[2], &m.Topic); err != nil {
		return message{}, fmt.Errorf("decode topic: %w", err)
	}
	if err := json.Unmarshal(frame[3], &m.Event); err != nil {
		return message{}, fmt.Errorf("decode event: %w", err)
	}
	m.Payload = frame[4]
	return m, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `make test`
Expected: PASS for `hookspot/internal/ws` (4 new tests). Note: the existing `client_test.go` is rewritten in Task 2; until then it still compiles against the old `New`/`Listen`, so the package builds.

- [ ] **Step 5: Commit**

```bash
git add internal/ws/message.go internal/ws/message_test.go
git commit -m "Add Phoenix V2 frame encode/decode to ws package"
```

---

### Task 2: Phoenix join + heartbeat + dispatch in `client.go`

**Files:**
- Modify: `internal/ws/client.go` (whole file — `New` signature and `Listen` body)
- Test: `internal/ws/client_test.go` (rewrite to speak Phoenix V2)

**Interfaces:**
- Consumes: `message`, `encode`, `decode` from Task 1; `github.com/gorilla/websocket`.
- Produces:
  - `func New(url, cliKey, topic string, sources []string) *Client`
  - `func (c *Client) Listen(ctx context.Context, handler func(message []byte) error) error` — unchanged signature; handler receives the payload bytes of `delivery_attempt.created` events.

- [ ] **Step 1: Rewrite the test to speak Phoenix V2**

Replace the entire contents of `internal/ws/client_test.go`:

```go
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var errStop = errors.New("stop after first message")

// readFrame reads one text frame and decodes it as a Phoenix V2 message.
func readFrame(t *testing.T, conn *websocket.Conn) message {
	t.Helper()
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	m, err := decode(data)
	if err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	return m
}

func TestClient_Listen_JoinsAndReceivesEvent(t *testing.T) {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-CLI-KEY"); got != "test-key" {
			t.Errorf("X-CLI-KEY = %q, want %q", got, "test-key")
		}
		if got := r.URL.Query().Get("vsn"); got != "2.0.0" {
			t.Errorf("vsn = %q, want 2.0.0", got)
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		// Expect the join frame.
		join := readFrame(t, conn)
		if join.Event != "phx_join" {
			t.Errorf("event = %q, want phx_join", join.Event)
		}
		if join.Topic != "project:proj_1" {
			t.Errorf("topic = %q, want project:proj_1", join.Topic)
		}
		var joinPayload struct {
			Sources []string `json:"sources"`
		}
		if err := json.Unmarshal(join.Payload, &joinPayload); err != nil {
			t.Errorf("unmarshal join payload: %v", err)
		}
		if len(joinPayload.Sources) != 1 || joinPayload.Sources[0] != "stripe" {
			t.Errorf("sources = %v, want [stripe]", joinPayload.Sources)
		}

		// Reply ok, then push an event.
		reply, _ := encode(message{
			JoinRef: join.JoinRef,
			Ref:     join.Ref,
			Topic:   join.Topic,
			Event:   "phx_reply",
			Payload: json.RawMessage(`{"status":"ok","response":{}}`),
		})
		if err := conn.WriteMessage(websocket.TextMessage, reply); err != nil {
			t.Errorf("write reply: %v", err)
		}

		push, _ := encode(message{
			Topic:   "project:proj_1",
			Event:   "delivery_attempt.created",
			Payload: json.RawMessage(`{"id":"da_1"}`),
		})
		if err := conn.WriteMessage(websocket.TextMessage, push); err != nil {
			t.Errorf("write push: %v", err)
		}

		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/cli/websocket?vsn=2.0.0"

	client := New(wsURL, "test-key", "project:proj_1", []string{"stripe"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	received := make(chan string, 1)
	err := client.Listen(ctx, func(message []byte) error {
		received <- string(message)
		return errStop
	})

	if !errors.Is(err, errStop) {
		t.Fatalf("Listen error = %v, want %v", err, errStop)
	}

	select {
	case msg := <-received:
		if msg != `{"id":"da_1"}` {
			t.Fatalf("payload = %q, want %q", msg, `{"id":"da_1"}`)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestClient_Listen_JoinErrorReturns(t *testing.T) {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		join := readFrame(t, conn)
		reply, _ := encode(message{
			JoinRef: join.JoinRef,
			Ref:     join.Ref,
			Topic:   join.Topic,
			Event:   "phx_reply",
			Payload: json.RawMessage(`{"status":"error","response":{"reason":"unauthorized"}}`),
		})
		if err := conn.WriteMessage(websocket.TextMessage, reply); err != nil {
			t.Errorf("write reply: %v", err)
		}

		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/cli/websocket?vsn=2.0.0"

	client := New(wsURL, "test-key", "project:proj_1", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Listen(ctx, func([]byte) error { return nil })
	if err == nil {
		t.Fatal("Listen error = nil, want join error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `make test`
Expected: FAIL — `New` now takes 4 args; current `New(url, cliKey)` won't compile, and join/dispatch behavior is absent.

- [ ] **Step 3: Rewrite `client.go`**

Replace the entire contents of `internal/ws/client.go`:

```go
package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

const (
	joinRef           = "1"
	heartbeatInterval = 30 * time.Second
	deliveryEvent     = "delivery_attempt.created"
)

// Client connects to a hookspot Phoenix Channel and streams events.
type Client struct {
	url     string
	cliKey  string
	topic   string
	sources []string
}

// New returns a Client that connects to url, authenticates with cliKey, and
// joins topic, requesting the given sources.
func New(url, cliKey, topic string, sources []string) *Client {
	return &Client{url: url, cliKey: cliKey, topic: topic, sources: sources}
}

// Listen connects, joins the channel, and invokes handler with the payload of
// each delivery_attempt.created event. It blocks until handler returns an
// error, the channel errors/closes, or ctx is cancelled.
func (c *Client) Listen(ctx context.Context, handler func(message []byte) error) error {
	header := http.Header{}
	if c.cliKey != "" {
		header.Set("X-CLI-KEY", c.cliKey)
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.url, header)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", c.url, err)
	}
	defer conn.Close()

	if err := c.join(conn); err != nil {
		return err
	}

	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	go c.heartbeat(conn, done)

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read message: %w", err)
		}

		msg, err := decode(data)
		if err != nil {
			return err
		}

		switch msg.Event {
		case deliveryEvent:
			if err := handler(msg.Payload); err != nil {
				return err
			}
		case "phx_error", "phx_close":
			if msg.Topic == c.topic {
				return fmt.Errorf("channel %s: received %s", c.topic, msg.Event)
			}
		}
	}
}

// join sends phx_join and waits for the matching phx_reply.
func (c *Client) join(conn *websocket.Conn) error {
	sources := c.sources
	if sources == nil {
		sources = []string{}
	}
	payload, err := json.Marshal(struct {
		Sources []string `json:"sources"`
	}{Sources: sources})
	if err != nil {
		return fmt.Errorf("encode join payload: %w", err)
	}

	ref := joinRef
	join := message{
		JoinRef: &ref,
		Ref:     &ref,
		Topic:   c.topic,
		Event:   "phx_join",
		Payload: payload,
	}
	frame, err := encode(join)
	if err != nil {
		return fmt.Errorf("encode join: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		return fmt.Errorf("send join: %w", err)
	}

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read join reply: %w", err)
		}
		msg, err := decode(data)
		if err != nil {
			return err
		}
		if msg.Event != "phx_reply" || msg.Ref == nil || *msg.Ref != joinRef {
			continue
		}

		var reply struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(msg.Payload, &reply); err != nil {
			return fmt.Errorf("decode join reply: %w", err)
		}
		if reply.Status != "ok" {
			return fmt.Errorf("join %s rejected: %s", c.topic, msg.Payload)
		}
		return nil
	}
}

// heartbeat sends a Phoenix heartbeat every heartbeatInterval until done closes.
func (c *Client) heartbeat(conn *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	ref := 1
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			ref++
			r := strconv.Itoa(ref)
			frame, err := encode(message{
				Ref:   &r,
				Topic: "phoenix",
				Event: "heartbeat",
			})
			if err != nil {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
				return
			}
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `make test`
Expected: PASS — `hookspot/internal/ws` (Task 1 message tests + the two new client tests). The build of `cmd` will fail here because `cmd/listen.go` still calls `ws.New` with 2 args; that is fixed in Task 3. Confirm the `ws` package tests themselves pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ws/client.go internal/ws/client_test.go
git commit -m "Speak Phoenix Channels V2 protocol in ws client"
```

---

### Task 3: Wire `cmd/listen.go` to the new client

**Files:**
- Modify: `cmd/listen.go` (the wsURL construction and `ws.New` call)

**Interfaces:**
- Consumes: `ws.New(url, cliKey, topic string, sources []string) *Client` and `(*Client).Listen` from Task 2.
- Produces: no new exported symbols.

- [ ] **Step 1: Update the URL and `ws.New` call**

In `cmd/listen.go`, the current block is:

```go
		query := url.Values{}
		query.Set("project", cfg.Project)
		for _, source := range sources {
			query.Add("source", source)
		}

		wsURL := strings.Replace(srvURL, "http", "ws", 1) + "/cli/websocket?" + query.Encode()

		wsClient := ws.New(wsURL, cfg.CLIKey)
```

Replace it with:

```go
		wsURL := strings.Replace(srvURL, "http", "ws", 1) + "/cli/websocket?vsn=2.0.0"
		topic := "project:" + cfg.Project

		wsClient := ws.New(wsURL, cfg.CLIKey, topic, sources)
```

- [ ] **Step 2: Remove the now-unused `net/url` import**

In `cmd/listen.go`, the import block currently includes `"net/url"`. After Step 1 nothing uses `url.Values`, so remove that line. The import block becomes:

```go
import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hookspot/internal/api"
	"hookspot/internal/config"
	"hookspot/internal/proxy"
	"hookspot/internal/ws"
)
```

- [ ] **Step 3: Verify build and the full suite**

Run: `make vet && make test`
Expected: vet clean; all packages build; all tests PASS (including `hookspot/internal/ws`). `cmd` has no test files but must compile.

- [ ] **Step 4: Commit**

```bash
git add cmd/listen.go
git commit -m "Wire listen command to Phoenix channel topic and sources"
```

---

## Self-Review notes

- **Spec coverage:** Task 1 = `message.go` encode/decode + tests (V2 serializer). Task 2 = join, heartbeat, read-loop dispatch of `delivery_attempt.created`, `phx_error`/`phx_close` handling, `phx_reply` status check, `New` join config, concurrency (single writer = heartbeat goroutine), and the rewritten client test. Task 3 = `cmd/listen.go` URL/topic/sources wiring. All spec sections map to a task.
- **Type consistency:** `message{JoinRef, Ref *string; Topic, Event string; Payload json.RawMessage}`, `encode`/`decode`, and `New(url, cliKey, topic string, sources []string)` are used identically across Tasks 1–3.
- **Make-not-go:** every verification step uses `make test` / `make vet` per the global constraint.
- **Known cross-task build gap:** after Task 2 the `cmd` package won't compile until Task 3 updates the `ws.New` call. Each task's `ws`-package tests still pass; the repo fully builds again at the end of Task 3. This is called out in the relevant steps.
