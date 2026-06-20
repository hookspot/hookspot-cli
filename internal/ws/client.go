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
	deliveryEvent     = "delivery"
)

// Delivery is the payload of a delivery event: a captured webhook request to
// replay against the local target.
//
// Body is base64-encoded on the wire; encoding/json base64-decodes it
// automatically when unmarshaling into the []byte field, so delivery.Body
// holds the raw request body.
type Delivery struct {
	Method  string      `json:"method"`
	Headers http.Header `json:"headers"`
	Query   string      `json:"query"`
	Body    []byte      `json:"body"`
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

// Listen connects, joins the channel, and invokes handler with the decoded
// Delivery of each delivery event. It blocks until handler returns an error,
// the channel errors/closes, or ctx is cancelled.
func (c *Client) Listen(ctx context.Context, handler func(delivery Delivery) error) error {
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
			var delivery Delivery
			if err := json.Unmarshal(msg.Payload, &delivery); err != nil {
				return fmt.Errorf("decode delivery: %w", err)
			}
			if err := handler(delivery); err != nil {
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
