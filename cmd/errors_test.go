package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"

	"hookspot/internal/api"
	"hookspot/internal/ws"
)

func TestHandleError(t *testing.T) {
	const wantNotFoundHint = "The project may have been deleted or your access removed. " +
		"Select a project with 'hookspot project use', 'hookspot login', --project, or HOOKSPOT_ORGANIZATION_SLUG and HOOKSPOT_PROJECT_SLUG.\n"
	tests := []struct {
		name     string
		err      error
		wantCode int
		want     string
	}{
		{
			name:     "nil",
			wantCode: 0,
		},
		{
			name:     "cancellation is a clean exit",
			err:      fmt.Errorf("listen stopped: %w", context.Canceled),
			wantCode: 0,
		},
		{
			name:     "command guidance",
			err:      loginRequiredError(),
			wantCode: 1,
			want:     "not logged in\n\nRun 'hookspot login' or set HOOKSPOT_CLI_KEY.\n",
		},
		{
			name: "wrapped unauthorized API response",
			err: fmt.Errorf("validate key: %w", &api.Error{
				StatusCode: 401,
				Method:     "GET",
				URL:        "https://example.test/cli/me",
				Message:    "invalid key",
			}),
			wantCode: 1,
			want:     "authentication failed: the Hookspot CLI key was rejected\n\nCheck the key, then run 'hookspot login' again or update HOOKSPOT_CLI_KEY.\n",
		},
		{
			name:     "API redirect hides the response body",
			err:      &api.Error{StatusCode: 302, Method: "GET", URL: "https://example.test/cli/me", Message: "<html>body</html>"},
			wantCode: 1,
			want: "Hookspot API redirect blocked: GET https://example.test/cli/me returned 302 Found\n\n" +
				"Redirects are not followed to protect the Hookspot CLI key. Install the correct Hookspot release.\n",
		},
		{
			name:     "API rate limit",
			err:      &api.Error{StatusCode: 429},
			wantCode: 1,
			want:     "Hookspot API rate limit exceeded\n\nWait briefly and try the command again.\n",
		},
		{
			name:     "API forbidden",
			err:      &api.Error{StatusCode: 403},
			wantCode: 1,
			want:     "authorization failed: the Hookspot CLI key cannot access this resource\n\nCheck that the key belongs to the selected project and has the required access.\n",
		},
		{
			name:     "API unavailable",
			err:      &api.Error{StatusCode: 503},
			wantCode: 1,
			want:     "Hookspot API is unavailable (503 Service Unavailable)\n\nTry again shortly. If the problem continues, check the Hookspot service status.\n",
		},
		{
			name: "ordinary API response preserves command context",
			err: fmt.Errorf("resolve project: %w", &api.Error{
				StatusCode: 400,
				Method:     "GET",
				URL:        "https://example.test/cli/projects/proj_1",
				Message:    "bad request",
			}),
			wantCode: 1,
			want:     "resolve project: GET https://example.test/cli/projects/proj_1: 400 Bad Request: bad request\n",
		},
		{
			name: "API project not found",
			err: fmt.Errorf("resolve project: %w", &api.Error{
				StatusCode: 404,
				Method:     "GET",
				URL:        "https://example.test/cli/projects/proj_1",
				Message:    "not_found",
			}),
			wantCode: 1,
			want:     "resolve project: GET https://example.test/cli/projects/proj_1: 404 Not Found: not_found\n\n" + wantNotFoundHint,
		},
		{
			name: "API transport failure",
			err: fmt.Errorf("list projects: %w", &url.Error{
				Op:  "Get",
				URL: "https://example.test/cli/projects",
				Err: errors.New("connection refused"),
			}),
			wantCode: 1,
			want:     "cannot reach the Hookspot API: connection refused\n\nCheck your network connection and the configured Hookspot server URL.\n",
		},
		{
			name: "WebSocket authentication",
			err: &ws.SessionError{
				Kind: ws.SessionAuthentication,
				Err:  errors.New("unauthorized"),
			},
			wantCode: 1,
			want:     "authentication failed: the WebSocket session was rejected\n\nRun 'hookspot login' again or update HOOKSPOT_CLI_KEY.\n",
		},
		{
			name: "wrapped WebSocket project not found",
			err: fmt.Errorf("listen: %w", &ws.SessionError{
				Kind: ws.SessionNotFound,
				Err:  errors.New("channel join rejected: not_found"),
			}),
			wantCode: 1,
			want:     "project not found: the WebSocket channel join was rejected\n\n" + wantNotFoundHint,
		},
		{
			name:     "generic error",
			err:      errors.New("something failed"),
			wantCode: 1,
			want:     "something failed\n",
		},
		{
			name:     "terminal control characters are escaped",
			err:      errors.New("bad\x1b[31m\rvalue"),
			wantCode: 1,
			want:     "bad\\x1b[31m\\rvalue\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if got := HandleError(&output, test.err); got != test.wantCode {
				t.Fatalf("exit code = %d, want %d", got, test.wantCode)
			}
			if got := output.String(); got != test.want {
				t.Fatalf("output = %q, want %q", got, test.want)
			}
		})
	}
}
