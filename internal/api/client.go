package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"hookspot/internal/endpoint"
)

const (
	maxErrorBodyBytes   = 64 * 1024
	maxSuccessBodyBytes = 1 * 1024 * 1024
)

// Error is a non-success response from the Hookspot API. It keeps the status
// code available for global presentation without requiring string matching.
type Error struct {
	StatusCode int
	Method     string
	URL        string
	Message    string
}

func (e *Error) Error() string {
	status := e.Status()
	if e.Message != "" {
		return fmt.Sprintf("%s %s: %s: %s", e.Method, e.URL, status, e.Message)
	}
	return fmt.Sprintf("%s %s: %s", e.Method, e.URL, status)
}

// Status returns the numeric and semantic HTTP status when available.
func (e *Error) Status() string {
	text := http.StatusText(e.StatusCode)
	if text == "" {
		return fmt.Sprintf("HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("%d %s", e.StatusCode, text)
}

// Client is a small REST client for the hookspot API.
type Client struct {
	base   endpoint.Base
	cliKey string
	http   *http.Client
}

// New returns a Client configured for base, authenticating with cliKey.
func New(base endpoint.Base, cliKey string) *Client {
	return &Client{
		base:   base,
		cliKey: cliKey,
		http: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
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
	if err := c.get(ctx, "cli/me", &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// Organization represents the organization a project belongs to.
type Organization struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Source represents a webhook source belonging to a project.
type Source struct {
	UID         string       `json:"uid"`
	Name        string       `json:"name"`
	URL         string       `json:"url"`
	Active      bool         `json:"active"`
	Connections []Connection `json:"connections"`
}

// Connection represents a source connection and its forwarding destination.
type Connection struct {
	UID         string      `json:"uid"`
	Name        *string     `json:"name"`
	Active      bool        `json:"active"`
	Destination Destination `json:"destination"`
	DisplayName string      `json:"display_name"`
}

// Destination represents the target path configured for a connection.
type Destination struct {
	UID    string `json:"uid"`
	Path   string `json:"path"`
	Active bool   `json:"active"`
}

// Project represents a hookspot project accessible to the current user.
type Project struct {
	UID          string       `json:"uid"`
	Name         string       `json:"name"`
	Slug         string       `json:"slug"`
	Organization Organization `json:"organization"`
	Sources      []Source     `json:"sources"`
}

// ListProjects returns the projects accessible to the client's CLI key.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var projects []Project
	if err := c.get(ctx, "cli/projects", &projects); err != nil {
		return nil, err
	}
	return projects, nil
}

// GetProject returns a single project by uid, including its organization.
func (c *Client) GetProject(ctx context.Context, uid string) (*Project, error) {
	uid, err := endpoint.Segment(uid)
	if err != nil {
		return nil, fmt.Errorf("invalid project UID: %w", err)
	}
	var project Project
	if err := c.get(ctx, "cli/projects/"+uid, &project); err != nil {
		return nil, err
	}
	return &project, nil
}

// ListProjectSources returns the sources and connections belonging to a project.
func (c *Client) ListProjectSources(ctx context.Context, projectUID string) ([]Source, error) {
	projectUID, err := endpoint.Segment(projectUID)
	if err != nil {
		return nil, fmt.Errorf("invalid project UID: %w", err)
	}
	var sources []Source
	if err := c.get(ctx, "cli/projects/"+projectUID+"/sources", &sources); err != nil {
		return nil, err
	}
	return sources, nil
}

// GetProjectBySlugs returns the project matching organizationSlug and
// projectSlug from the projects accessible to the client's CLI key.
func (c *Client) GetProjectBySlugs(ctx context.Context, organizationSlug, projectSlug string) (*Project, error) {
	projects, err := c.ListProjects(ctx)
	if err != nil {
		return nil, err
	}

	for _, project := range projects {
		if project.Organization.Slug == organizationSlug && project.Slug == projectSlug {
			return &project, nil
		}
	}

	return nil, fmt.Errorf("project %q not found", organizationSlug+"/"+projectSlug)
}

func (c *Client) get(ctx context.Context, path string, out interface{}) error {
	u := c.base.API(path)
	if u == nil {
		return fmt.Errorf("hookspot API endpoint is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
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
		return decodeErrorResponse(req, resp)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSuccessBodyBytes+1))
	if err != nil {
		return fmt.Errorf("read Hookspot API response: %w", err)
	}
	if len(body) > maxSuccessBodyBytes {
		return errors.New("hookspot API response exceeds 1 MiB limit")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode Hookspot API response: %w", err)
	}
	return nil
}

func decodeErrorResponse(req *http.Request, resp *http.Response) error {
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if readErr != nil {
		return fmt.Errorf("read Hookspot API error response: %w", readErr)
	}

	return &Error{
		StatusCode: resp.StatusCode,
		Method:     req.Method,
		URL:        req.URL.String(),
		Message:    apiErrorMessage(body),
	}
}

func apiErrorMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}

	var payload struct {
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
		Reason  string          `json:"reason"`
	}
	if json.Unmarshal(body, &payload) == nil {
		if payload.Message != "" {
			return payload.Message
		}
		if payload.Reason != "" {
			return payload.Reason
		}
		if len(payload.Error) > 0 {
			var message string
			if json.Unmarshal(payload.Error, &message) == nil && message != "" {
				return message
			}
			var nested struct {
				Message string `json:"message"`
				Reason  string `json:"reason"`
			}
			if json.Unmarshal(payload.Error, &nested) == nil {
				if nested.Message != "" {
					return nested.Message
				}
				if nested.Reason != "" {
					return nested.Reason
				}
			}
		}
	}

	return trimmed
}
