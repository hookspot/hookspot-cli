package ws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	heartbeatIntervalDefault = 30 * time.Second
	joinTimeoutDefault       = 10 * time.Second
	writeTimeoutDefault      = 10 * time.Second
	receiveIdleDefault       = 90 * time.Second
	maxFrameBytesDefault     = 32 * 1024 * 1024
	deliveryEvent            = "delivery"
	responseEvent            = "delivery_response"
	maxPendingHeartbeats     = 8
)

// SessionErrorKind identifies which WebSocket failures can be retried by the
// listen supervisor and which require the command to stop.
type SessionErrorKind uint8

const (
	SessionConnect SessionErrorKind = iota
	SessionDisconnected
	SessionAuthentication
	SessionProtocol
	SessionHandler
)

// SessionError retains the original failure and whether the session completed
// its channel join before failing.
type SessionError struct {
	Kind      SessionErrorKind
	Connected bool
	Err       error
}

func (e *SessionError) Error() string {
	if e == nil || e.Err == nil {
		return "WebSocket session error"
	}
	return e.Err.Error()
}

func (e *SessionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Retryable reports whether reconnecting can reasonably recover this failure.
func (e *SessionError) Retryable() bool {
	return e != nil && (e.Kind == SessionConnect || e.Kind == SessionDisconnected)
}

func sessionError(kind SessionErrorKind, connected bool, err error) error {
	if err == nil {
		return nil
	}
	var existing *SessionError
	if errors.As(err, &existing) {
		if connected {
			existing.Connected = true
		}
		return existing
	}
	return &SessionError{Kind: kind, Connected: connected, Err: err}
}

// Delivery is the payload of a delivery event: a captured webhook request to
// replay against the local target. AttemptUID is internal protocol correlation;
// RequestUID identifies the captured request and SourceUID selects its label.
//
// Body is padded or unpadded standard Base64 on the wire. Delivery's JSON
// boundary decodes either form, so Body holds the raw request bytes.
type Delivery struct {
	AttemptUID string      `json:"attempt_uid"`
	RequestUID string      `json:"request_uid"`
	SourceUID  string      `json:"source_uid"`
	Method     string      `json:"method"`
	Path       string      `json:"path"`
	Headers    http.Header `json:"headers"`
	Query      string      `json:"query"`
	Body       []byte      `json:"body"`
}

// UnmarshalJSON accepts both ordinary padded Base64 and the raw, unpadded
// encoding used by the backend. Decode failures deliberately omit the wire
// value because it contains the captured webhook body.
func (d *Delivery) UnmarshalJSON(data []byte) error {
	type deliveryAlias Delivery
	var decoded deliveryAlias
	wire := struct {
		*deliveryAlias
		Body *string `json:"body"`
	}{deliveryAlias: &decoded}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	if wire.Body != nil {
		encoding := base64.RawStdEncoding
		if strings.ContainsRune(*wire.Body, '=') {
			encoding = base64.StdEncoding
		}
		body, err := encoding.DecodeString(*wire.Body)
		if err != nil {
			return errors.New("delivery body has invalid Base64 encoding")
		}
		decoded.Body = body
	}

	*d = Delivery(decoded)
	return nil
}

// Response is the local target's reply to a forwarded delivery. Body is
// base64-encoded on the wire by encoding/json, and LatencyMS is the complete
// local HTTP operation duration sent in the delivery_response event.
type Response struct {
	Status    int         `json:"status"`
	Headers   http.Header `json:"headers"`
	Body      []byte      `json:"body"`
	LatencyMS int64       `json:"latency_ms"`
}

// Handler responds to a webhook delivery.
type Handler func(delivery Delivery) (Response, error)

// deliveryResponse is the delivery_response event payload sent back to the
// server, correlated to the delivery by AttemptUID.
type deliveryResponse struct {
	AttemptUID string      `json:"attempt_uid"`
	Status     int         `json:"status"`
	Headers    http.Header `json:"headers"`
	Body       []byte      `json:"body"`
	LatencyMS  int64       `json:"latency_ms"`
}

// Client connects to a hookspot Phoenix Channel and streams events.
type Client struct {
	url     string
	cliKey  string
	topic   string
	sources []string
	options clientOptions
}

type clientOptions struct {
	dialer            *websocket.Dialer
	joinTimeout       time.Duration
	writeTimeout      time.Duration
	heartbeatInterval time.Duration
	receiveIdle       time.Duration
	maxFrameBytes     int64
}

// New returns a Client that connects to url, authenticates with cliKey, and
// joins topic, requesting the given sources.
func New(url, cliKey, topic string, sources []string) *Client {
	return newClient(url, cliKey, topic, sources, clientOptions{})
}

func newClient(url, cliKey, topic string, sources []string, options clientOptions) *Client {
	if options.dialer == nil {
		options.dialer = websocket.DefaultDialer
	}
	if options.joinTimeout <= 0 {
		options.joinTimeout = joinTimeoutDefault
	}
	if options.writeTimeout <= 0 {
		options.writeTimeout = writeTimeoutDefault
	}
	if options.heartbeatInterval <= 0 {
		options.heartbeatInterval = heartbeatIntervalDefault
	}
	if options.receiveIdle <= 0 {
		options.receiveIdle = receiveIdleDefault
	}
	if options.maxFrameBytes <= 0 {
		options.maxFrameBytes = maxFrameBytesDefault
	}
	return &Client{url: url, cliKey: cliKey, topic: topic, sources: sources, options: options}
}

// connWriter serializes writes to a websocket connection and assigns a unique,
// incrementing ref to each outgoing message. gorilla permits only one
// concurrent writer, and both the heartbeat goroutine and the read loop send
// frames, so all writes go through here.
type connWriter struct {
	conn    frameWriter
	timeout time.Duration
	mu      sync.Mutex
	ref     int
}

type frameWriter interface {
	SetWriteDeadline(time.Time) error
	WriteMessage(int, []byte) error
}

// watchSocket starts when the TCP socket is acquired, before Gorilla performs
// proxy negotiation or the HTTP upgrade. The caller closes done and waits for
// workers on every return path.
func watchSocket(ctx context.Context, done <-chan struct{}, workers *sync.WaitGroup, conn net.Conn) {
	workers.Add(1)
	go func() {
		defer workers.Done()
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
}

func (w *connWriter) send(m message) (string, error) {
	return w.sendMessage(m, false, nil)
}

func (w *connWriter) sendJoin(m message) (string, error) {
	return w.sendMessage(m, true, nil)
}

func (w *connWriter) sendHeartbeat(pending *heartbeatTracker) (string, error) {
	return w.sendMessage(message{Topic: "phoenix", Event: "heartbeat"}, false, pending)
}

func (w *connWriter) sendMessage(m message, join bool, pending *heartbeatTracker) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.ref++
	r := strconv.Itoa(w.ref)
	m.Ref = &r
	if join {
		m.JoinRef = &r
	}

	frame, err := encode(m)
	if err != nil {
		return "", err
	}
	if err := w.conn.SetWriteDeadline(time.Now().Add(w.timeout)); err != nil {
		return "", err
	}
	if pending != nil {
		pending.add(r)
	}
	if err := w.conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		if pending != nil {
			pending.take(r)
		}
		return "", err
	}
	return r, nil
}

// Listen connects, joins the channel, and for each delivery event invokes
// handler and pushes the returned Response back as a delivery_response. It
// blocks until handler returns an error, the channel errors/closes, or ctx is
// cancelled.
func (c *Client) Listen(ctx context.Context, handler Handler) error {
	header := http.Header{}
	if c.cliKey != "" {
		header.Set("X-CLI-KEY", c.cliKey)
	}

	done := make(chan struct{})
	var workers sync.WaitGroup
	var socket net.Conn
	var conn *websocket.Conn
	defer func() {
		close(done)
		if conn != nil {
			_ = conn.Close()
		}
		if socket != nil {
			_ = socket.Close()
		}
		workers.Wait()
	}()

	dialer := *c.options.dialer
	dial := dialer.NetDialContext
	if dial == nil {
		dial = new(net.Dialer).DialContext
	}
	dialer.NetDialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
		raw, err := dial(dialCtx, network, address)
		if err != nil {
			return nil, err
		}
		socket = raw
		watchSocket(ctx, done, &workers, raw)
		return raw, nil
	}

	var response *http.Response
	var err error
	conn, response, err = dialer.DialContext(ctx, c.url, header)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if response != nil && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
			return sessionError(SessionAuthentication, false, fmt.Errorf("connect to %s: server returned %s", c.url, response.Status))
		}
		return sessionError(SessionConnect, false, fmt.Errorf("connect to %s: %w", c.url, err))
	}
	conn.SetReadLimit(c.options.maxFrameBytes)
	writer := &connWriter{conn: conn, timeout: c.options.writeTimeout}

	activeJoinRef, err := c.join(ctx, conn, writer)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var typed *SessionError
		if errors.As(err, &typed) {
			return err
		}
		return sessionError(SessionConnect, false, err)
	}
	if err := c.rearmReceiveIdle(conn); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return sessionError(SessionDisconnected, true, fmt.Errorf("set receive-idle deadline: %w", err))
	}

	heartbeats := newHeartbeatTracker(maxPendingHeartbeats)
	heartbeatErr := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		c.heartbeat(writer, heartbeats, done, func(err error) {
			select {
			case heartbeatErr <- err:
			default:
			}
			_ = conn.Close()
		})
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			select {
			case heartbeatFailure := <-heartbeatErr:
				return sessionError(SessionDisconnected, true, fmt.Errorf("send heartbeat: %w", heartbeatFailure))
			default:
			}
			return sessionError(SessionDisconnected, true, fmt.Errorf("read message: %w", err))
		}

		msg, err := decode(data)
		if err != nil {
			// A malformed frame is isolated to that server message. Dropping it is
			// safer than disrupting every in-flight delivery by reconnecting.
			continue
		}

		switch msg.Event {
		case deliveryEvent:
			if msg.Topic != c.topic {
				continue
			}
			delivery, err := decodeDelivery(msg.Payload)
			if err != nil {
				return sessionError(SessionProtocol, true, err)
			}
			if err := c.handleDelivery(ctx, writer, activeJoinRef, delivery, handler); err != nil {
				return err
			}
			if err := c.rearmReceiveIdle(conn); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return sessionError(SessionDisconnected, true, fmt.Errorf("renew receive-idle deadline: %w", err))
			}
		case "phx_reply":
			if matchingHeartbeatReply(msg, heartbeats) {
				if err := c.rearmReceiveIdle(conn); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					return sessionError(SessionDisconnected, true, fmt.Errorf("renew receive-idle deadline: %w", err))
				}
			}
		case "phx_error", "phx_close":
			if msg.Topic == c.topic {
				return sessionError(SessionDisconnected, true, fmt.Errorf("joined channel received %s", msg.Event))
			}
		}
	}
}

func decodeDelivery(payload []byte) (Delivery, error) {
	var delivery Delivery
	if err := json.Unmarshal(payload, &delivery); err != nil {
		return Delivery{}, errors.New("invalid delivery payload")
	}
	if delivery.AttemptUID == "" || delivery.Method == "" {
		return Delivery{}, errors.New("delivery is missing correlation fields")
	}
	return delivery, nil
}

// handleDelivery invokes the handler and sends any usable response before
// returning a handler error. This lets completed forwards be acknowledged with
// their real response even when rendering that response fails. A zero response
// paired with an error is not acknowledged.
func (c *Client) handleDelivery(ctx context.Context, writer *connWriter, activeJoinRef string, delivery Delivery, handler Handler) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	resp, err := handler(delivery)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil && resp.Status == 0 {
		return sessionError(SessionHandler, true, err)
	}

	responsePayload, encodeErr := json.Marshal(deliveryResponse{
		AttemptUID: delivery.AttemptUID,
		Status:     resp.Status,
		Headers:    resp.Headers,
		Body:       resp.Body,
		LatencyMS:  resp.LatencyMS,
	})
	if encodeErr != nil {
		return sessionError(SessionHandler, true, fmt.Errorf("encode delivery response: %w", encodeErr))
	}

	if _, sendErr := writer.send(message{
		JoinRef: &activeJoinRef,
		Topic:   c.topic,
		Event:   responseEvent,
		Payload: responsePayload,
	}); sendErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return sessionError(SessionHandler, true, errors.Join(err, fmt.Errorf("send delivery response: %w", sendErr)))
		}
		return sessionError(SessionDisconnected, true, fmt.Errorf("send delivery response: %w", sendErr))
	}
	if err != nil {
		return sessionError(SessionHandler, true, err)
	}
	return nil
}

func (c *Client) rearmReceiveIdle(conn *websocket.Conn) error {
	return conn.SetReadDeadline(time.Now().Add(c.options.receiveIdle))
}

func successfulReply(payload []byte) bool {
	var reply struct {
		Status string `json:"status"`
	}
	return json.Unmarshal(payload, &reply) == nil && reply.Status == "ok"
}

func matchingHeartbeatReply(msg message, pending *heartbeatTracker) bool {
	return msg.Topic == "phoenix" && msg.Event == "phx_reply" && msg.Ref != nil &&
		successfulReply(msg.Payload) && pending.take(*msg.Ref)
}

// join sends phx_join and waits for the reply matching both its topic and ref.
func (c *Client) join(ctx context.Context, conn *websocket.Conn, writer *connWriter) (string, error) {
	sources := c.sources
	if sources == nil {
		sources = []string{}
	}
	payload, err := json.Marshal(struct {
		Sources []string `json:"sources"`
	}{Sources: sources})
	if err != nil {
		return "", fmt.Errorf("encode join payload: %w", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(c.options.joinTimeout)); err != nil {
		return "", fmt.Errorf("set join deadline: %w", err)
	}
	ref, err := writer.sendJoin(message{
		Topic:   c.topic,
		Event:   "phx_join",
		Payload: payload,
	})
	if err != nil {
		return "", fmt.Errorf("send join: %w", err)
	}

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("read join reply: %w", err)
		}
		msg, err := decode(data)
		if err != nil {
			return "", sessionError(SessionProtocol, false, errors.New("decode join reply: invalid frame"))
		}
		if msg.Event != "phx_reply" || msg.Topic != c.topic || msg.Ref == nil || *msg.Ref != ref {
			continue
		}

		var reply struct {
			Status   string `json:"status"`
			Response struct {
				Reason string `json:"reason"`
			} `json:"response"`
		}
		if err := json.Unmarshal(msg.Payload, &reply); err != nil {
			return "", sessionError(SessionProtocol, false, errors.New("decode join reply: invalid payload"))
		}
		if reply.Status != "ok" {
			detail := shortReplyDetail(reply.Status, reply.Response.Reason)
			err := fmt.Errorf("channel join rejected%s", detail)
			if strings.EqualFold(reply.Response.Reason, "unauthorized") || strings.EqualFold(reply.Response.Reason, "forbidden") {
				return "", sessionError(SessionAuthentication, false, err)
			}
			return "", sessionError(SessionProtocol, false, err)
		}
		return ref, nil
	}
}

func shortReplyDetail(status, reason string) string {
	status = shortProtocolText(status)
	reason = shortProtocolText(reason)
	if status == "" && reason == "" {
		return ""
	}
	if reason == "" {
		return fmt.Sprintf(" (status %q)", status)
	}
	if status == "" {
		return fmt.Sprintf(" (reason %q)", reason)
	}
	return fmt.Sprintf(" (status %q, reason %q)", status, reason)
}

func shortProtocolText(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > 80 {
		value = string(runes[:80]) + "…"
	}
	return value
}

// heartbeat has no reader or deadline ownership. A write failure closes the
// connection through fail so the single read loop wakes and reports it.
func (c *Client) heartbeat(writer *connWriter, pending *heartbeatTracker, done <-chan struct{}, fail func(error)) {
	ticker := time.NewTicker(c.options.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			_, err := writer.sendHeartbeat(pending)
			if err != nil {
				fail(err)
				return
			}
		}
	}
}

type heartbeatTracker struct {
	mu    sync.Mutex
	max   int
	refs  map[string]struct{}
	order []string
}

func newHeartbeatTracker(max int) *heartbeatTracker {
	return &heartbeatTracker{max: max, refs: make(map[string]struct{}, max)}
}

func (p *heartbeatTracker) add(ref string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.order) == p.max {
		delete(p.refs, p.order[0])
		p.order = p.order[1:]
	}
	p.refs[ref] = struct{}{}
	p.order = append(p.order, ref)
}

func (p *heartbeatTracker) take(ref string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.refs[ref]; !ok {
		return false
	}
	delete(p.refs, ref)
	for index, candidate := range p.order {
		if candidate == ref {
			p.order = append(p.order[:index], p.order[index+1:]...)
			break
		}
	}
	return true
}
