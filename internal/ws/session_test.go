package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testWebSocketURL(server *httptest.Server) string {
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/cli/websocket?vsn=2.0.0"
}

func testClientOptions() clientOptions {
	return clientOptions{
		joinTimeout:       100 * time.Millisecond,
		writeTimeout:      100 * time.Millisecond,
		heartbeatInterval: time.Hour,
		receiveIdle:       time.Second,
		maxFrameBytes:     32 * 1024 * 1024,
	}
}

func writeTestMessage(t *testing.T, conn *websocket.Conn, msg message) {
	t.Helper()
	frame, err := encode(msg)
	if err != nil {
		t.Fatalf("encode message: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		t.Fatalf("write message: %v", err)
	}
}

func acceptTestConnection(t *testing.T, w http.ResponseWriter, r *http.Request) *websocket.Conn {
	t.Helper()
	conn, err := new(websocket.Upgrader).Upgrade(w, r, nil)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	return conn
}

func writeJoinAccepted(t *testing.T, conn *websocket.Conn, join message) {
	t.Helper()
	writeTestMessage(t, conn, message{
		JoinRef: join.JoinRef,
		Ref:     join.Ref,
		Topic:   join.Topic,
		Event:   "phx_reply",
		Payload: json.RawMessage(`{"status":"ok","response":{}}`),
	})
}

func backendDelivery(attemptUID, method, body string) json.RawMessage {
	return json.RawMessage(`{"attempt_uid":"` + attemptUID + `","request_uid":"req_1","source_uid":"src_1","method":"` + method + `","path":"","headers":{},"query":"","body":"` + body + `"}`)
}

func TestClientJoinWithoutReplyTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := acceptTestConnection(t, w, r)
		defer conn.Close()
		_ = readFrame(t, conn)
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	options := testClientOptions()
	options.joinTimeout = 30 * time.Millisecond
	err := newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, options).
		Listen(context.Background(), func(Delivery) (Response, error) { return Response{}, nil })
	var sessionErr *SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Kind != SessionConnect {
		t.Fatalf("Listen error = %T %v, want connection timeout", err, err)
	}
}

func TestClientCancellationInterruptsJoin(t *testing.T) {
	joinSeen := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := acceptTestConnection(t, w, r)
		defer conn.Close()
		_ = readFrame(t, conn)
		close(joinSeen)
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	options := testClientOptions()
	options.joinTimeout = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, options).
			Listen(ctx, func(Delivery) (Response, error) { return Response{}, nil })
	}()
	<-joinSeen
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Listen error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Listen did not return after cancellation during join")
	}
}

func TestClientMalformedJoinFrameIsFatalProtocolError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := acceptTestConnection(t, w, r)
		defer conn.Close()
		_ = readFrame(t, conn)
		_ = conn.WriteMessage(websocket.TextMessage, []byte("malformed-frame-sentinel"))
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	err := newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, testClientOptions()).
		Listen(context.Background(), func(Delivery) (Response, error) { return Response{}, nil })
	var sessionErr *SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Kind != SessionProtocol || sessionErr.Connected {
		t.Fatalf("Listen error = %#v, want pre-join protocol error", err)
	}
	if strings.Contains(err.Error(), "malformed-frame-sentinel") {
		t.Fatalf("join error exposed raw frame: %v", err)
	}
}

func TestClientJoinRejectionReportsOnlyShortParsedDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := acceptTestConnection(t, w, r)
		defer conn.Close()
		join := readFrame(t, conn)
		writeTestMessage(t, conn, message{
			JoinRef: join.JoinRef,
			Ref:     join.Ref,
			Topic:   join.Topic,
			Event:   "phx_reply",
			Payload: json.RawMessage(`{"status":"error","response":{"reason":"denied\nnext\u001b"},"body":"raw-payload-sentinel"}`),
		})
	}))
	defer server.Close()

	err := newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, testClientOptions()).
		Listen(context.Background(), func(Delivery) (Response, error) { return Response{}, nil })
	var sessionErr *SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Kind != SessionProtocol || sessionErr.Connected {
		t.Fatalf("Listen error = %#v, want pre-join protocol rejection", err)
	}
	if !strings.Contains(err.Error(), `status "error", reason "denied\nnext\x1b"`) {
		t.Fatalf("join rejection omitted safe parsed details: %q", err.Error())
	}
	if strings.Contains(err.Error(), "raw-payload-sentinel") || strings.ContainsRune(err.Error(), '\x1b') {
		t.Fatalf("join rejection exposed raw or active control data: %q", err.Error())
	}
}

func TestClientCorrelatesJoinAndRoutesOnlyJoinedTopicDeliveries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := acceptTestConnection(t, w, r)
		defer conn.Close()
		join := readFrame(t, conn)
		writeTestMessage(t, conn, message{
			JoinRef: join.JoinRef, Ref: join.Ref, Topic: "project:foreign", Event: "phx_reply",
			Payload: json.RawMessage(`{"status":"ok","response":{}}`),
		})
		writeTestMessage(t, conn, message{Topic: join.Topic, Event: deliveryEvent, Payload: backendDelivery("att_before_join", "POST", "")})
		writeJoinAccepted(t, conn, join)
		writeTestMessage(t, conn, message{Topic: "project:foreign", Event: deliveryEvent, Payload: backendDelivery("att_foreign", "POST", "")})
		writeTestMessage(t, conn, message{JoinRef: nil, Topic: join.Topic, Event: deliveryEvent, Payload: backendDelivery("att_valid", "POST", "aGk")})
		response := readFrame(t, conn)
		if response.Event != responseEvent {
			t.Errorf("response event = %q, want %q", response.Event, responseEvent)
		}
		cancel()
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	var calls atomic.Int32
	err := newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, testClientOptions()).Listen(ctx, func(delivery Delivery) (Response, error) {
		calls.Add(1)
		if delivery.AttemptUID != "att_valid" || string(delivery.Body) != "hi" {
			t.Errorf("unexpected delivery: %#v", delivery)
		}
		return Response{Status: http.StatusOK}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Listen error = %v, want context cancellation", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
}

func TestClientInvalidJoinedTopicDeliveryIsFatalWithoutReply(t *testing.T) {
	tests := []struct {
		name    string
		payload json.RawMessage
	}{
		{name: "null", payload: json.RawMessage(`null`)},
		{name: "empty object", payload: json.RawMessage(`{}`)},
		{name: "missing attempt UID", payload: backendDelivery("", "POST", "")},
		{name: "missing method", payload: backendDelivery("att_1", "", "")},
		{name: "malformed Base64", payload: backendDelivery("att_1", "POST", "body-sentinel!")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			replySeen := make(chan bool, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn := acceptTestConnection(t, w, r)
				defer conn.Close()
				join := readFrame(t, conn)
				writeJoinAccepted(t, conn, join)
				writeTestMessage(t, conn, message{Topic: join.Topic, Event: deliveryEvent, Payload: test.payload})
				_, _, err := conn.ReadMessage()
				replySeen <- err == nil
			}))
			defer server.Close()

			var calls atomic.Int32
			err := newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, testClientOptions()).Listen(context.Background(), func(Delivery) (Response, error) {
				calls.Add(1)
				return Response{Status: http.StatusOK}, nil
			})
			var sessionErr *SessionError
			if !errors.As(err, &sessionErr) || sessionErr.Kind != SessionProtocol || !sessionErr.Connected {
				t.Fatalf("Listen error = %#v, want connected protocol error", err)
			}
			if strings.Contains(err.Error(), "body-sentinel") {
				t.Fatalf("protocol error exposed delivery body: %v", err)
			}
			if got := calls.Load(); got != 0 {
				t.Fatalf("handler calls = %d, want 0", got)
			}
			if <-replySeen {
				t.Fatal("invalid delivery received a response")
			}
		})
	}
}

type immediateHeartbeatReplyWriter struct {
	pending *heartbeatTracker
	matched bool
}

func (w *immediateHeartbeatReplyWriter) SetWriteDeadline(time.Time) error { return nil }

func (w *immediateHeartbeatReplyWriter) WriteMessage(_ int, frame []byte) error {
	msg, err := decode(frame)
	if err != nil || msg.Ref == nil {
		return errors.New("invalid heartbeat frame")
	}
	w.matched = w.pending.take(*msg.Ref)
	return nil
}

func TestConnWriterRegistersHeartbeatBeforeWritingFrame(t *testing.T) {
	pending := newHeartbeatTracker(2)
	connection := &immediateHeartbeatReplyWriter{pending: pending}
	writer := &connWriter{conn: connection, timeout: time.Second}
	if _, err := writer.sendHeartbeat(pending); err != nil {
		t.Fatal(err)
	}
	if !connection.matched {
		t.Fatal("immediate heartbeat reply arrived before its ref was registered")
	}
}

func TestHeartbeatTrackerBoundsPendingReferences(t *testing.T) {
	pending := newHeartbeatTracker(2)
	pending.add("1")
	pending.add("2")
	pending.add("3")
	if pending.take("1") {
		t.Fatal("oldest heartbeat ref was not evicted")
	}
	if !pending.take("2") || !pending.take("3") {
		t.Fatal("recent heartbeat refs were not retained")
	}
}

func TestHeartbeatReplyConsumesRefOnlyAfterSuccessfulPayload(t *testing.T) {
	pending := newHeartbeatTracker(2)
	pending.add("7")
	ref := "7"
	invalid := message{Topic: "phoenix", Event: "phx_reply", Ref: &ref, Payload: json.RawMessage(`{"status":"error"}`)}
	if matchingHeartbeatReply(invalid, pending) {
		t.Fatal("failed heartbeat reply was accepted")
	}
	valid := message{Topic: "phoenix", Event: "phx_reply", Ref: &ref, Payload: json.RawMessage(`{"status":"ok","response":{}}`)}
	if !matchingHeartbeatReply(valid, pending) {
		t.Fatal("valid heartbeat reply could not use its pending ref")
	}
}

func TestHandleDeliveryDoesNotStartHandlerAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := new(Client).handleDelivery(ctx, nil, "1", Delivery{AttemptUID: "att_1", Method: "POST"}, func(Delivery) (Response, error) {
		called = true
		return Response{Status: http.StatusOK}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("handleDelivery error = %v, want cancellation", err)
	}
	if called {
		t.Fatal("handler started after cancellation")
	}
}

func TestClientCancellationInterruptsDial(t *testing.T) {
	started := make(chan struct{})
	dialer := *websocket.DefaultDialer
	dialer.Proxy = nil
	dialer.NetDialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	options := testClientOptions()
	options.dialer = &dialer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- newClient("ws://example.invalid/socket", "test-key", "project:proj_1", nil, options).
			Listen(ctx, func(Delivery) (Response, error) { return Response{}, nil })
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Listen error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Listen did not return after cancellation during dial")
	}
}

func TestClientCancellationInterruptsWithheldUpgradeResponse(t *testing.T) {
	requestSeen := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseServer := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestSeen)
		select {
		case <-r.Context().Done():
		case <-release:
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}
	}))
	defer func() {
		releaseServer()
		server.Close()
	}()

	options := testClientOptions()
	options.joinTimeout = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, options).
			Listen(ctx, func(Delivery) (Response, error) { return Response{}, nil })
	}()
	<-requestSeen
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Listen error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		releaseServer()
		<-done
		t.Fatal("Listen did not return after cancellation during WebSocket upgrade")
	}
}

func TestClientCancellationInterruptsRead(t *testing.T) {
	joined := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := acceptTestConnection(t, w, r)
		defer conn.Close()
		join := readFrame(t, conn)
		writeJoinAccepted(t, conn, join)
		close(joined)
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, testClientOptions()).
			Listen(ctx, func(Delivery) (Response, error) { return Response{}, nil })
	}()
	<-joined
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Listen error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Listen did not return after cancellation during read")
	}
}

func TestClientRepeatedCancellationJoinsSessionWorkers(t *testing.T) {
	joined := make(chan struct{})
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := acceptTestConnection(t, w, r)
		defer conn.Close()
		join := readFrame(t, conn)
		writeJoinAccepted(t, conn, join)
		joined <- struct{}{}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				closed <- struct{}{}
				return
			}
		}
	}))
	defer server.Close()

	dialer := *websocket.DefaultDialer
	dialer.Proxy = nil
	originalDial := new(net.Dialer).DialContext
	var dialCalls atomic.Int32
	dialer.NetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialCalls.Add(1)
		return originalDial(ctx, network, address)
	}
	options := testClientOptions()
	options.dialer = &dialer
	options.heartbeatInterval = 5 * time.Millisecond

	const sessions = 10
	for index := 0; index < sessions; index++ {
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			result <- newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, options).
				Listen(ctx, func(Delivery) (Response, error) { return Response{}, nil })
		}()
		<-joined
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("session %d error = %v, want cancellation", index, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("session %d did not stop", index)
		}
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatalf("session %d socket remained open", index)
		}
	}
	if got := dialCalls.Load(); got != sessions {
		t.Fatalf("custom dial calls = %d, want %d", got, sessions)
	}
}

func TestClientMatchingHeartbeatRepliesKeepIdleSessionAlive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	replies := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := acceptTestConnection(t, w, r)
		defer conn.Close()
		join := readFrame(t, conn)
		writeJoinAccepted(t, conn, join)
		for count := 0; count < 8; count++ {
			heartbeat := readFrame(t, conn)
			writeTestMessage(t, conn, message{
				Ref: heartbeat.Ref, Topic: "phoenix", Event: "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{}}`),
			})
		}
		replies <- struct{}{}
		cancel()
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	options := testClientOptions()
	options.heartbeatInterval = 5 * time.Millisecond
	options.receiveIdle = 25 * time.Millisecond
	err := newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, options).
		Listen(ctx, func(Delivery) (Response, error) { return Response{}, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Listen error = %v, want cancellation after healthy replies", err)
	}
	<-replies
}

func TestMalformedAndForeignFramesDoNotRenewReceiveIdle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := acceptTestConnection(t, w, r)
		defer conn.Close()
		join := readFrame(t, conn)
		writeJoinAccepted(t, conn, join)

		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if err := conn.WriteMessage(websocket.TextMessage, []byte("malformed")); err != nil {
				return
			}
			foreignRef := "stale"
			if err := writeMessage(conn, message{
				Ref: &foreignRef, Topic: "phoenix", Event: "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{}}`),
			}); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	options := testClientOptions()
	options.receiveIdle = 40 * time.Millisecond
	started := time.Now()
	err := newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, options).
		Listen(context.Background(), func(Delivery) (Response, error) { return Response{}, nil })
	var sessionErr *SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Kind != SessionDisconnected || !sessionErr.Connected {
		t.Fatalf("Listen error = %#v, want connected receive-idle failure", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("ignored frames kept session alive for %s", elapsed)
	}
}

func writeMessage(conn *websocket.Conn, msg message) error {
	frame, err := encode(msg)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, frame)
}

func TestConnectionCancellationInterruptsBlockedWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sessionDone := make(chan struct{})
	var workers sync.WaitGroup
	client, server := net.Pipe()
	defer server.Close()
	watchSocket(ctx, sessionDone, &workers, client)
	writeDone := make(chan error, 1)
	go func() {
		_, err := client.Write([]byte("frame"))
		writeDone <- err
	}()
	cancel()
	select {
	case err := <-writeDone:
		if err == nil {
			t.Fatal("blocked write succeeded after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt blocked write")
	}
	close(sessionDone)
	workers.Wait()
}

type failingHeartbeatWriter struct{ err error }

func (w failingHeartbeatWriter) SetWriteDeadline(time.Time) error { return nil }
func (w failingHeartbeatWriter) WriteMessage(int, []byte) error   { return w.err }

func TestHeartbeatWriteFailureNotifiesSession(t *testing.T) {
	wantErr := errors.New("write unavailable")
	client := &Client{options: clientOptions{heartbeatInterval: time.Millisecond}}
	done := make(chan struct{})
	failure := make(chan error, 1)
	go client.heartbeat(
		&connWriter{conn: failingHeartbeatWriter{err: wantErr}, timeout: time.Second},
		newHeartbeatTracker(2), done, func(err error) { failure <- err },
	)
	select {
	case err := <-failure:
		if !errors.Is(err, wantErr) {
			t.Fatalf("heartbeat error = %v, want write failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat write failure was not reported")
	}
	close(done)
}

func TestClientSerialSlowDeliveriesRearmIdleThenInactivityExpires(t *testing.T) {
	const deliveryCount = 5
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := acceptTestConnection(t, w, r)
		defer conn.Close()
		join := readFrame(t, conn)
		writeJoinAccepted(t, conn, join)
		for index := 0; index < deliveryCount; index++ {
			attempt := fmt.Sprintf("att_%d", index)
			writeTestMessage(t, conn, message{Topic: join.Topic, Event: deliveryEvent, Payload: backendDelivery(attempt, "POST", "")})
		}
		responses := 0
		pending := make([]message, 0)
		for responses < deliveryCount {
			frame := readFrame(t, conn)
			switch frame.Event {
			case responseEvent:
				responses++
			case "heartbeat":
				pending = append(pending, frame)
			}
		}
		for _, heartbeat := range pending {
			writeTestMessage(t, conn, message{
				Ref: heartbeat.Ref, Topic: "phoenix", Event: "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{}}`),
			})
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	options := testClientOptions()
	options.heartbeatInterval = 10 * time.Millisecond
	options.receiveIdle = 80 * time.Millisecond
	var order []string
	err := newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, options).Listen(context.Background(), func(delivery Delivery) (Response, error) {
		order = append(order, delivery.AttemptUID)
		time.Sleep(30 * time.Millisecond)
		return Response{Status: http.StatusOK}, nil
	})
	var sessionErr *SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Kind != SessionDisconnected || !sessionErr.Connected {
		t.Fatalf("Listen error = %#v, want connected inactivity error", err)
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("Listen error = %v, want receive-idle timeout", err)
	}
	if len(order) != deliveryCount {
		t.Fatalf("handled deliveries = %v, want %d ordered deliveries", order, deliveryCount)
	}
	for index, attempt := range order {
		if attempt != fmt.Sprintf("att_%d", index) {
			t.Fatalf("delivery order = %v", order)
		}
	}
}

func TestClientRejectsFrameAtConfiguredLimitPlusOne(t *testing.T) {
	const limit = int64(256)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := acceptTestConnection(t, w, r)
		defer conn.Close()
		join := readFrame(t, conn)
		writeJoinAccepted(t, conn, join)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", int(limit+1))))
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	options := testClientOptions()
	options.maxFrameBytes = limit
	err := newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, options).
		Listen(context.Background(), func(Delivery) (Response, error) { return Response{}, nil })
	var sessionErr *SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Kind != SessionDisconnected {
		t.Fatalf("Listen error = %#v, want frame-limit disconnect", err)
	}
}

func TestClientHandlerFailureWithoutResponseSendsNoAcknowledgement(t *testing.T) {
	replySeen := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := acceptTestConnection(t, w, r)
		defer conn.Close()
		join := readFrame(t, conn)
		writeJoinAccepted(t, conn, join)
		writeTestMessage(t, conn, message{Topic: join.Topic, Event: deliveryEvent, Payload: backendDelivery("att_1", "POST", "")})
		_, _, err := conn.ReadMessage()
		replySeen <- err == nil
	}))
	defer server.Close()

	wantErr := errors.New("output unavailable")
	err := newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, testClientOptions()).Listen(context.Background(), func(Delivery) (Response, error) {
		return Response{}, wantErr
	})
	var sessionErr *SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Kind != SessionHandler || !errors.Is(err, wantErr) {
		t.Fatalf("Listen error = %#v, want handler output failure", err)
	}
	if <-replySeen {
		t.Fatal("handler failure without response was acknowledged")
	}
}

func TestClientCompletedResponseIsAcknowledgedBeforeHandlerFailure(t *testing.T) {
	responseSeen := make(chan deliveryResponse, 1)
	paddedBody := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := acceptTestConnection(t, w, r)
		defer conn.Close()
		join := readFrame(t, conn)
		writeJoinAccepted(t, conn, join)
		writeTestMessage(t, conn, message{Topic: join.Topic, Event: deliveryEvent, Payload: backendDelivery("att_1", "POST", "")})
		frame := readFrame(t, conn)
		paddedBody <- strings.Contains(string(frame.Payload), `"body":"b2s="`)
		var response deliveryResponse
		if err := json.Unmarshal(frame.Payload, &response); err != nil {
			t.Fatal(err)
		}
		responseSeen <- response
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	wantErr := errors.New("display unavailable")
	err := newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, testClientOptions()).Listen(context.Background(), func(Delivery) (Response, error) {
		return Response{
			Status:  http.StatusTemporaryRedirect,
			Headers: http.Header{"Location": []string{"/next"}},
			Body:    []byte("ok"),
		}, wantErr
	})
	var sessionErr *SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Kind != SessionHandler || !errors.Is(err, wantErr) {
		t.Fatalf("Listen error = %#v, want post-ack handler failure", err)
	}
	response := <-responseSeen
	if response.Status != http.StatusTemporaryRedirect || response.Headers.Get("Location") != "/next" || string(response.Body) != "ok" {
		t.Fatalf("acknowledged response changed: %#v", response)
	}
	if !<-paddedBody {
		t.Fatal("outgoing response body was not padded standard Base64")
	}
}

func TestHandlerFailureTakesPrecedenceOverResponseWriteFailure(t *testing.T) {
	handlerErr := errors.New("display unavailable")
	writeErr := errors.New("connection unavailable")
	writer := &connWriter{conn: failingHeartbeatWriter{err: writeErr}, timeout: time.Second}
	err := new(Client).handleDelivery(
		context.Background(),
		writer,
		"1",
		Delivery{AttemptUID: "att_1", Method: http.MethodPost},
		func(Delivery) (Response, error) {
			return Response{Status: http.StatusOK}, handlerErr
		},
	)
	var sessionErr *SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Kind != SessionHandler || sessionErr.Retryable() {
		t.Fatalf("handleDelivery error = %#v, want nonretryable handler error", err)
	}
	if !errors.Is(err, handlerErr) {
		t.Fatalf("handleDelivery error = %v, want original handler failure", err)
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("handleDelivery error = %v, want response write context", err)
	}
}

func TestClientCancellationWinsAfterBlockedHandlerResumes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := acceptTestConnection(t, w, r)
		defer conn.Close()
		join := readFrame(t, conn)
		writeJoinAccepted(t, conn, join)
		writeTestMessage(t, conn, message{Topic: join.Topic, Event: deliveryEvent, Payload: backendDelivery("att_1", "POST", "")})
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- newClient(testWebSocketURL(server), "test-key", "project:proj_1", nil, testClientOptions()).Listen(ctx, func(Delivery) (Response, error) {
			close(started)
			<-release
			return Response{}, errors.New("closed output")
		})
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		t.Fatalf("Listen returned while synchronous handler was blocked: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Listen error = %v, want cancellation precedence", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Listen did not return after blocked handler resumed")
	}
}
