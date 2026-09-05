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

func TestClient_Listen_ForwardsAndRepliesWithResponse(t *testing.T) {
	upgrader := websocket.Upgrader{}

	gotResponse := make(chan deliveryResponse, 1)

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

		// Reply ok, then push a delivery.
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

		deliveryPayload, _ := json.Marshal(Delivery{
			AttemptUID: "att_1",
			RequestUID: "req_1",
			SourceUID:  "src_1",
			Method:     "POST",
			Path:       "/webhooks/stripe",
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Query:      "a=1",
			Body:       []byte(`{"k":1}`),
		})
		push, _ := encode(message{
			Topic:   "project:proj_1",
			Event:   "delivery",
			Payload: deliveryPayload,
		})
		if err := conn.WriteMessage(websocket.TextMessage, push); err != nil {
			t.Errorf("write push: %v", err)
		}

		// Expect the delivery_response push back.
		respFrame := readFrame(t, conn)
		if respFrame.Event != "delivery_response" {
			t.Errorf("event = %q, want delivery_response", respFrame.Event)
		}
		if respFrame.Topic != "project:proj_1" {
			t.Errorf("topic = %q, want project:proj_1", respFrame.Topic)
		}
		var dr deliveryResponse
		if err := json.Unmarshal(respFrame.Payload, &dr); err != nil {
			t.Errorf("unmarshal delivery_response: %v", err)
		}
		gotResponse <- dr

		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/cli/websocket?vsn=2.0.0"

	client := New(wsURL, "test-key", "project:proj_1", []string{"stripe"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	gotDelivery := make(chan Delivery, 1)
	go func() {
		_ = client.Listen(ctx, func(d Delivery) (Response, error) {
			gotDelivery <- d
			return Response{
				Status:    201,
				Headers:   http.Header{"X-Foo": []string{"bar"}},
				Body:      []byte("ok"),
				LatencyMS: 38,
			}, nil
		})
	}()

	select {
	case d := <-gotDelivery:
		if d.AttemptUID != "att_1" {
			t.Fatalf("attempt_uid = %q, want att_1", d.AttemptUID)
		}
		if d.RequestUID != "req_1" {
			t.Fatalf("request_uid = %q, want req_1", d.RequestUID)
		}
		if d.SourceUID != "src_1" {
			t.Fatalf("source_uid = %q, want src_1", d.SourceUID)
		}
		if d.Method != "POST" {
			t.Fatalf("method = %q, want POST", d.Method)
		}
		if d.Path != "/webhooks/stripe" {
			t.Fatalf("path = %q, want /webhooks/stripe", d.Path)
		}
		if d.Query != "a=1" {
			t.Fatalf("query = %q, want a=1", d.Query)
		}
		if got := d.Headers.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		if string(d.Body) != `{"k":1}` {
			t.Fatalf("body = %q, want %q", d.Body, `{"k":1}`)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delivery")
	}

	select {
	case dr := <-gotResponse:
		if dr.AttemptUID != "att_1" {
			t.Fatalf("attempt_uid = %q, want att_1", dr.AttemptUID)
		}
		if dr.Status != 201 {
			t.Fatalf("status = %d, want 201", dr.Status)
		}
		if got := dr.Headers.Get("X-Foo"); got != "bar" {
			t.Fatalf("X-Foo = %q, want bar", got)
		}
		if string(dr.Body) != "ok" {
			t.Fatalf("body = %q, want %q", dr.Body, "ok")
		}
		if dr.LatencyMS != 38 {
			t.Fatalf("latency_ms = %d, want 38", dr.LatencyMS)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delivery_response")
	}
}

func TestDelivery_UnmarshalRemainsCompatibleWithoutRequestUID(t *testing.T) {
	var delivery Delivery
	if err := json.Unmarshal([]byte(`{"attempt_uid":"att_1","source_uid":"src_1","body":""}`), &delivery); err != nil {
		t.Fatalf("unmarshal delivery: %v", err)
	}
	if delivery.RequestUID != "" {
		t.Fatalf("request_uid = %q, want empty", delivery.RequestUID)
	}
	if delivery.SourceUID != "src_1" {
		t.Fatalf("source_uid = %q, want src_1", delivery.SourceUID)
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

	err := client.Listen(ctx, func(Delivery) (Response, error) { return Response{}, nil })
	if err == nil {
		t.Fatal("Listen error = nil, want join error")
	}
	var sessionErr *SessionError
	if !errors.As(err, &sessionErr) {
		t.Fatalf("Listen error = %T %v, want *SessionError", err, err)
	}
	if sessionErr.Kind != SessionAuthentication {
		t.Fatalf("session error kind = %v, want authentication", sessionErr.Kind)
	}
	if sessionErr.Retryable() {
		t.Fatal("authentication error is retryable")
	}
}

func TestClient_Listen_HandshakeAuthenticationErrorReturns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/cli/websocket?vsn=2.0.0"
	err := New(wsURL, "bad-key", "project:proj_1", nil).Listen(
		context.Background(),
		func(Delivery) (Response, error) { return Response{}, nil },
	)

	var sessionErr *SessionError
	if !errors.As(err, &sessionErr) {
		t.Fatalf("Listen error = %T %v, want *SessionError", err, err)
	}
	if sessionErr.Kind != SessionAuthentication {
		t.Fatalf("session error kind = %v, want authentication", sessionErr.Kind)
	}
	if sessionErr.Retryable() {
		t.Fatal("authentication error is retryable")
	}
}

func TestSessionErrorRetryPolicy(t *testing.T) {
	tests := []struct {
		kind      SessionErrorKind
		retryable bool
	}{
		{SessionConnect, true},
		{SessionDisconnected, true},
		{SessionAuthentication, false},
		{SessionProtocol, false},
		{SessionHandler, false},
	}
	for _, test := range tests {
		err := &SessionError{Kind: test.kind, Err: errors.New("failure")}
		if got := err.Retryable(); got != test.retryable {
			t.Errorf("kind %v retryable = %v, want %v", test.kind, got, test.retryable)
		}
	}
}
