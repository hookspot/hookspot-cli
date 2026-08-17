package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"hookspot/internal/api"
	"hookspot/internal/printer"
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

	printListenInfo(&buf, sources, "http://localhost:3000")

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
		t.Fatalf("printListenInfo() output:\n%q\nwant:\n%q", got, want)
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

	printListenInfo(&buf, sources, "")

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
		t.Fatalf("printListenInfo() output:\n%q\nwant:\n%q", got, want)
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

func TestForwardBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare host and port", "localhost:3000", "http://localhost:3000"},
		{"http url kept", "http://localhost:3000", "http://localhost:3000"},
		{"https url kept", "https://example.com/hooks", "https://example.com/hooks"},
		{"host only", "host.docker.internal", "http://host.docker.internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := forwardBaseURL(tt.in); got != tt.want {
				t.Errorf("forwardBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
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

	printListenInfoWithReplay(&output, sources, "http://localhost:3000", true)
	if !strings.Contains(output.String(), "└─ Forwards to → http://localhost:3000/api/webhooks") {
		t.Fatalf("exact forwarding URL missing:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "↵ replay last request") {
		t.Fatalf("replay hint missing:\n%s", output.String())
	}

	output.Reset()
	printListenInfoWithReplay(&output, sources, "http://localhost:3000", false)
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
		Mode:    printer.ModeForward,
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
		Mode:    printer.ModeForward,
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

func TestReplayInputRespondsOnlyToEnter(t *testing.T) {
	count := 0
	replayInput(context.Background(), strings.NewReader("\nnot enter\n\n"), func() { count++ })
	if count != 2 {
		t.Fatalf("replay count = %d, want 2", count)
	}
	if isTerminalReader(strings.NewReader("")) {
		t.Fatal("plain reader was treated as a terminal")
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
