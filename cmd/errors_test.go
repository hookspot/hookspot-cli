package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"hookspot/internal/api"
	"hookspot/internal/endpoint"
	"hookspot/internal/ws"
)

func TestRootCommandSilencesCobraErrorOutput(t *testing.T) {
	if !rootCmd.SilenceErrors {
		t.Fatal("root command allows Cobra to print errors")
	}
	if !rootCmd.SilenceUsage {
		t.Fatal("root command allows Cobra to print usage for runtime errors")
	}
}

func TestHandleError(t *testing.T) {
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
				StatusCode: 404,
				Method:     "GET",
				URL:        "https://example.test/cli/projects/missing",
				Message:    "project not found",
			}),
			wantCode: 1,
			want:     "resolve project: GET https://example.test/cli/projects/missing: 404 Not Found: project not found\n",
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

func TestSafeDisplayTextEscapesLayoutAndTerminalControls(t *testing.T) {
	got := safeDisplayText("line\ncolumn\t\x1b")
	if got != `line\ncolumn\t\x1b` {
		t.Fatalf("safe display = %q", got)
	}
}

func TestHandleErrorExplainsBlockedAPIClientRedirect(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var redirectedRequests atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirectedRequests.Add(1)
				fmt.Fprint(w, "location-sentinel")
			}))
			defer destination.Close()

			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", destination.URL+"/location-sentinel")
				w.WriteHeader(status)
				fmt.Fprint(w, "<html>html-body-sentinel</html>")
			}))
			defer origin.Close()

			base, err := endpoint.Parse(origin.URL, "dev")
			if err != nil {
				t.Fatal(err)
			}
			_, err = api.New(base, "key-sentinel").Me(context.Background())
			if err == nil {
				t.Fatal("API client accepted redirect")
			}

			var output bytes.Buffer
			if code := HandleError(&output, err); code != 1 {
				t.Fatalf("exit code = %d, want 1", code)
			}
			want := fmt.Sprintf(
				"Hookspot API redirect blocked: GET %s/cli/me returned %d %s\n\n"+
					"Redirects are not followed to protect the Hookspot CLI key. Install the correct Hookspot release for this environment.\n",
				origin.URL,
				status,
				http.StatusText(status),
			)
			if output.String() != want {
				t.Fatalf("output = %q, want %q", output.String(), want)
			}
			for _, sentinel := range []string{"html-body-sentinel", "location-sentinel", "key-sentinel", destination.URL} {
				if strings.Contains(output.String(), sentinel) {
					t.Fatalf("output leaked %q: %s", sentinel, output.String())
				}
			}
			if got := redirectedRequests.Load(); got != 0 {
				t.Fatalf("redirect destination requests = %d, want 0", got)
			}
		})
	}
}
