package ws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var errStop = errors.New("stop after first message")

func TestClient_Listen_ReceivesMessages(t *testing.T) {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
			t.Errorf("write message: %v", err)
		}

		// Keep the connection open briefly so the client can read
		// before the handler returns and closes it.
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client := New(wsURL, "test-token")

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
		if msg != "hello" {
			t.Fatalf("message = %q, want %q", msg, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
	}
}
