package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"hookspot/internal/api"
	"hookspot/internal/endpoint"
	"hookspot/internal/printer"
	"hookspot/internal/proxy"
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
	_, _, err := resolveSources(io.Discard, endpoint.Base{}, project, nil, []string{"missing"})
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

	sources, uids, err := resolveSources(io.Discard, endpoint.Base{}, project, available, []string{"stripe", "shopify"})
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

	sources, uids, err := resolveSources(io.Discard, endpoint.Base{}, project, available, nil)
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
			_, _, err := resolveSources(io.Discard, endpoint.Base{}, project, available, []string{"Stripe"})
			if err == nil || err.Error() != test.want {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

var listenTestProject = &api.Project{Name: "Payments", Organization: api.Organization{Name: "Acme"}}

func TestPrintListenInfo_ShowsSourceURLsAndRoutes(t *testing.T) {
	var buf bytes.Buffer
	routeName := "cli-shopify"
	sources := []api.Source{
		{
			Name: "shopify",
			URL:  "https://events.example.com/shopify",
			Routes: []api.Route{
				{
					Name:        &routeName,
					DisplayName: "shopify -> cli-shopify",
					Destination: api.Destination{Path: "/webhooks/shopify"},
				},
			},
		},
	}

	forwarder, err := proxy.New("http://localhost:3000")
	if err != nil {
		t.Fatal(err)
	}
	if err := printListenInfo(&buf, listenTestProject, sources, forwarder); err != nil {
		t.Fatal(err)
	}

	want := "Listening in Acme | Payments on 1 source • 1 route\n" +
		"\n" +
		"shopify\n" +
		"│  Requests to → https://events.example.com/shopify\n" +
		"└─ Forwards to → http://localhost:3000/webhooks/shopify (cli-shopify)\n" +
		"\n" +
		"Requests ──────────────────────────────────────\n" +
		"\n" +
		"Connecting…\n"
	if got := buf.String(); got != want {
		t.Fatalf("listen info output:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintListenInfo_ShowsTerminalOutput(t *testing.T) {
	var buf bytes.Buffer
	sources := []api.Source{
		{
			Name:   "shopify",
			URL:    "https://events.example.com/shopify",
			Routes: []api.Route{{UID: "rte_shopify"}},
		},
	}

	if err := printListenInfo(&buf, listenTestProject, sources, nil); err != nil {
		t.Fatal(err)
	}

	want := "Listening in Acme | Payments on 1 source • 1 route\n" +
		"\n" +
		"shopify\n" +
		"├ Requests to → https://events.example.com/shopify\n" +
		"└ Output      → terminal\n" +
		"\n" +
		"Requests ──────────────────────────────────────\n" +
		"\n" +
		"Connecting…\n"
	if got := buf.String(); got != want {
		t.Fatalf("listen info output:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintListenInfo_CountsRoutesAcrossSources(t *testing.T) {
	var buf bytes.Buffer
	sources := []api.Source{
		{Name: "shopify", Routes: []api.Route{{UID: "rte_1"}, {UID: "rte_2"}}},
		{Name: "stripe", Routes: []api.Route{{UID: "rte_3"}}},
	}

	if err := printListenInfo(&buf, listenTestProject, sources, nil); err != nil {
		t.Fatal(err)
	}

	if got, want := strings.SplitN(buf.String(), "\n", 2)[0], "Listening in Acme | Payments on 2 sources • 3 routes"; got != want {
		t.Fatalf("banner = %q, want %q", got, want)
	}
}

func TestPrintListenInfoReturnsWriterAndShortWriteFailures(t *testing.T) {
	sources := []api.Source{{Name: "shopify", URL: "https://events.example.invalid", Routes: []api.Route{{UID: "rte_1"}}}}
	wantErr := errors.New("stdout unavailable")
	if err := printListenInfo(failingWriter{err: wantErr}, listenTestProject, sources, nil); !errors.Is(err, wantErr) {
		t.Fatalf("writer error = %v, want output failure", err)
	}
	if err := printListenInfo(shortWriter{}, listenTestProject, sources, nil); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write error = %v, want io.ErrShortWrite", err)
	}
}

func TestPrintListenInfoEscapesHostileSourceFieldsInBothModes(t *testing.T) {
	sources := []api.Source{{
		Name: "source\nInjected\x1b",
		URL:  "https://example.invalid/path\tPrompt\x1b",
		Routes: []api.Route{{
			Destination: api.Destination{Path: "/webhook"},
		}},
	}}
	forwarder, err := proxy.New("http://localhost:3000")
	if err != nil {
		t.Fatal(err)
	}
	for _, selectedForwarder := range []*proxy.Forwarder{nil, forwarder} {
		var output bytes.Buffer
		if err := printListenInfo(&output, listenTestProject, sources, selectedForwarder); err != nil {
			t.Fatal(err)
		}
		text := output.String()
		for _, want := range []string{`source\nInjected\x1b`, `path\tPrompt\x1b`} {
			if !strings.Contains(text, want) {
				t.Fatalf("escaped field %q missing from %q", want, text)
			}
		}
		if strings.ContainsRune(text, '\x1b') || strings.Contains(text, "source\nInjected") {
			t.Fatalf("hostile field remained active in %q", text)
		}
	}
}

func TestRouteLabel(t *testing.T) {
	name := "named-destination"
	tests := []struct {
		name  string
		route api.Route
		want  string
	}{
		{"name", api.Route{Name: &name, DisplayName: "shopify -> fallback"}, "named-destination"},
		{"generated display name", api.Route{DisplayName: "shopify -> /webhooks/shopify"}, ""},
		{"missing", api.Route{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := routeLabel(tt.route); got != tt.want {
				t.Fatalf("routeLabel() = %q, want %q", got, tt.want)
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

func TestReplayCacheDeepCopiesAndReplacesLatestDelivery(t *testing.T) {
	var cache replayCache
	first := ws.Delivery{
		RequestUID: "req_1",
		SourceUID:  "src_1",
		Method:     http.MethodPost,
		Path:       "/first",
		Query:      "a=1",
		Headers:    http.Header{"X-Test": []string{"original"}},
		Body:       []byte("original"),
	}
	cache.Store(first)
	first.Headers["X-Test"][0] = "mutated"
	first.Body[0] = 'X'

	cached, ok := cache.Load()
	if !ok {
		t.Fatal("cache is empty")
	}
	if got := cached.Headers.Get("X-Test"); got != "original" {
		t.Fatalf("cached header = %q, want original", got)
	}
	if got := string(cached.Body); got != "original" {
		t.Fatalf("cached body = %q, want original", got)
	}

	cached.Headers.Set("X-Test", "changed after load")
	cached.Body[0] = 'Y'
	again, _ := cache.Load()
	if got := again.Headers.Get("X-Test"); got != "original" {
		t.Fatalf("cache load shared header data: %q", got)
	}
	if got := string(again.Body); got != "original" {
		t.Fatalf("cache load shared body data: %q", got)
	}

	cache.Store(ws.Delivery{RequestUID: "req_2", Path: "/second"})
	latest, _ := cache.Load()
	if latest.RequestUID != "req_2" || latest.Path != "/second" {
		t.Fatalf("latest delivery = %#v, want req_2 /second", latest)
	}
}

type forwardCall struct {
	method  string
	path    string
	query   string
	body    []byte
	headers http.Header
}

type fakeForwarder struct {
	calls    []forwardCall
	status   int
	body     string
	response http.Header
	err      error
}

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) {
	if len(value) == 0 {
		return 0, nil
	}
	return len(value) - 1, nil
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

	err := superviseListen(context.Background(), newConnectionNotices(&stdout, &stderr, false, ""), listener, nil, reconnectPolicy{
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
			err := superviseListen(context.Background(), newConnectionNotices(io.Discard, test.writer, false, ""), listener, nil, reconnectPolicy{Delay: 0, MaxInitialAttempts: 3})
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

	err := superviseListen(context.Background(), newConnectionNotices(io.Discard, &stderr, false, ""), listener, nil, reconnectPolicy{
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

	err := superviseListen(context.Background(), newConnectionNotices(io.Discard, &stderr, false, ""), listener, nil, reconnectPolicy{Delay: 0, MaxInitialAttempts: 1})
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

	err := superviseListen(context.Background(), newConnectionNotices(io.Discard, &stderr, false, ""), listener, nil, reconnectPolicy{
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

	err := superviseListen(context.Background(), newConnectionNotices(io.Discard, &stderr, false, ""), listener, nil, reconnectPolicy{Delay: 0, MaxInitialAttempts: 1})
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
	notices := newConnectionNotices(&stdout, &stderr, true, "https://app.example.invalid/acme/payments/requests")
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
	if want := "Ready. Waiting for requests (Ctrl-C to quit)\n↵ replay last request\n"; stdout.String() != want {
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

	if !strings.HasPrefix(result.stdout, "Listening in Acme | Payments on 2 sources • 2 routes\n") {
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

	err := superviseListen(context.Background(), newConnectionNotices(io.Discard, &stderr, false, ""), listener, nil, reconnectPolicy{Delay: 0, MaxInitialAttempts: 10})
	var sessionErr *ws.SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Kind != ws.SessionProtocol || listener.calls != 1 {
		t.Fatalf("error = %#v, calls = %d; want one fatal protocol attempt", err, listener.calls)
	}
	if stderr.Len() != 0 {
		t.Fatalf("fatal protocol error printed reconnect notice: %q", stderr.String())
	}
}

func (f *fakeForwarder) Forward(_ context.Context, method, path, query string, body []byte, headers http.Header) (*http.Response, error) {
	f.calls = append(f.calls, forwardCall{
		method:  method,
		path:    path,
		query:   query,
		body:    append([]byte(nil), body...),
		headers: headers.Clone(),
	})
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{
		StatusCode: f.status,
		Header:     f.response.Clone(),
		Body:       io.NopCloser(strings.NewReader(f.body)),
	}, nil
}

func (f *fakeForwarder) DestinationURL(path, _ string) (*url.URL, error) {
	return url.Parse("http://localhost:3000" + path)
}

func TestForwardSessionReplayIsLocalOnlyAndReusesRequestUID(t *testing.T) {
	var output bytes.Buffer
	p := printer.New(&output, printer.Options{
		Sources: map[string]string{"src_1": "stripe"},
	})
	forwarder := &fakeForwarder{
		status:   http.StatusOK,
		body:     "ok",
		response: http.Header{"Content-Type": []string{"text/plain"}},
	}
	session := newForwardSession(context.Background(), forwarder, "http://localhost:3000", p)
	times := []time.Time{
		time.Unix(0, 0), time.Unix(0, int64(3*time.Millisecond)),
		time.Unix(0, int64(10*time.Millisecond)), time.Unix(0, int64(14*time.Millisecond)),
	}
	session.now = func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}

	delivery := ws.Delivery{
		AttemptUID: "att_1",
		RequestUID: "req_1",
		SourceUID:  "src_1",
		Method:     http.MethodPut,
		Path:       "/api/webhooks",
		Query:      "a=1",
		Headers:    http.Header{"X-Test": []string{"original"}},
		Body:       []byte(`{"type":"created"}`),
	}
	upstream, err := session.Handle(delivery)
	if err != nil || upstream.Status != http.StatusOK {
		t.Fatalf("Handle response = %#v, %v", upstream, err)
	}
	if upstream.LatencyMS != 3 {
		t.Fatalf("upstream latency_ms = %d, want 3", upstream.LatencyMS)
	}
	delivery.Body[0] = 'X'
	delivery.Headers.Set("X-Test", "mutated")
	session.Replay()

	if len(forwarder.calls) != 2 {
		t.Fatalf("local forward count = %d, want 2", len(forwarder.calls))
	}
	if got := string(forwarder.calls[1].body); got != `{"type":"created"}` {
		t.Fatalf("replay body = %q, want original", got)
	}
	if got := forwarder.calls[1].headers.Get("X-Test"); got != "original" {
		t.Fatalf("replay header = %q, want original", got)
	}
	if got := output.String(); !strings.Contains(got, "id req_1  replay  created") {
		t.Fatalf("replay output did not reuse request id or show tag:\n%s", got)
	}
	if strings.Contains(output.String(), "att_1") {
		t.Fatal("attempt ID exposed in replay output")
	}
}

func TestForwardSessionReturnsUpstream502ForTransportFailure(t *testing.T) {
	var output bytes.Buffer
	p := printer.New(&output, printer.Options{
		Sources: map[string]string{"src_1": "stripe"},
	})
	forwarder := &fakeForwarder{err: errors.New("network unavailable")}
	session := newForwardSession(context.Background(), forwarder, "http://localhost:3000", p)
	times := []time.Time{time.Unix(0, 0), time.Unix(0, int64(7*time.Millisecond))}
	session.now = func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}

	response, err := session.Handle(ws.Delivery{RequestUID: "req_1", SourceUID: "src_1", Path: "/hook"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if response.Status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 upstream", response.Status)
	}
	if response.LatencyMS != 7 {
		t.Fatalf("latency_ms = %d, want 7", response.LatencyMS)
	}
	if strings.Contains(output.String(), "502") {
		t.Fatalf("transport failure displayed as 502:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "✗ transport error") {
		t.Fatalf("transport category missing:\n%s", output.String())
	}
}

func TestForwardSessionPreservesCompletedRedirectWhenDisplayFails(t *testing.T) {
	wantErr := errors.New("output unavailable")
	p := printer.New(failingWriter{err: wantErr}, printer.Options{})
	forwarder := &fakeForwarder{
		status:   http.StatusTemporaryRedirect,
		body:     "redirect response",
		response: http.Header{"Location": []string{"/next"}},
	}
	session := newForwardSession(context.Background(), forwarder, "http://localhost:3000", p)
	session.now = func() time.Time { return time.Unix(0, 0) }

	response, err := session.Handle(ws.Delivery{RequestUID: "req_1", SourceUID: "src_1", Path: "/hook"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Handle error = %v, want display failure", err)
	}
	if response.Status != http.StatusTemporaryRedirect || response.Headers.Get("Location") != "/next" || string(response.Body) != "redirect response" {
		t.Fatalf("completed response changed after display failure: %#v", response)
	}
}

func TestForwardSessionRejectsOversizedResponseWithoutAcknowledgement(t *testing.T) {
	var output bytes.Buffer
	p := printer.New(&output, printer.Options{})
	forwarder := &fakeForwarder{
		status: http.StatusOK,
		body:   strings.Repeat("x", 16*1024*1024+1),
	}
	session := newForwardSession(context.Background(), forwarder, "http://localhost:3000", p)

	response, err := session.Handle(ws.Delivery{RequestUID: "req_1", SourceUID: "src_1", Path: "/hook"})
	if err == nil || !strings.Contains(err.Error(), "local response body exceeds 16 MiB limit") {
		t.Fatalf("Handle error = %v, want response limit error", err)
	}
	if response.Status != 0 {
		t.Fatalf("response status = %d, want no acknowledgement", response.Status)
	}
	if strings.Contains(output.String(), strings.Repeat("x", 64)) {
		t.Fatal("oversized local response was printed")
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

func TestForwardSessionRootHintOnlyForRootNotFoundOrNotAllowed(t *testing.T) {
	tests := []struct {
		name   string
		base   string
		path   string
		status int
		want   bool
	}{
		{name: "root 405", path: "/", status: http.StatusMethodNotAllowed, want: true},
		{name: "root 500", path: "/", status: http.StatusInternalServerError},
		{name: "non-root path", path: "/hooks", status: http.StatusNotFound},
		{name: "root under base path", base: "/webhooks", path: "/", status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer local.Close()
			forwarder, err := proxy.New(local.URL + test.base)
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			session := newForwardSession(context.Background(), forwarder, forwarder.String(), printer.New(&stdout, printer.Options{Notices: &stderr}))

			if _, err := session.Handle(ws.Delivery{RequestUID: "req_1", Method: http.MethodPost, Path: test.path}); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(stderr.String(), "include it in --forward-to"); got != test.want {
				t.Fatalf("root hint = %v, want %v; stderr %q", got, test.want, stderr.String())
			}
		})
	}
}

func TestReplayInputRespondsOnlyToEnterAndPropagatesFailure(t *testing.T) {
	wantErr := errors.New("replay output unavailable")
	count := 0
	canceled := make(chan struct{})
	session := startReplayInput(context.Background(), io.NopCloser(strings.NewReader("\nnot enter\n\n")), func() error {
		count++
		if count == 2 {
			return wantErr
		}
		return nil
	}, func() { close(canceled) })
	<-canceled
	if err := session.Stop(); !errors.Is(err, wantErr) {
		t.Fatalf("Stop error = %v, want replay failure", err)
	}
	if count != 2 {
		t.Fatalf("replay count = %d, want 2", count)
	}
	if isTerminalReader(strings.NewReader("")) {
		t.Fatal("plain reader was treated as a terminal")
	}
}

func TestReplayInputClosesAndJoinsOwnedReaderOnCancellation(t *testing.T) {
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	session := startReplayInput(ctx, reader, func() error { return nil }, func() {})
	cancel()
	done := make(chan error, 1)
	go func() { done <- session.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("owned replay input did not stop")
	}
	if _, err := writer.Write([]byte("\n")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after stop error = %v, want closed pipe", err)
	}
}

func TestReplayInputStopsAfterBlockedFallbackReadResumes(t *testing.T) {
	reader := newGatedReplayReader("ignored\nstill ignored\n")
	ctx, cancel := context.WithCancel(context.Background())
	session := startReplayInput(ctx, reader, func() error {
		t.Fatal("nonempty input triggered replay")
		return nil
	}, func() {})
	<-reader.started
	cancel()
	close(reader.release)
	select {
	case <-session.producerDone:
	case <-time.After(time.Second):
		t.Fatal("fallback replay reader did not stop after its read resumed")
	}
	if got := reader.readCount(); got != 1 {
		t.Fatalf("read count after cancellation = %d, want 1", got)
	}
	if err := session.Stop(); err != nil {
		t.Fatalf("Stop error = %v", err)
	}
}

func TestReplayInputDoesNotRunQueuedReplayAfterCancellation(t *testing.T) {
	for iteration := 0; iteration < 50; iteration++ {
		firstStarted := make(chan struct{})
		releaseFirst := make(chan struct{})
		var count atomic.Int32
		ctx, cancel := context.WithCancel(context.Background())
		session := startReplayInput(ctx, io.NopCloser(strings.NewReader("\n\n")), func() error {
			if count.Add(1) == 1 {
				close(firstStarted)
				<-releaseFirst
			}
			return nil
		}, func() {})
		<-firstStarted
		cancel()
		close(releaseFirst)
		if err := session.Stop(); err != nil {
			t.Fatalf("iteration %d: Stop error = %v", iteration, err)
		}
		if got := count.Load(); got != 1 {
			t.Fatalf("iteration %d: replay count after cancellation = %d, want 1", iteration, got)
		}
	}
}

type gatedReplayReader struct {
	started chan struct{}
	release chan struct{}
	data    []byte
	mu      sync.Mutex
	reads   int
}

func newGatedReplayReader(value string) *gatedReplayReader {
	return &gatedReplayReader{
		started: make(chan struct{}),
		release: make(chan struct{}),
		data:    []byte(value),
	}
}

func (r *gatedReplayReader) Read(buffer []byte) (int, error) {
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

func (r *gatedReplayReader) readCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

func TestForwardSessionReplayReturnsDisplayFailure(t *testing.T) {
	wantErr := errors.New("output unavailable")
	p := printer.New(failingWriter{err: wantErr}, printer.Options{})
	forwarder := &fakeForwarder{status: http.StatusOK, body: "ok"}
	session := newForwardSession(context.Background(), forwarder, "http://localhost:3000", p)
	session.cache.Store(ws.Delivery{RequestUID: "req_1", SourceUID: "src_1", Method: http.MethodPost, Path: "/hook"})
	if err := session.Replay(); !errors.Is(err, wantErr) {
		t.Fatalf("Replay error = %v, want display failure", err)
	}
}

func TestLatencyMilliseconds(t *testing.T) {
	tests := []struct {
		latency time.Duration
		want    int64
	}{
		{latency: 0, want: 0},
		{latency: -time.Millisecond, want: 0},
		{latency: 100 * time.Microsecond, want: 1},
		{latency: 38*time.Millisecond + 400*time.Microsecond, want: 38},
		{latency: 38*time.Millisecond + 600*time.Microsecond, want: 39},
	}

	for _, test := range tests {
		if got := latencyMilliseconds(test.latency); got != test.want {
			t.Errorf("latencyMilliseconds(%s) = %d, want %d", test.latency, got, test.want)
		}
	}
}
