package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/golden"
	"github.com/gorilla/websocket"

	"hookspot/internal/api"
	"hookspot/internal/cards"
	"hookspot/internal/endpoint"
	"hookspot/internal/proxy"
	"hookspot/internal/session"
	"hookspot/internal/tui"
	"hookspot/internal/ws"
)

func TestFormatProjectLabel_UsesSlugs(t *testing.T) {
	project := &api.Project{
		Name: "Payments API",
		Slug: "payments",
		Organization: api.Organization{
			Name: "Acme Incorporated",
			Slug: "acme",
		},
	}

	if got, want := formatProjectLabel(project), "acme/payments"; got != want {
		t.Fatalf("formatProjectLabel() = %q, want %q", got, want)
	}
}

func TestFormatProjectLabelEscapesBackendControls(t *testing.T) {
	project := &api.Project{
		Slug:         "payments\nInjected\x1b",
		Organization: api.Organization{Slug: "acme\tPrompt"},
	}
	want := `acme\tPrompt/payments\nInjected\x1b`
	if got := formatProjectLabel(project); got != want {
		t.Fatalf("formatProjectLabel() = %q, want %q", got, want)
	}
	_, _, _, err := resolveSources(endpoint.Base{}, project, nil, []string{"missing"})
	if err == nil || !strings.Contains(err.Error(), want) || strings.ContainsRune(err.Error(), '\x1b') {
		t.Fatalf("missing-source error = %q", err)
	}
}

func TestResolveSources_SelectsNamesInArgumentOrder(t *testing.T) {
	project := &api.Project{
		Slug:         "payments",
		Organization: api.Organization{Slug: "acme"},
	}
	available := []api.Source{
		{Name: "shopify", UID: "src_shopify", Routes: []api.Route{{UID: "rte_shopify"}}},
		{Name: "stripe", UID: "src_stripe", Routes: []api.Route{{UID: "rte_stripe"}}},
	}

	sources, uids, _, err := resolveSources(endpoint.Base{}, project, available, []string{"stripe", "shopify"})
	if err != nil {
		t.Fatalf("resolveSources() error = %v", err)
	}
	if got, want := []string{sources[0].Name, sources[1].Name}, []string{"stripe", "shopify"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected source names = %v, want %v", got, want)
	}
	if want := []string{"src_stripe", "src_shopify"}; !reflect.DeepEqual(uids, want) {
		t.Fatalf("source UIDs = %v, want %v", uids, want)
	}
}

func TestResolveSources_SelectsOnlySourcesWithRoutesWithoutFilter(t *testing.T) {
	project := &api.Project{Slug: "payments", Organization: api.Organization{Slug: "acme"}}
	available := []api.Source{
		{Name: "shopify", UID: "src_shopify", Routes: []api.Route{{UID: "rte_shopify"}}},
		{Name: "stripe", UID: "src_stripe"},
	}

	sources, uids, _, err := resolveSources(endpoint.Base{}, project, available, nil)
	if err != nil {
		t.Fatalf("resolveSources() error = %v", err)
	}
	if got, want := len(sources), 1; got != want {
		t.Fatalf("len(sources) = %d, want %d", got, want)
	}
	if got, want := sources[0].Name, "shopify"; got != want {
		t.Fatalf("source name = %q, want %q", got, want)
	}
	if len(uids) != 0 {
		t.Fatalf("source UIDs = %v, want no filter UIDs", uids)
	}
}

func TestResolveSources_SuggestsOnlyAnUnambiguousCaseMatch(t *testing.T) {
	project := &api.Project{
		Slug:         "payments",
		Organization: api.Organization{Slug: "acme"},
	}
	missing := `source "Stripe" is not present in project acme/payments`
	tests := []struct {
		name      string
		available []string
		want      string
	}{
		{"one case match", []string{"stripe", "shopify"}, missing + `; did you mean "stripe"?`},
		{"two case matches", []string{"stripe", "STRIPE"}, missing},
		{"no case match", []string{"shopify"}, missing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var available []api.Source
			for _, name := range test.available {
				available = append(available, api.Source{Name: name, Routes: []api.Route{{UID: "rte_" + name}}})
			}
			_, _, _, err := resolveSources(endpoint.Base{}, project, available, []string{"Stripe"})
			if err == nil || err.Error() != test.want {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSourceNamesByUID(t *testing.T) {
	sources := []api.Source{
		{UID: "src_1", Name: "stripe"},
		{UID: "src_2", Name: "shopify"},
	}
	want := map[string]string{"src_1": "stripe", "src_2": "shopify"}
	if got := sourceNamesByUID(sources); !reflect.DeepEqual(got, want) {
		t.Fatalf("sourceNamesByUID() = %#v, want %#v", got, want)
	}
}

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) {
	if len(value) == 0 {
		return 0, nil
	}
	return len(value) - 1, nil
}

// plainNotices reports connection states through the plain stream writer.
func plainNotices(out, errOut io.Writer, requestsURL string) *connectionNotices {
	return newConnectionNotices(cards.NewWriter(out, errOut, cards.Listen{}, requestsURL).Emit)
}

type scriptedWebSocketListener struct {
	errors []error
	calls  int
}

func (l *scriptedWebSocketListener) Listen(context.Context, ws.Handler) error {
	l.calls++
	if len(l.errors) == 0 {
		return nil
	}
	err := l.errors[0]
	l.errors = l.errors[1:]
	return err
}

func TestSuperviseListenStopsAfterInitialConnectionLimit(t *testing.T) {
	finalCause := errors.New("offline 3")
	listener := &scriptedWebSocketListener{errors: []error{
		&ws.SessionError{Kind: ws.SessionConnect, Err: errors.New("offline 1")},
		&ws.SessionError{Kind: ws.SessionConnect, Err: errors.New("offline 2")},
		&ws.SessionError{Kind: ws.SessionConnect, Err: finalCause},
	}}
	var stdout, stderr bytes.Buffer

	err := superviseListen(context.Background(), plainNotices(&stdout, &stderr, ""), listener, nil, reconnectPolicy{
		Delay:              0,
		MaxInitialAttempts: 3,
	})
	if err == nil {
		t.Fatal("superviseListen error = nil")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout without a join = %q", stdout.String())
	}
	if !errors.Is(err, finalCause) {
		t.Fatalf("error = %#v, want final connection cause", err)
	}
	if listener.calls != 3 {
		t.Fatalf("Listen calls = %d, want 3", listener.calls)
	}
	if got := strings.Count(stderr.String(), "reconnecting"); got != 2 {
		t.Fatalf("reconnect notices = %d, want 2:\n%s", got, stderr.String())
	}
	var output bytes.Buffer
	if got := HandleError(&output, err); got != 1 {
		t.Fatalf("HandleError exit code = %d, want 1", got)
	}
	wantOutput := "could not connect to Hookspot after 3 attempts: offline 3\n\nCheck your network connection and the Hookspot server URL, then try again.\n"
	if output.String() != wantOutput {
		t.Fatalf("HandleError output = %q, want %q", output.String(), wantOutput)
	}
}

func TestSuperviseListenStopsWhenReconnectNoticeFails(t *testing.T) {
	wantErr := errors.New("stderr unavailable")
	tests := []struct {
		name   string
		writer io.Writer
		want   error
	}{
		{name: "writer error", writer: failingWriter{err: wantErr}, want: wantErr},
		{name: "short write", writer: shortWriter{}, want: io.ErrShortWrite},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			listener := &scriptedWebSocketListener{errors: []error{
				&ws.SessionError{Kind: ws.SessionConnect, Err: errors.New("offline")},
				&ws.SessionError{Kind: ws.SessionConnect, Err: errors.New("must not retry")},
			}}
			err := superviseListen(context.Background(), plainNotices(io.Discard, test.writer, ""), listener, nil, reconnectPolicy{Delay: 0, MaxInitialAttempts: 3})
			if !errors.Is(err, test.want) {
				t.Fatalf("superviseListen error = %v, want %v", err, test.want)
			}
			if listener.calls != 1 {
				t.Fatalf("Listen calls = %d, want 1", listener.calls)
			}
		})
	}
}

func TestSuperviseListenRetriesIndefinitelyAfterConnection(t *testing.T) {
	listener := &scriptedWebSocketListener{errors: []error{
		&ws.SessionError{Kind: ws.SessionDisconnected, Connected: true, Err: errors.New("dropped")},
		&ws.SessionError{Kind: ws.SessionConnect, Err: errors.New("offline")},
		&ws.SessionError{Kind: ws.SessionHandler, Connected: true, Err: errors.New("handler failed")},
	}}
	var stderr bytes.Buffer

	err := superviseListen(context.Background(), plainNotices(io.Discard, &stderr, ""), listener, nil, reconnectPolicy{
		Delay:              0,
		MaxInitialAttempts: 1,
	})
	var sessionErr *ws.SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Kind != ws.SessionHandler {
		t.Fatalf("error = %#v, want handler session error", err)
	}
	if listener.calls != 3 {
		t.Fatalf("Listen calls = %d, want 3", listener.calls)
	}
	if got := strings.Count(stderr.String(), "reconnecting"); got != 2 {
		t.Fatalf("reconnect notices = %d, want 2:\n%s", got, stderr.String())
	}
}

func TestSuperviseListenEscapesReconnectErrorControls(t *testing.T) {
	listener := &scriptedWebSocketListener{errors: []error{
		&ws.SessionError{Kind: ws.SessionDisconnected, Connected: true, Err: errors.New("dropped\nInjected\t\x1b")},
		&ws.SessionError{Kind: ws.SessionHandler, Connected: true, Err: errors.New("stop")},
	}}
	var stderr bytes.Buffer

	err := superviseListen(context.Background(), plainNotices(io.Discard, &stderr, ""), listener, nil, reconnectPolicy{Delay: 0, MaxInitialAttempts: 1})
	if err == nil {
		t.Fatal("superviseListen returned nil")
	}
	output := stderr.String()
	if !strings.Contains(output, `connection lost: dropped\nInjected\t\x1b; reconnecting`) {
		t.Fatalf("reconnect notice = %q", output)
	}
	if strings.ContainsRune(output, '\x1b') || strings.Contains(output, "dropped\nInjected") {
		t.Fatalf("reconnect notice retained active controls: %q", output)
	}
}

func TestSuperviseListenDoesNotRetryFatalSessionError(t *testing.T) {
	listener := &scriptedWebSocketListener{errors: []error{
		&ws.SessionError{Kind: ws.SessionAuthentication, Err: errors.New("unauthorized")},
	}}
	var stderr bytes.Buffer

	err := superviseListen(context.Background(), plainNotices(io.Discard, &stderr, ""), listener, nil, reconnectPolicy{
		Delay:              0,
		MaxInitialAttempts: 10,
	})
	var sessionErr *ws.SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Kind != ws.SessionAuthentication {
		t.Fatalf("error = %#v, want authentication session error", err)
	}
	if listener.calls != 1 {
		t.Fatalf("Listen calls = %d, want 1", listener.calls)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected reconnect notice: %s", stderr.String())
	}
}

func TestSuperviseListenStopsWhenProjectNotFoundAfterReconnect(t *testing.T) {
	listener := &scriptedWebSocketListener{errors: []error{
		&ws.SessionError{Kind: ws.SessionDisconnected, Connected: true, Err: errors.New("dropped")},
		&ws.SessionError{Kind: ws.SessionNotFound, Err: errors.New("channel join rejected")},
		&ws.SessionError{Kind: ws.SessionConnect, Err: errors.New("must not be reached")},
	}}
	var stderr bytes.Buffer

	err := superviseListen(context.Background(), plainNotices(io.Discard, &stderr, ""), listener, nil, reconnectPolicy{Delay: 0, MaxInitialAttempts: 1})
	var sessionErr *ws.SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Kind != ws.SessionNotFound {
		t.Fatalf("error = %#v, want not-found session error", err)
	}
	if listener.calls != 2 {
		t.Fatalf("Listen calls = %d, want 2", listener.calls)
	}
	if got := strings.Count(stderr.String(), "reconnecting"); got != 1 {
		t.Fatalf("reconnect notices = %d, want 1:\n%s", got, stderr.String())
	}
}

// scriptedSessions runs one function per Listen call in place of a
// WebSocket session.
type scriptedSessions []func() error

func (s *scriptedSessions) Listen(context.Context, ws.Handler) error {
	session := (*s)[0]
	*s = (*s)[1:]
	return session()
}

func TestSuperviseListenPrintsReadyOnceAndTimesEachOutage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	notices := plainNotices(&stdout, &stderr, "https://app.example.invalid/acme/payments/requests")
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	notices.now = func() time.Time { return now }
	offline := &ws.SessionError{Kind: ws.SessionConnect, Err: errors.New("offline")}
	dropped := &ws.SessionError{Kind: ws.SessionDisconnected, Connected: true, Err: errors.New("dropped")}
	stop := &ws.SessionError{Kind: ws.SessionHandler, Connected: true, Err: errors.New("stop")}
	joinThen := func(elapsed time.Duration, err error) func() error {
		return func() error {
			now = now.Add(elapsed)
			if joinErr := notices.joined(); joinErr != nil {
				return joinErr
			}
			now = now.Add(time.Minute)
			return err
		}
	}
	sessions := scriptedSessions{
		func() error {
			now = now.Add(time.Second)
			return offline
		},
		func() error {
			if stdout.Len() != 0 {
				t.Errorf("stdout before the first join = %q", stdout.String())
			}
			return joinThen(0, dropped)()
		},
		func() error {
			now = now.Add(5 * time.Second)
			return offline
		},
		joinThen(9*time.Second, dropped),
		joinThen(3*time.Second, stop),
	}

	err := superviseListen(context.Background(), notices, &sessions, nil, reconnectPolicy{Delay: 0, MaxInitialAttempts: 3})
	if !errors.Is(err, stop) {
		t.Fatalf("superviseListen error = %v, want the fatal session error", err)
	}
	if want := "Ready. Waiting for requests (Ctrl-C to quit)\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	reconnected := "offline. Requests that arrived meanwhile were not delivered; retry them from https://app.example.invalid/acme/payments/requests\n"
	want := "connection lost: offline; reconnecting in 0s...\n" +
		"connection lost: dropped; reconnecting in 0s...\n" +
		"connection lost: offline; reconnecting in 0s...\n" +
		"Reconnected after 14s " + reconnected +
		"connection lost: dropped; reconnecting in 0s...\n" +
		"Reconnected after 3s " + reconnected
	if stderr.String() != want {
		t.Fatalf("stderr:\n%s\nwant:\n%s", stderr.String(), want)
	}
}

func TestDashboardRequestsURLUsesOnlySafeSlugs(t *testing.T) {
	base, err := endpoint.Parse("https://app.example.invalid/prefix")
	if err != nil {
		t.Fatal(err)
	}
	project := &api.Project{Slug: "payments", Organization: api.Organization{Slug: "acme"}}
	if got, want := dashboardRequestsURL(base, project), "https://app.example.invalid/prefix/acme/payments/requests"; got != want {
		t.Fatalf("dashboardRequestsURL() = %q, want %q", got, want)
	}
	for _, slug := range []string{"pay/../ments", "payments\n\x1b", "pay ments", ""} {
		project.Slug = slug
		if got := dashboardRequestsURL(base, project); got != "the dashboard" {
			t.Fatalf("dashboardRequestsURL() with slug %q = %q, want the fallback", slug, got)
		}
	}
}

func TestListenPrintsReadyAfterJoinAndReconnectNotice(t *testing.T) {
	var sessions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cli/projects/proj_payments":
			_, _ = w.Write([]byte(`{"uid":"proj_payments","name":"Payments","slug":"payments","organization":{"name":"Acme","slug":"acme"}}`))
		case "/cli/projects/proj_payments/sources":
			_, _ = w.Write([]byte(`[{"uid":"src_stripe","name":"stripe","url":"https://in.example.invalid/src_stripe","routes":[{"uid":"rte_stripe","destination":{"uid":"dst_local","path":"/"}}]}]`))
		case "/cli/websocket":
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			var join []json.RawMessage
			if err := conn.ReadJSON(&join); err != nil || len(join) != 5 {
				return
			}
			_ = conn.WriteJSON([]any{join[0], join[1], join[2], "phx_reply", map[string]any{"status": "ok", "response": map[string]any{}}})
			if sessions.Add(1) == 1 {
				return
			}
			// An invalid delivery is fatal, which ends the command after the reconnect.
			_ = conn.WriteJSON([]any{nil, nil, join[2], "delivery", map[string]any{}})
			_, _, _ = conn.ReadMessage()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := writeCommandFixture(configPath, []byte("schema_version = 1\ncli_key = 'key'\nproject = 'proj_payments'\n")); err != nil {
		t.Fatal(err)
	}

	result := runCommandProcess(t, "", developmentMetadata(server.URL), "--config", configPath, "listen", "stripe")
	if result.err == nil || !strings.Contains(result.stderr, "delivery is missing correlation fields") {
		t.Fatalf("listen = %v, stderr %q", result.err, result.stderr)
	}
	if !strings.HasSuffix(result.stdout, "\nConnecting…\nReady. Waiting for requests (Ctrl-C to quit)\n") {
		t.Fatalf("stdout = %q, want one Ready after Connecting…", result.stdout)
	}
	reconnected := regexp.MustCompile(`(?m)^Reconnected after \d+s offline\. Requests that arrived meanwhile were not delivered; retry them from ` +
		regexp.QuoteMeta(server.URL+"/acme/payments/requests") + `$`)
	if !strings.Contains(result.stderr, "connection lost: ") || !reconnected.MatchString(result.stderr) {
		t.Fatalf("stderr = %q, want the connection-lost and reconnect notices", result.stderr)
	}
	// Without a terminal on stdin there's no t command to offer.
	hint := "No requests yet. Check the whole path with a test event:\n\n" +
		`  curl -X POST 'https://in.example.invalid/src_stripe' -H 'Content-Type: application/json' -d '{"type":"hookspot.test"}'` + "\n"
	if strings.Count(result.stderr, hint) != 1 {
		t.Fatalf("stderr = %q, want the test hint once, after Ready", result.stderr)
	}
}

func TestListenJoinsProjectTopicWithAPIUIDs(t *testing.T) {
	joins := make(chan []json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cli/projects/proj_payments":
			_, _ = w.Write([]byte(`{"uid":"proj_payments","name":"Payments","slug":"payments","organization":{"name":"Acme","slug":"acme"}}`))
		case "/cli/projects/proj_payments/sources":
			_, _ = w.Write([]byte(`[{"uid":"src_stripe","name":"stripe","routes":[{"uid":"rte_stripe","destination":{"uid":"dst_local","path":"/"}}]}]`))
		case "/cli/websocket":
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			var join []json.RawMessage
			if err := conn.ReadJSON(&join); err != nil || len(join) != 5 {
				return
			}
			select {
			case joins <- join:
			default:
			}
			_ = conn.WriteJSON([]any{join[0], join[1], join[2], "phx_reply", map[string]any{
				"status":   "error",
				"response": map[string]string{"reason": "not_found"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := writeCommandFixture(configPath, []byte("schema_version = 1\ncli_key = 'key'\nproject = 'proj_payments'\n")); err != nil {
		t.Fatal(err)
	}

	result := runCommandProcess(t, "", developmentMetadata(server.URL), "--config", configPath, "listen", "stripe")
	if result.err == nil || !strings.Contains(result.stderr, "project not found: the WebSocket channel join was rejected") {
		t.Fatalf("listen = %v, stderr %q", result.err, result.stderr)
	}
	if !strings.HasSuffix(result.stdout, "Connecting…\n") {
		t.Fatalf("stdout after a rejected join = %q, want the banner without Ready", result.stdout)
	}
	select {
	case join := <-joins:
		if got := string(join[2]) + " " + string(join[3]) + " " + string(join[4]); got != `"project:proj_payments" "phx_join" {"sources":["src_stripe"]}` {
			t.Fatalf("join = %s", got)
		}
	default:
		t.Fatal("listen did not join a channel")
	}
}

// runListenAgainst runs listen against a fake server for the Acme | Payments
// project with the given sources JSON. The channel join is rejected, so a
// listen that gets past the banner ends right after it.
func runListenAgainst(t *testing.T, sources string, args ...string) (commandResult, string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cli/projects/proj_payments":
			_, _ = w.Write([]byte(`{"uid":"proj_payments","name":"Payments","slug":"payments","organization":{"name":"Acme","slug":"acme"}}`))
		case "/cli/projects/proj_payments/sources":
			_, _ = w.Write([]byte(sources))
		case "/cli/websocket":
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			var join []json.RawMessage
			if err := conn.ReadJSON(&join); err != nil || len(join) != 5 {
				return
			}
			_ = conn.WriteJSON([]any{join[0], join[1], join[2], "phx_reply", map[string]any{
				"status":   "error",
				"response": map[string]string{"reason": "not_found"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := writeCommandFixture(configPath, []byte("schema_version = 1\ncli_key = 'key'\nproject = 'proj_payments'\n")); err != nil {
		t.Fatal(err)
	}
	return runCommandProcess(t, "", developmentMetadata(server.URL), append([]string{"--config", configPath, "listen"}, args...)...), server.URL
}

func TestListenBannerNamesProjectAndWarnsAboutSources(t *testing.T) {
	sources := `[
		{"uid":"src_stripe","name":"stripe","active":true,"routes":[{"uid":"rte_stripe","destination":{"path":"/"}}]},
		{"uid":"src_github","name":"github","active":false,"routes":[{"uid":"rte_github","destination":{"path":"/"}}]},
		{"uid":"src_shopify","name":"shopify","active":true,"routes":[]}
	]`
	result, _ := runListenAgainst(t, sources, "stripe", "github", "shopify")

	if banner, _, _ := strings.Cut(result.stdout, "\n"); !strings.HasPrefix(banner, "╭─ Listening in Acme | Payments ") || !strings.Contains(banner, " 2 sources • 2 routes ") {
		t.Fatalf("stdout = %q, want the banner naming the project", result.stdout)
	}
	if strings.Contains(result.stdout, "⚠") {
		t.Fatalf("stdout = %q, want warnings on stderr only", result.stdout)
	}
	wantWarnings := "⚠ shopify has no route and is skipped. Add one in the dashboard.\n" +
		"⚠ github is disabled: requests to it are rejected. Enable it in the dashboard.\n"
	if !strings.HasPrefix(result.stderr, wantWarnings) {
		t.Fatalf("stderr = %q, want the source warnings first", result.stderr)
	}
}

func TestListenRequiresASourceWithRoutes(t *testing.T) {
	sources := `[{"uid":"src_shopify","name":"shopify","active":true,"routes":[]}]`
	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{name: "no names", wantStderr: "no sources with routes in Acme | Payments\n"},
		{
			name: "every named source skipped",
			args: []string{"shopify"},
			wantStderr: "⚠ shopify has no route and is skipped. Add one in the dashboard.\n" +
				"none of the named sources has a route\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, serverURL := runListenAgainst(t, sources, test.args...)
			want := test.wantStderr + "\nAdd a route in the dashboard: " + serverURL + "/acme/payments/routes/new\n"
			if result.err == nil || result.stdout != "" || result.stderr != want {
				t.Fatalf("listen = %v, stdout %q, stderr:\n%q\nwant:\n%q", result.err, result.stdout, result.stderr, want)
			}
		})
	}
}

func TestSuperviseListenDoesNotReconnectAfterInvalidDelivery(t *testing.T) {
	listener := &scriptedWebSocketListener{errors: []error{
		&ws.SessionError{Kind: ws.SessionProtocol, Connected: true, Err: errors.New("invalid delivery payload")},
		&ws.SessionError{Kind: ws.SessionConnect, Err: errors.New("must not be reached")},
	}}
	var stderr bytes.Buffer

	err := superviseListen(context.Background(), plainNotices(io.Discard, &stderr, ""), listener, nil, reconnectPolicy{Delay: 0, MaxInitialAttempts: 10})
	var sessionErr *ws.SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Kind != ws.SessionProtocol || listener.calls != 1 {
		t.Fatalf("error = %#v, calls = %d; want one fatal protocol attempt", err, listener.calls)
	}
	if stderr.Len() != 0 {
		t.Fatalf("fatal protocol error printed reconnect notice: %q", stderr.String())
	}
}

func TestListenPrintsRootHintOnceAndOnlyWhenForwarding(t *testing.T) {
	local := httptest.NewServer(http.NotFoundHandler())
	defer local.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cli/projects/proj_payments":
			_, _ = w.Write([]byte(`{"uid":"proj_payments","name":"Payments","slug":"payments","organization":{"name":"Acme","slug":"acme"}}`))
		case "/cli/projects/proj_payments/sources":
			_, _ = w.Write([]byte(`[{"uid":"src_stripe","name":"stripe","active":true,"routes":[{"uid":"rte_stripe","active":true,"destination":{"uid":"dst_local","path":"/"}}]}]`))
		case "/cli/websocket":
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			var join []json.RawMessage
			if err := conn.ReadJSON(&join); err != nil || len(join) != 5 {
				return
			}
			_ = conn.WriteJSON([]any{join[0], join[1], join[2], "phx_reply", map[string]any{"status": "ok", "response": map[string]any{}}})
			for _, attempt := range []string{"att_1", "att_2"} {
				_ = conn.WriteJSON([]any{nil, nil, join[2], "delivery", map[string]string{
					"attempt_uid": attempt, "request_uid": "req_1", "source_uid": "src_stripe", "method": "POST", "path": "/",
				}})
				var response []json.RawMessage
				if err := conn.ReadJSON(&response); err != nil {
					return
				}
			}
			// A delivery without correlation fields ends the command after both deliveries were answered.
			_ = conn.WriteJSON([]any{nil, nil, join[2], "delivery", map[string]string{}})
			_, _, _ = conn.ReadMessage()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := writeCommandFixture(configPath, []byte("schema_version = 1\ncli_key = 'key'\nproject = 'proj_payments'\n")); err != nil {
		t.Fatal(err)
	}
	hint := local.URL + "/ returned 404. If your webhook route is elsewhere, include it in --forward-to, e.g. --forward-to " + local.URL + "/webhooks\n"

	for _, test := range []struct {
		name  string
		args  []string
		hints int
	}{
		{name: "forwarding", args: []string{"--forward-to", local.URL}, hints: 1},
		{name: "print-only", hints: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"--config", configPath, "listen", "stripe"}, test.args...)
			result := runCommandProcess(t, "", developmentMetadata(server.URL), args...)
			if result.err == nil || !strings.Contains(result.stderr, "delivery is missing correlation fields") {
				t.Fatalf("listen = %v, stderr %q", result.err, result.stderr)
			}
			if got := strings.Count(result.stderr, hint); got != test.hints {
				t.Fatalf("root hints on stderr = %d, want %d:\n%s", got, test.hints, result.stderr)
			}
			if strings.Contains(result.stdout, "returned 404") {
				t.Fatalf("root hint printed on stdout:\n%s", result.stdout)
			}
		})
	}
}

func TestLineCommandReaderRunsEachLineAndPropagatesFailure(t *testing.T) {
	wantErr := errors.New("replay output unavailable")
	var lines []string
	canceled := make(chan struct{})
	reader := startLineCommands(context.Background(), io.NopCloser(strings.NewReader("r 1\n\nr 2\nnever run\n")), func(line string) error {
		lines = append(lines, line)
		if len(lines) == 3 {
			return wantErr
		}
		return nil
	}, func() { close(canceled) })
	<-canceled
	if err := reader.Stop(); !errors.Is(err, wantErr) {
		t.Fatalf("Stop error = %v, want the command failure", err)
	}
	if want := []string{"r 1", "", "r 2"}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines run = %q, want %q", lines, want)
	}
	if isTerminalReader(strings.NewReader("")) {
		t.Fatal("plain reader was treated as a terminal")
	}
}

func TestLineCommandReaderClosesAndJoinsOwnedReaderOnCancellation(t *testing.T) {
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	commands := startLineCommands(ctx, reader, func(string) error { return nil }, func() {})
	cancel()
	done := make(chan error, 1)
	go func() { done <- commands.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("owned command input did not stop")
	}
	if _, err := writer.Write([]byte("\n")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after stop error = %v, want closed pipe", err)
	}
}

func TestLineCommandReaderStopsAfterBlockedFallbackReadResumes(t *testing.T) {
	reader := newGatedLineReader("r 1\nr 2\n")
	ctx, cancel := context.WithCancel(context.Background())
	commands := startLineCommands(ctx, reader, func(string) error {
		t.Fatal("a line read after cancellation ran")
		return nil
	}, func() {})
	<-reader.started
	cancel()
	close(reader.release)
	select {
	case <-commands.producerDone:
	case <-time.After(time.Second):
		t.Fatal("fallback command reader did not stop after its read resumed")
	}
	if got := reader.readCount(); got != 1 {
		t.Fatalf("read count after cancellation = %d, want 1", got)
	}
	if err := commands.Stop(); err != nil {
		t.Fatalf("Stop error = %v", err)
	}
}

func TestLineCommandReaderDoesNotRunQueuedLineAfterCancellation(t *testing.T) {
	for iteration := 0; iteration < 50; iteration++ {
		firstStarted := make(chan struct{})
		releaseFirst := make(chan struct{})
		var count atomic.Int32
		ctx, cancel := context.WithCancel(context.Background())
		commands := startLineCommands(ctx, io.NopCloser(strings.NewReader("\n\n")), func(string) error {
			if count.Add(1) == 1 {
				close(firstStarted)
				<-releaseFirst
			}
			return nil
		}, func() {})
		<-firstStarted
		cancel()
		close(releaseFirst)
		if err := commands.Stop(); err != nil {
			t.Fatalf("iteration %d: Stop error = %v", iteration, err)
		}
		if got := count.Load(); got != 1 {
			t.Fatalf("iteration %d: lines run after cancellation = %d, want 1", iteration, got)
		}
	}
}

type gatedLineReader struct {
	started chan struct{}
	release chan struct{}
	data    []byte
	mu      sync.Mutex
	reads   int
}

func newGatedLineReader(value string) *gatedLineReader {
	return &gatedLineReader{
		started: make(chan struct{}),
		release: make(chan struct{}),
		data:    []byte(value),
	}
}

func (r *gatedLineReader) Read(buffer []byte) (int, error) {
	r.mu.Lock()
	r.reads++
	read := r.reads
	r.mu.Unlock()
	if read == 1 {
		close(r.started)
		<-r.release
		return copy(buffer, r.data), nil
	}
	return 0, io.EOF
}

func (r *gatedLineReader) readCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

func TestLineCommandsReplayAndAnswerTypos(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer local.Close()
	forwarder, err := proxy.New(local.URL)
	if err != nil {
		t.Fatal(err)
	}
	sources := []api.Source{{UID: "src_stripe", Name: "stripe", Routes: []api.Route{{UID: "rte_stripe", Destination: api.Destination{Path: "/hooks"}}}}}
	var stdout, stderr bytes.Buffer
	writer := cards.NewWriter(&stdout, &stderr, cards.Listen{Sources: sourceNamesByUID(sources)}, "")
	sess := session.New(context.Background(), sources, forwarder, writer)

	// ↵ before the first request has nothing to replay.
	if err := runLineCommand(sess, writer, true, ""); err != nil || stdout.Len()+stderr.Len() != 0 {
		t.Fatalf("↵ before any request = %v, output %q %q", err, stdout.String(), stderr.String())
	}
	if _, err := sess.Handle(ws.Delivery{AttemptUID: "att_1", SourceUID: "src_stripe", Method: "POST", Path: "/hooks"}); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"", "r 1", " r  2 ", "r 9", "r x", "?"} {
		if err := runLineCommand(sess, writer, true, line); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
	}

	rows := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(rows) != 4 {
		t.Fatalf("stdout = %q, want the request and three replays", stdout.String())
	}
	for i, row := range rows {
		if !strings.HasPrefix(row, "#"+strconv.Itoa(i+1)+" ") || strings.Contains(row, "↻ #") != (i > 0) {
			t.Fatalf("row %d = %q", i+1, row)
		}
	}
	help := "commands: ↵ replay last · r N replay #N · c N copy as cURL · e N export fixture · t test event\n"
	if want := "#9: no such request\n" + help + help; stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestLineCommandsCopyAndExportInInspectMode(t *testing.T) {
	t.Chdir(t.TempDir())
	sources := []api.Source{{UID: "src_stripe", Name: "stripe", URL: "https://in.hookspot.test/src_stripe", Routes: []api.Route{{UID: "rte_stripe", Destination: api.Destination{Path: "/hooks"}}}}}
	var stdout, stderr bytes.Buffer
	writer := cards.NewWriter(&stdout, &stderr, cards.Listen{Sources: sourceNamesByUID(sources)}, "")
	sess := session.New(context.Background(), sources, nil, writer)
	if _, err := sess.Handle(ws.Delivery{
		AttemptUID: "att_1", RequestUID: "req_1", SourceUID: "src_stripe", Method: "POST", Path: "/hooks",
		Headers: http.Header{"Authorization": []string{"Bearer secret"}}, Body: []byte(`{"type":"paid"}`),
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	for _, line := range []string{"c 1", "e 1", "e 9", "", "r 1", "?"} {
		if err := runLineCommand(sess, writer, false, line); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
	}

	dir, err := filepath.Abs("hookspot-fixtures")
	if err != nil {
		t.Fatal(err)
	}
	refused := "nothing to replay without --forward-to\n"
	want := "#1 as cURL, which resends it through Hookspot\n" +
		"sensitive headers are hidden; --show-sensitive-headers shows the full command\n" +
		"curl -X POST 'https://in.hookspot.test/src_stripe' \\\n" +
		"  -H 'Authorization: [redacted]' \\\n" +
		"  --data-binary '{\"type\":\"paid\"}'\n" +
		"exported #1 to " + filepath.Join(dir, "req_1.json") + " and " + filepath.Join(dir, "req_1.body") + " · sensitive headers redacted\n" +
		"#9: no such request\n" + refused + refused +
		"commands: c N copy as cURL · e N export fixture · t test event\n"
	if stderr.String() != want || stdout.Len() != 0 {
		t.Fatalf("stderr:\n%s\nwant:\n%s\nstdout: %q", stderr.String(), want, stdout.String())
	}
}

func TestLineCommandsSendTestEventsAndRefuseReplaysWhenInspecting(t *testing.T) {
	ingest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Hookspot-Test") == "" {
			t.Errorf("test event without its id: %v", r.Header)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer ingest.Close()
	sources := []api.Source{{UID: "src_stripe", Name: "stripe", URL: ingest.URL + "/in/src_stripe", Routes: []api.Route{{UID: "rte_stripe", Destination: api.Destination{Path: "/hooks"}}}}}
	var stdout, stderr bytes.Buffer
	writer := cards.NewWriter(&stdout, &stderr, cards.Listen{Sources: sourceNamesByUID(sources)}, "")
	sess := session.New(context.Background(), sources, nil, writer)
	if _, err := sess.Handle(ws.Delivery{AttemptUID: "att_1", SourceUID: "src_stripe", Method: "POST", Path: "/hooks"}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()

	for _, line := range []string{"", "r 1", "t", "t github", "x"} {
		if err := runLineCommand(sess, writer, false, line); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
	}
	want := "nothing to replay without --forward-to\n" +
		"nothing to replay without --forward-to\n" +
		"test event sent to stripe\n" +
		"github: not a source this run listens to\n" +
		"commands: c N copy as cURL · e N export fixture · t test event\n"
	if stdout.Len() != 0 || stderr.String() != want {
		t.Fatalf("stdout %q, stderr %q; want replies %q", stdout.String(), stderr.String(), want)
	}
}

func TestRunInTerminal(t *testing.T) {
	t.Run("listen ending quits the program", func(t *testing.T) {
		_, stop := context.WithCancel(context.Background())
		defer stop()
		listenErr := errors.New("project not found")
		err := runInTerminal(tui.NewProgram(nil, io.Discard, stop), tui.Stream{}, func() error { return listenErr })
		if !errors.Is(err, listenErr) {
			t.Fatalf("runInTerminal = %v, want listen's error", err)
		}
	})
	t.Run("the program failing first stops listen and wins", func(t *testing.T) {
		ctx, stop := context.WithCancel(context.Background())
		defer stop()
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		_ = writer.Close()
		_ = reader.Close()
		listenErr := errors.New("listen failed")
		err = runInTerminal(tui.NewProgram(reader, io.Discard, stop), tui.Stream{}, func() error {
			<-ctx.Done()
			return listenErr
		})
		if err == nil || errors.Is(err, listenErr) {
			t.Fatalf("runInTerminal = %v, want the program's error", err)
		}
	})
}

// listenStreamSources are two sources: one route named, one not.
const listenStreamSources = `[
	{"uid":"src_stripe","name":"stripe","url":"https://in.hookspot.test/src_stripe","active":true,"routes":[{"uid":"rte_stripe","name":"payments","destination":{"path":"/webhooks/stripe"}}]},
	{"uid":"src_github","name":"github","url":"https://in.hookspot.test/src_github","active":true,"routes":[{"uid":"rte_github","destination":{"path":"/webhooks/github"}}]}
]`

// runListenStream runs listen against a fake Hookspot whose session sends the
// deliveries in one burst, waits for every response, then sends an invalid
// delivery, which ends the command.
func runListenStream(t *testing.T, deliveries []ws.Delivery, args ...string) commandResult {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cli/projects/proj_payments":
			_, _ = w.Write([]byte(`{"uid":"proj_payments","name":"Payments","slug":"payments","organization":{"name":"Acme","slug":"acme"}}`))
		case "/cli/projects/proj_payments/sources":
			_, _ = w.Write([]byte(listenStreamSources))
		case "/cli/websocket":
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			var join []json.RawMessage
			if err := conn.ReadJSON(&join); err != nil || len(join) != 5 {
				return
			}
			_ = conn.WriteJSON([]any{join[0], join[1], join[2], "phx_reply", map[string]any{"status": "ok", "response": map[string]any{}}})
			for _, delivery := range deliveries {
				_ = conn.WriteJSON([]any{nil, nil, join[2], "delivery", delivery})
			}
			for range deliveries {
				var response []json.RawMessage
				if err := conn.ReadJSON(&response); err != nil {
					return
				}
			}
			_ = conn.WriteJSON([]any{nil, nil, join[2], "delivery", map[string]string{}})
			_, _, _ = conn.ReadMessage()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := writeCommandFixture(configPath, []byte("schema_version = 1\ncli_key = 'key'\nproject = 'proj_payments'\n")); err != nil {
		t.Fatal(err)
	}
	result := runCommandProcess(t, "", developmentMetadata(server.URL), append([]string{"--config", configPath, "listen"}, args...)...)
	if result.err == nil || !strings.Contains(result.stderr, "delivery is missing correlation fields") {
		t.Fatalf("listen = %v, stderr %q", result.err, result.stderr)
	}
	return result
}

var (
	clockPattern   = regexp.MustCompile(`\d\d:\d\d:\d\d\.\d{3}`)
	localPattern   = regexp.MustCompile(`127\.0\.0\.1:\d+`)
	latencyPattern = regexp.MustCompile(`\b\d+(\.\d+)?m?s\b`)
	gapPattern     = regexp.MustCompile(` {2,}`)
)

// maskStream hides what changes between runs: clock times, local ports and
// latencies. A latency's width moves the padding after it, so a line with one
// keeps two-space gaps only.
func maskStream(stream string) string {
	lines := strings.Split(stream, "\n")
	for i, line := range lines {
		line = clockPattern.ReplaceAllString(line, "hh:mm:ss.mmm")
		line = localPattern.ReplaceAllStringFunc(line, func(host string) string {
			return "127.0.0.1:" + strings.Repeat("x", len(host)-len("127.0.0.1:"))
		})
		if latencyPattern.MatchString(line) {
			line = gapPattern.ReplaceAllString(latencyPattern.ReplaceAllString(line, "Nms"), "  ")
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func TestListenStream(t *testing.T) {
	jsonHeaders := http.Header{"Content-Type": []string{"application/json"}}
	t.Run("inspect", func(t *testing.T) {
		result := runListenStream(t, []ws.Delivery{
			{
				AttemptUID: "att_1", RequestUID: "req_stripe_1", SourceUID: "src_stripe", Method: "POST", Path: "/webhooks/stripe", Query: "attempt=1",
				Headers: http.Header{"Content-Type": []string{"application/json"}, "Authorization": []string{"Bearer secret"}},
				Body:    []byte(`{"type":"payment_intent.succeeded","amount":2000}`),
			},
			{
				AttemptUID: "att_2", RequestUID: "req_github_1", SourceUID: "src_github", Method: "POST", Path: "/webhooks/github",
				Headers: http.Header{"X-Github-Event": []string{"push"}},
				Body:    []byte("ref=refs/heads/main"),
			},
		})
		if strings.ContainsRune(result.stdout, '\x1b') {
			t.Fatalf("piped stream has ANSI codes: %q", result.stdout)
		}
		golden.RequireEqual(t, maskStream(result.stdout))
	})
	t.Run("forward", func(t *testing.T) {
		local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/webhooks/github" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"error":"missing installation"}`))
			}
		}))
		defer local.Close()
		// Rows and cards of two sources interleave in one burst.
		result := runListenStream(t, []ws.Delivery{
			{AttemptUID: "att_1", RequestUID: "req_stripe_1", SourceUID: "src_stripe", Method: "POST", Path: "/webhooks/stripe", Headers: jsonHeaders, Body: []byte(`{"type":"payment_intent.succeeded"}`)},
			{AttemptUID: "att_2", RequestUID: "req_github_1", SourceUID: "src_github", Method: "POST", Path: "/webhooks/github", Headers: jsonHeaders, Body: []byte(`{"action":"opened"}`)},
			{AttemptUID: "att_3", RequestUID: "req_stripe_2", SourceUID: "src_stripe", Method: "POST", Path: "/webhooks/stripe", Headers: jsonHeaders, Body: []byte(`{"type":"invoice.paid"}`)},
		}, "--forward-to", local.URL)
		if strings.ContainsRune(result.stdout, '\x1b') {
			t.Fatalf("piped stream has ANSI codes: %q", result.stdout)
		}
		golden.RequireEqual(t, maskStream(result.stdout))
	})
}
