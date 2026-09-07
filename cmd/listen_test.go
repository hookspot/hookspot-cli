package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hookspot/internal/api"
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
	_, _, err := resolveSources(project, nil, []string{"missing"})
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
		{Name: "shopify", UID: "src_shopify", Connections: []api.Connection{{UID: "conn_shopify"}}},
		{Name: "stripe", UID: "src_stripe", Connections: []api.Connection{{UID: "conn_stripe"}}},
	}

	sources, uids, err := resolveSources(project, available, []string{"stripe", "shopify"})
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

func TestResolveSources_SelectsOnlySourcesWithConnectionsWithoutFilter(t *testing.T) {
	project := &api.Project{Slug: "payments", Organization: api.Organization{Slug: "acme"}}
	available := []api.Source{
		{Name: "shopify", UID: "src_shopify", Connections: []api.Connection{{UID: "conn_shopify"}}},
		{Name: "stripe", UID: "src_stripe"},
	}

	sources, uids, err := resolveSources(project, available, nil)
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

func TestResolveSources_ReturnsErrorWithoutMatchingConnections(t *testing.T) {
	project := &api.Project{Slug: "payments", Organization: api.Organization{Slug: "acme"}}
	available := []api.Source{{Name: "shopify", UID: "src_shopify"}}

	_, _, err := resolveSources(project, available, []string{"shopify"})
	if err == nil {
		t.Fatal("resolveSources() returned nil error")
	}
	if got, want := err.Error(), "no matching connections found"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestResolveSources_RejectsNameMissingFromProject(t *testing.T) {
	project := &api.Project{
		Slug:         "payments",
		Organization: api.Organization{Slug: "acme"},
	}
	available := []api.Source{{Name: "shopify", UID: "src_shopify"}}

	_, _, err := resolveSources(project, available, []string{"missing"})
	if err == nil {
		t.Fatal("resolveSources() returned nil error")
	}
	if got, want := err.Error(), `source "missing" is not present in project acme/payments`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestPrintListenInfo_ShowsSourceURLsAndConnections(t *testing.T) {
	var buf bytes.Buffer
	connectionName := "cli-shopify"
	sources := []api.Source{
		{
			Name: "shopify",
			URL:  "https://events.example.com/shopify",
			Connections: []api.Connection{
				{
					Name:        &connectionName,
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
	if err := printListenInfoWithReplay(&buf, sources, forwarder, false); err != nil {
		t.Fatal(err)
	}

	want := "Listening on 1 source • 1 connection\n" +
		"\n" +
		"shopify\n" +
		"│  Requests to → https://events.example.com/shopify\n" +
		"└─ Forwards to → http://localhost:3000/webhooks/shopify (cli-shopify)\n" +
		"\n" +
		"Requests ──────────────────────────────────────\n" +
		"\n" +
		"Waiting for requests...\n"
	if got := buf.String(); got != want {
		t.Fatalf("listen info output:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintListenInfo_ShowsTerminalOutput(t *testing.T) {
	var buf bytes.Buffer
	sources := []api.Source{
		{
			Name:        "shopify",
			URL:         "https://events.example.com/shopify",
			Connections: []api.Connection{{UID: "conn_shopify"}},
		},
	}

	if err := printListenInfoWithReplay(&buf, sources, nil, false); err != nil {
		t.Fatal(err)
	}

	want := "Listening on 1 source • 1 connection\n" +
		"\n" +
		"shopify\n" +
		"├ Requests to → https://events.example.com/shopify\n" +
		"└ Output      → terminal\n" +
		"\n" +
		"Requests ──────────────────────────────────────\n" +
		"\n" +
		"Waiting for requests...\n"
	if got := buf.String(); got != want {
		t.Fatalf("listen info output:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintListenInfoReturnsWriterAndShortWriteFailures(t *testing.T) {
	sources := []api.Source{{Name: "shopify", URL: "https://events.example.invalid", Connections: []api.Connection{{UID: "conn_1"}}}}
	wantErr := errors.New("stdout unavailable")
	if err := printListenInfoWithReplay(failingWriter{err: wantErr}, sources, nil, false); !errors.Is(err, wantErr) {
		t.Fatalf("writer error = %v, want output failure", err)
	}
	if err := printListenInfoWithReplay(shortWriter{}, sources, nil, false); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write error = %v, want io.ErrShortWrite", err)
	}
}

func TestPrintListenInfoEscapesHostileSourceFieldsInBothModes(t *testing.T) {
	sources := []api.Source{{
		Name: "source\nInjected\x1b",
		URL:  "https://example.invalid/path\tPrompt\x1b",
		Connections: []api.Connection{{
			Destination: api.Destination{Path: "/webhook"},
		}},
	}}
	forwarder, err := proxy.New("http://localhost:3000")
	if err != nil {
		t.Fatal(err)
	}
	for _, selectedForwarder := range []*proxy.Forwarder{nil, forwarder} {
		var output bytes.Buffer
		if err := printListenInfoWithReplay(&output, sources, selectedForwarder, false); err != nil {
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

func TestConnectionLabel(t *testing.T) {
	name := "named-destination"
	tests := []struct {
		name       string
		connection api.Connection
		want       string
	}{
		{"name", api.Connection{Name: &name, DisplayName: "shopify -> fallback"}, "named-destination"},
		{"generated display name", api.Connection{DisplayName: "shopify -> /webhooks/shopify"}, ""},
		{"missing", api.Connection{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := connectionLabel(tt.connection); got != tt.want {
				t.Fatalf("connectionLabel() = %q, want %q", got, tt.want)
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

func TestPrintListenInfo_ShowsReplayHintOnlyWhenEnabled(t *testing.T) {
	var output bytes.Buffer
	sources := []api.Source{{
		Name: "stripe",
		URL:  "https://events.example.com/stripe",
		Connections: []api.Connection{{
			Destination: api.Destination{Path: "/api/webhooks"},
		}},
	}}

	forwarder, err := proxy.New("http://localhost:3000")
	if err != nil {
		t.Fatal(err)
	}
	if err := printListenInfoWithReplay(&output, sources, forwarder, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "└─ Forwards to → http://localhost:3000/api/webhooks") {
		t.Fatalf("exact forwarding URL missing:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "↵ replay last request") {
		t.Fatalf("replay hint missing:\n%s", output.String())
	}

	output.Reset()
	if err := printListenInfoWithReplay(&output, sources, forwarder, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "replay last request") {
		t.Fatalf("replay hint shown for non-interactive input:\n%s", output.String())
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
	var stderr bytes.Buffer

	err := superviseListen(context.Background(), &stderr, listener, nil, reconnectPolicy{
		Delay:              0,
		MaxInitialAttempts: 3,
	})
	if err == nil {
		t.Fatal("superviseListen error = nil")
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
			err := superviseListen(context.Background(), test.writer, listener, nil, reconnectPolicy{Delay: 0, MaxInitialAttempts: 3})
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

	err := superviseListen(context.Background(), &stderr, listener, nil, reconnectPolicy{
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

	err := superviseListen(context.Background(), &stderr, listener, nil, reconnectPolicy{Delay: 0, MaxInitialAttempts: 1})
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

	err := superviseListen(context.Background(), &stderr, listener, nil, reconnectPolicy{
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

func TestSuperviseListenDoesNotReconnectAfterInvalidDelivery(t *testing.T) {
	listener := &scriptedWebSocketListener{errors: []error{
		&ws.SessionError{Kind: ws.SessionProtocol, Connected: true, Err: errors.New("invalid delivery payload")},
		&ws.SessionError{Kind: ws.SessionConnect, Err: errors.New("must not be reached")},
	}}
	var stderr bytes.Buffer

	err := superviseListen(context.Background(), &stderr, listener, nil, reconnectPolicy{Delay: 0, MaxInitialAttempts: 10})
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
