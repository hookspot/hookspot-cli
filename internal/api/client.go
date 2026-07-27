package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client is a small REST client for the hookspot API.
type Client struct {
	baseURL string
	cliKey  string
	http    *http.Client
}

// New returns a Client configured for baseURL, authenticating with cliKey.
func New(baseURL, cliKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		cliKey:  cliKey,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// User represents the authenticated hookspot user.
type User struct {
	UID   string `json:"uid"`
	Email string `json:"email"`
}

// Me returns the user associated with the client's CLI key.
func (c *Client) Me(ctx context.Context) (*User, error) {
	var user User
	if err := c.get(ctx, "/cli/me", &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// Organization represents the organization a project belongs to.
type Organization struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
}

// Project represents a hookspot project accessible to the current user.
type Project struct {
	UID          string       `json:"uid"`
	Name         string       `json:"name"`
	Organization Organization `json:"organization"`
}

// ListProjects returns the projects accessible to the client's CLI key.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var projects []Project
	if err := c.get(ctx, "/cli/projects", &projects); err != nil {
		return nil, err
	}
	return projects, nil
}

// GetProject returns a single project by uid, including its organization.
func (c *Client) GetProject(ctx context.Context, uid string) (*Project, error) {
	var project Project
	if err := c.get(ctx, "/cli/projects/"+uid, &project); err != nil {
		return nil, err
	}
	return &project, nil
}

func (c *Client) get(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.cliKey != "" {
		req.Header.Set("X-CLI-KEY", c.cliKey)
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
