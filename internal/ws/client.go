package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	joinRef           = "1"
	heartbeatInterval = 30 * time.Second
	deliveryEvent     = "delivery"
	responseEvent     = "delivery_response"
)

// Delivery is the payload of a delivery event: a captured webhook request to
// replay against the local target. AttemptUID correlates the delivery with the
// delivery_response sent back after forwarding.
//
// Body is base64-encoded on the wire; encoding/json base64-decodes it
// automatically when unmarshaling into the []byte field, so delivery.Body
// holds the raw request body.
type Delivery struct {
	AttemptUID string      `json:"attempt_uid"`
	Method     string      `json:"method"`
	Path       string      `json:"path"`
	Headers    http.Header `json:"headers"`
	Query      string      `json:"query"`
	Body       []byte      `json:"body"`
}

// Response is the local target's reply to a forwarded delivery. Body is
// base64-encoded on the wire by encoding/json.
type Response struct {
	Status  int         `json:"status"`
	Headers http.Header `json:"headers"`
	Body    []byte      `json:"body"`
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
}

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

// connWriter serializes writes to a websocket connection and assigns a unique,
// incrementing ref to each outgoing message. gorilla permits only one
// concurrent writer, and both the heartbeat goroutine and the read loop send
// frames, so all writes go through here.
type connWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
	ref  int
}

func (w *connWriter) send(m message) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.ref++
	r := strconv.Itoa(w.ref)
	m.Ref = &r

	frame, err := encode(m)
	if err != nil {
		return err
	}
	return w.conn.WriteMessage(websocket.TextMessage, frame)
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

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.url, header)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", c.url, err)
	}
	defer conn.Close()

	if err := c.join(conn); err != nil {
		return err
	}

	writer := &connWriter{conn: conn}

	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	go c.heartbeat(writer, done)

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
			if err := c.handleDelivery(writer, msg.Payload, handler); err != nil {
				return err
			}
		case "phx_error", "phx_close":
			if msg.Topic == c.topic {
				return fmt.Errorf("channel %s: received %s", c.topic, msg.Event)
			}
		}
	}
}

// handleDelivery decodes a delivery, invokes handler, and pushes the resulting
// delivery_response back to the server, correlated by the attempt uid.
func (c *Client) handleDelivery(writer *connWriter, payload []byte, handler func(Delivery) (Response, error)) error {
	var delivery Delivery
	if err := json.Unmarshal(payload, &delivery); err != nil {
		return fmt.Errorf("decode delivery: %w", err)
	}

	resp, err := handler(delivery)
	if err != nil {
		return err
	}

	responsePayload, err := json.Marshal(deliveryResponse{
		AttemptUID: delivery.AttemptUID,
		Status:     resp.Status,
		Headers:    resp.Headers,
		Body:       resp.Body,
	})
	if err != nil {
		return fmt.Errorf("encode delivery response: %w", err)
	}

	jr := joinRef
	if err := writer.send(message{
		JoinRef: &jr,
		Topic:   c.topic,
		Event:   responseEvent,
		Payload: responsePayload,
	}); err != nil {
		return fmt.Errorf("send delivery response: %w", err)
	}
	return nil
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
func (c *Client) heartbeat(writer *connWriter, done <-chan struct{}) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if err := writer.send(message{Topic: "phoenix", Event: "heartbeat"}); err != nil {
				return
			}
		}
	}
}
