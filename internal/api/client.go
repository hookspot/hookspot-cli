package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Client is a small REST client for the hookspot API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New returns a Client configured for baseURL, authenticating with token.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    http.DefaultClient,
	}
}

// User represents the authenticated hookspot user.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// Me returns the user associated with the client's token.
func (c *Client) Me(ctx context.Context) (*User, error) {
	var user User
	if err := c.get(ctx, "/api/me", &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (c *Client) get(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: unexpected status %d", req.Method, req.URL, resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
