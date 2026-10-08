package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hookspot/internal/api"
	"hookspot/internal/config"
	"hookspot/internal/endpoint"
)

type fakeLoginAPI struct {
	start func(context.Context, string) (*api.LoginAttempt, error)
	poll  func(context.Context, string) (*api.LoginResult, error)
}

func (f *fakeLoginAPI) StartLogin(ctx context.Context, deviceName string) (*api.LoginAttempt, error) {
	if f.start == nil {
		return nil, errors.New("unexpected StartLogin call")
	}
	return f.start(ctx, deviceName)
}

func (f *fakeLoginAPI) PollLogin(ctx context.Context, pollToken string) (*api.LoginResult, error) {
	if f.poll == nil {
		return nil, errors.New("unexpected PollLogin call")
	}
	return f.poll(ctx, pollToken)
}

type fakeLoginStore struct {
	key        string
	projectUID string
	calls      int
}

func (f *fakeLoginStore) SaveLogin(key, projectUID string) error {
	f.calls++
	f.key, f.projectUID = key, projectUID
	return nil
}

func loginTestEndpoint(t *testing.T) endpoint.Base {
	t.Helper()
	base, err := endpoint.Parse("https://hookspot.invalid")
	if err != nil {
		t.Fatal(err)
	}
	return base
}

func instantLoginAttempt() *api.LoginAttempt {
	return &api.LoginAttempt{
		BrowserToken: "browser-token",
		PollToken:    "poll-token",
		Code:         "ABCD-1234",
		ExpiresIn:    600,
	}
}

func approvedLoginResult(key string, project *api.Project) *api.LoginResult {
	return &api.LoginResult{
		Status:  "approved",
		User:    api.User{UID: "usr_1", Email: "dev@example.com", CLIKey: key},
		Project: project,
	}
}

func TestRunBrowserLoginApprovesWithProject(t *testing.T) {
	var deviceName, pollToken, opened string
	loginAPI := &fakeLoginAPI{
		start: func(_ context.Context, name string) (*api.LoginAttempt, error) {
			deviceName = name
			return instantLoginAttempt(), nil
		},
		poll: func(_ context.Context, token string) (*api.LoginResult, error) {
			pollToken = token
			return approvedLoginResult("new-key", &api.Project{
				UID:          "proj_payments",
				Name:         "Payments",
				Organization: api.Organization{Name: "Acme"},
			}), nil
		},
	}
	store := &fakeLoginStore{}
	var out bytes.Buffer

	err := runBrowserLogin(context.Background(), browserLoginDeps{
		api:          loginAPI,
		endpoint:     loginTestEndpoint(t),
		openBrowser:  func(url string) error { opened = url; return nil },
		store:        store,
		pollInterval: time.Millisecond,
		out:          &out,
	})
	if err != nil {
		t.Fatalf("runBrowserLogin: %v", err)
	}

	wantDevice, err := os.Hostname()
	if err != nil {
		wantDevice = ""
	}
	if deviceName != wantDevice {
		t.Fatalf("device name = %q, want %q", deviceName, wantDevice)
	}
	if pollToken != "poll-token" {
		t.Fatalf("poll token = %q, want poll-token", pollToken)
	}
	if want := "https://hookspot.invalid/cli/login/browser-token"; opened != want {
		t.Fatalf("opened URL = %q, want %q", opened, want)
	}
	if store.key != "new-key" || store.projectUID != "proj_payments" {
		t.Fatalf("saved key = %q, project = %q", store.key, store.projectUID)
	}
	for _, want := range []string{
		"│   ABCD-1234 ",
		"╯\nhttps://hookspot.invalid/cli/login/browser-token\n○ Waiting for approval… (Ctrl+C to cancel)\n",
		"✓ Logged in as dev@example.com\n",
		"✓ Active project set to Acme | Payments\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "Couldn't open a browser") || strings.Contains(out.String(), "\x1b") {
		t.Fatalf("unexpected open-failure notice or color:\n%s", out.String())
	}
}

func TestRunBrowserLoginWithoutProjectKeepsSavedProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := writeCommandFixture(path, []byte("schema_version = 1\ncli_key = 'old-key'\nproject = 'kept-project'\n")); err != nil {
		t.Fatal(err)
	}
	store, err := config.New(config.Options{ExplicitPath: path, ExplicitPathSet: true})
	if err != nil {
		t.Fatal(err)
	}
	loginAPI := &fakeLoginAPI{
		start: func(context.Context, string) (*api.LoginAttempt, error) {
			return instantLoginAttempt(), nil
		},
		poll: func(context.Context, string) (*api.LoginResult, error) {
			return approvedLoginResult("new-key", nil), nil
		},
	}
	var out bytes.Buffer

	if err = runBrowserLogin(context.Background(), browserLoginDeps{
		api:          loginAPI,
		endpoint:     loginTestEndpoint(t),
		openBrowser:  func(string) error { return nil },
		store:        store,
		pollInterval: time.Millisecond,
		out:          &out,
	}); err != nil {
		t.Fatalf("runBrowserLogin: %v", err)
	}
	if store.SavedProject() != "kept-project" {
		t.Fatalf("SavedProject() = %q, want kept-project", store.SavedProject())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "new-key") || !strings.Contains(string(contents), "kept-project") {
		t.Fatalf("unexpected config:\n%s", contents)
	}
	if !strings.HasSuffix(out.String(), "✓ Logged in as dev@example.com\n⚠ No active project set; run 'hookspot project use' to select one.\n") {
		t.Fatalf("missing login line and project hint:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Active project set to") {
		t.Fatalf("unexpected project line:\n%s", out.String())
	}
}

func TestRunBrowserLoginOpenFailureStillSucceeds(t *testing.T) {
	loginAPI := &fakeLoginAPI{
		start: func(context.Context, string) (*api.LoginAttempt, error) {
			return instantLoginAttempt(), nil
		},
		poll: func(context.Context, string) (*api.LoginResult, error) {
			return approvedLoginResult("new-key", nil), nil
		},
	}
	store := &fakeLoginStore{}
	var out bytes.Buffer
	opened := false

	err := runBrowserLogin(context.Background(), browserLoginDeps{
		api:          loginAPI,
		endpoint:     loginTestEndpoint(t),
		openBrowser:  func(string) error { opened = true; return errors.New("no browser available") },
		store:        store,
		pollInterval: time.Millisecond,
		out:          &out,
	})
	if err != nil {
		t.Fatalf("runBrowserLogin: %v", err)
	}
	if !opened || store.key != "new-key" {
		t.Fatalf("opened = %v, saved key = %q", opened, store.key)
	}
	if !strings.Contains(out.String(), "browser-token\nCouldn't open a browser; visit the URL above.\n") {
		t.Fatalf("missing manual-open notice:\n%s", out.String())
	}
}

func TestRunBrowserLoginRejectsMalformedBrowserToken(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{name: "empty", token: ""},
		{name: "unsafe segment", token: "../secret"},
		{name: "encoded segment", token: "token%2f"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loginAPI := &fakeLoginAPI{
				start: func(context.Context, string) (*api.LoginAttempt, error) {
					return &api.LoginAttempt{BrowserToken: test.token, PollToken: "poll-token", Code: "CODE", ExpiresIn: 600}, nil
				},
			}
			opened := false

			err := runBrowserLogin(context.Background(), browserLoginDeps{
				api:          loginAPI,
				endpoint:     loginTestEndpoint(t),
				openBrowser:  func(string) error { opened = true; return nil },
				store:        &fakeLoginStore{},
				pollInterval: time.Millisecond,
				out:          io.Discard,
			})
			if err == nil {
				t.Fatalf("runBrowserLogin accepted browser token %q", test.token)
			}
			if opened {
				t.Fatal("opened a browser for an invalid token")
			}
		})
	}
}

func TestRunBrowserLoginRejectsExpiredAttempt(t *testing.T) {
	for _, test := range []struct {
		name      string
		expiresIn int
		poll      func(context.Context, string) (*api.LoginResult, error)
	}{
		{
			name:      "server expired",
			expiresIn: 600,
			poll: func(context.Context, string) (*api.LoginResult, error) {
				return nil, &api.Error{StatusCode: http.StatusNotFound, Method: http.MethodPost, URL: "https://hookspot.invalid/cli/auth/poll"}
			},
		},
		{
			name:      "local deadline",
			expiresIn: 1,
			poll: func(context.Context, string) (*api.LoginResult, error) {
				return &api.LoginResult{Status: "pending"}, nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			loginAPI := &fakeLoginAPI{
				start: func(context.Context, string) (*api.LoginAttempt, error) {
					attempt := instantLoginAttempt()
					attempt.ExpiresIn = test.expiresIn
					return attempt, nil
				},
				poll: test.poll,
			}

			err := runBrowserLogin(context.Background(), browserLoginDeps{
				api:          loginAPI,
				endpoint:     loginTestEndpoint(t),
				openBrowser:  func(string) error { return nil },
				store:        &fakeLoginStore{},
				pollInterval: 10 * time.Millisecond,
				out:          io.Discard,
			})
			if err == nil {
				t.Fatal("runBrowserLogin accepted an expired attempt")
			}
			var stderr bytes.Buffer
			if code := HandleError(&stderr, err); code != 1 {
				t.Fatalf("exit code = %d, want 1", code)
			}
			want := "login attempt expired\n\nRun 'hookspot login' again; this also works if you just created your account.\n"
			if stderr.String() != want {
				t.Fatalf("stderr = %q, want %q", stderr.String(), want)
			}
		})
	}
}

func TestRunBrowserLoginRejectsEmptyCLIKey(t *testing.T) {
	loginAPI := &fakeLoginAPI{
		start: func(context.Context, string) (*api.LoginAttempt, error) {
			return instantLoginAttempt(), nil
		},
		poll: func(context.Context, string) (*api.LoginResult, error) {
			return approvedLoginResult("", nil), nil
		},
	}
	store := &fakeLoginStore{}

	err := runBrowserLogin(context.Background(), browserLoginDeps{
		api:          loginAPI,
		endpoint:     loginTestEndpoint(t),
		openBrowser:  func(string) error { return nil },
		store:        store,
		pollInterval: time.Millisecond,
		out:          io.Discard,
	})
	if err == nil {
		t.Fatal("runBrowserLogin accepted an approved response without a CLI key")
	}
	if store.calls != 0 {
		t.Fatalf("SaveLogin calls = %d, want 0", store.calls)
	}
	if message, _ := fatalErrorMessage(err); !strings.Contains(message, "CLI key") {
		t.Fatalf("message = %q", message)
	}
}

func TestRunBrowserLoginRetriesTransientAPIErrors(t *testing.T) {
	for _, status := range []int{
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var polls atomic.Int32
			loginAPI := &fakeLoginAPI{
				start: func(context.Context, string) (*api.LoginAttempt, error) {
					return instantLoginAttempt(), nil
				},
				poll: func(context.Context, string) (*api.LoginResult, error) {
					if polls.Add(1) == 1 {
						return nil, &api.Error{StatusCode: status, Method: http.MethodPost, URL: "https://hookspot.invalid/cli/auth/poll"}
					}
					return approvedLoginResult("new-key", nil), nil
				},
			}
			store := &fakeLoginStore{}

			err := runBrowserLogin(context.Background(), browserLoginDeps{
				api:          loginAPI,
				endpoint:     loginTestEndpoint(t),
				openBrowser:  func(string) error { return nil },
				store:        store,
				pollInterval: time.Millisecond,
				out:          io.Discard,
			})
			if err != nil {
				t.Fatalf("runBrowserLogin: %v", err)
			}
			if got := polls.Load(); got != 2 {
				t.Fatalf("poll calls = %d, want 2", got)
			}
			if store.key != "new-key" {
				t.Fatalf("saved key = %q, want new-key", store.key)
			}
		})
	}
}

func TestRunBrowserLoginAbortsOnOtherAPIErrors(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			loginAPI := &fakeLoginAPI{
				start: func(context.Context, string) (*api.LoginAttempt, error) {
					return instantLoginAttempt(), nil
				},
				poll: func(context.Context, string) (*api.LoginResult, error) {
					return nil, &api.Error{StatusCode: status, Method: http.MethodPost, URL: "https://hookspot.invalid/cli/auth/poll"}
				},
			}

			err := runBrowserLogin(context.Background(), browserLoginDeps{
				api:          loginAPI,
				endpoint:     loginTestEndpoint(t),
				openBrowser:  func(string) error { return nil },
				store:        &fakeLoginStore{},
				pollInterval: time.Millisecond,
				out:          io.Discard,
			})
			if err == nil || !strings.Contains(err.Error(), "poll browser login") {
				t.Fatalf("error = %v, want poll failure", err)
			}
		})
	}
}

func TestRunBrowserLoginRetriesTransientPollErrors(t *testing.T) {
	var polls atomic.Int32
	loginAPI := &fakeLoginAPI{
		start: func(context.Context, string) (*api.LoginAttempt, error) {
			return instantLoginAttempt(), nil
		},
		poll: func(context.Context, string) (*api.LoginResult, error) {
			if polls.Add(1) == 1 {
				return nil, errors.New("temporary network failure")
			}
			return approvedLoginResult("new-key", nil), nil
		},
	}
	store := &fakeLoginStore{}

	err := runBrowserLogin(context.Background(), browserLoginDeps{
		api:          loginAPI,
		endpoint:     loginTestEndpoint(t),
		openBrowser:  func(string) error { return nil },
		store:        store,
		pollInterval: time.Millisecond,
		out:          io.Discard,
	})
	if err != nil {
		t.Fatalf("runBrowserLogin: %v", err)
	}
	if got := polls.Load(); got != 2 {
		t.Fatalf("poll calls = %d, want 2", got)
	}
	if store.key != "new-key" {
		t.Fatalf("saved key = %q, want new-key", store.key)
	}
}

func TestRunBrowserLoginCancellationReturnsPromptly(t *testing.T) {
	polled := make(chan struct{}, 1)
	loginAPI := &fakeLoginAPI{
		start: func(context.Context, string) (*api.LoginAttempt, error) {
			return instantLoginAttempt(), nil
		},
		poll: func(context.Context, string) (*api.LoginResult, error) {
			select {
			case polled <- struct{}{}:
			default:
			}
			return &api.LoginResult{Status: "pending"}, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	deps := browserLoginDeps{
		api:          loginAPI,
		endpoint:     loginTestEndpoint(t),
		openBrowser:  func(string) error { return nil },
		store:        &fakeLoginStore{},
		pollInterval: time.Millisecond,
		out:          io.Discard,
	}
	done := make(chan error, 1)
	go func() {
		done <- runBrowserLogin(ctx, deps)
	}()

	<-polled
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runBrowserLogin did not return after cancellation")
	}
}

func TestBrowserLoginTTLClampsServerValue(t *testing.T) {
	tests := []struct {
		expiresIn int
		want      time.Duration
	}{
		{expiresIn: -1, want: 10 * time.Minute},
		{expiresIn: 0, want: 10 * time.Minute},
		{expiresIn: 600, want: 10 * time.Minute},
		{expiresIn: 900, want: 15 * time.Minute},
		{expiresIn: 86400, want: 15 * time.Minute},
	}

	for _, test := range tests {
		if got := browserLoginTTL(test.expiresIn); got != test.want {
			t.Fatalf("browserLoginTTL(%d) = %s, want %s", test.expiresIn, got, test.want)
		}
	}
}

func TestLoginWithExplicitKeySkipsBrowserFlow(t *testing.T) {
	for _, test := range []struct {
		name        string
		environment map[string]string
		args        []string
		key         string
	}{
		{name: "flag", args: []string{"--cli-key", "flag-key"}, key: "flag-key"},
		{name: "environment", environment: map[string]string{"HOOKSPOT_CLI_KEY": "environment-key"}, key: "environment-key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/cli/me" {
					t.Errorf("path = %q, want /cli/me", r.URL.Path)
				}
				if got := r.Header.Get("X-CLI-KEY"); got != test.key {
					t.Errorf("X-CLI-KEY = %q, want %q", got, test.key)
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"uid": "usr_1", "email": "dev@example.com"})
			}))
			defer server.Close()

			path := filepath.Join(t.TempDir(), "config.toml")
			args := append([]string{"--config", path}, test.args...)
			args = append(args, "login")
			result := runCommandProcessEnvironment(t, "", developmentMetadata(server.URL), test.environment, args...)
			if result.err != nil {
				t.Fatalf("login failed: %v\n%s", result.err, result.stderr)
			}
			if result.stdout != "✓ Logged in as dev@example.com\n" {
				t.Fatalf("unexpected output:\n%s", result.stdout)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(contents), test.key) {
				t.Fatalf("config missing key:\n%s", contents)
			}
		})
	}
}

func TestLoginInteractivePromptsForKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-CLI-KEY"); got != "typed-key" {
			t.Errorf("X-CLI-KEY = %q, want typed-key", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"uid": "usr_1", "email": "dev@example.com"})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "config.toml")
	result := runCommandProcess(t, "typed-key\n", developmentMetadata(server.URL), "--config", path, "login", "-i")
	if result.err != nil {
		t.Fatalf("login failed: %v\n%s", result.err, result.stderr)
	}
	if result.stdout != "Enter your hookspot CLI key (Account settings > CLI key): \n✓ Logged in as dev@example.com\n" {
		t.Fatalf("unexpected output:\n%s", result.stdout)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "typed-key") {
		t.Fatalf("config missing key:\n%s", contents)
	}
}

func TestLoginInteractivePromptsDespiteSavedKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-CLI-KEY"); got != "typed-key" {
			t.Errorf("X-CLI-KEY = %q, want typed-key", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"uid": "usr_1", "email": "dev@example.com"})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := writeCommandFixture(path, []byte("schema_version = 1\ncli_key = 'saved-key'\nproject = 'saved-project'\n")); err != nil {
		t.Fatal(err)
	}

	result := runCommandProcess(t, "typed-key\n", developmentMetadata(server.URL), "--config", path, "login", "-i")
	if result.err != nil {
		t.Fatalf("login failed: %v\n%s", result.err, result.stderr)
	}
	if !strings.Contains(result.stdout, "Enter your hookspot CLI key") {
		t.Fatalf("missing prompt despite saved key:\n%s", result.stdout)
	}
	if !strings.Contains(result.stdout, "Logged in as dev@example.com") {
		t.Fatalf("unexpected output:\n%s", result.stdout)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "typed-key") || strings.Contains(string(contents), "saved-key") {
		t.Fatalf("config did not switch to the new key:\n%s", contents)
	}
}

func TestLoginWithSavedKeyStartsBrowserFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cli/auth" {
			t.Errorf("path = %q, want /cli/auth", r.URL.Path)
		}
		if got := r.Header.Get("X-CLI-KEY"); got != "" {
			t.Errorf("X-CLI-KEY = %q, want empty", got)
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"status":"error"}`)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := writeCommandFixture(path, []byte("schema_version = 1\ncli_key = 'saved-key'\nproject = 'saved-project'\n")); err != nil {
		t.Fatal(err)
	}

	result := runCommandProcess(t, "", developmentMetadata(server.URL), "--config", path, "login")
	if result.err == nil {
		t.Fatal("login unexpectedly succeeded")
	}
	if !strings.Contains(result.stderr, "start browser login") {
		t.Fatalf("unexpected output:\n%s", result.stderr)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "saved-key") || strings.Contains(string(contents), "Confirmation") {
		t.Fatalf("config changed:\n%s", contents)
	}
}
