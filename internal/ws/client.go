package ws

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
)

// Client connects to a hookspot websocket event stream.
type Client struct {
	url   string
	token string
}

// New returns a Client that will connect to url, authenticating with token.
func New(url, token string) *Client {
	return &Client{url: url, token: token}
}

// Listen connects to the websocket and invokes handler for each received
// message. It blocks until handler returns an error, the connection is
// closed, or ctx is cancelled.
func (c *Client) Listen(ctx context.Context, handler func(message []byte) error) error {
	header := http.Header{}
	if c.token != "" {
		header.Set("Authorization", "Bearer "+c.token)
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.url, header)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", c.url, err)
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read message: %w", err)
		}

		if err := handler(message); err != nil {
			return err
		}
	}
}
