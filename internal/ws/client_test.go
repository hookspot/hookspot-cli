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
