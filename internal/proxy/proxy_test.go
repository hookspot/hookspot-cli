package proxy

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func mustForwarder(t *testing.T, target string) *Forwarder {
	t.Helper()
	f, err := New(target)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestForwarder_Forward_ReplaysRequest(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotBody, gotHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotHeader = r.Header.Get("X-Hookspot-Event")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		gotBody = string(body)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	f := mustForwarder(t, server.URL)

	headers := http.Header{}
	headers.Set("X-Hookspot-Event", "evt_123")

	resp, err := f.Forward(context.Background(), http.MethodPut, "/webhooks", "a=1&b=2", []byte(`{"id":"evt_123"}`), headers)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("method = %q, want %q", gotMethod, http.MethodPut)
	}
	if gotPath != "/webhooks" {
		t.Fatalf("path = %q, want %q", gotPath, "/webhooks")
	}
	if gotQuery != "a=1&b=2" {
		t.Fatalf("query = %q, want %q", gotQuery, "a=1&b=2")
	}
	if gotBody != `{"id":"evt_123"}` {
		t.Fatalf("body = %q, want %q", gotBody, `{"id":"evt_123"}`)
	}
	if gotHeader != "evt_123" {
		t.Fatalf("header = %q, want %q", gotHeader, "evt_123")
	}
}

func TestForwarder_Forward_DefaultsToPOST(t *testing.T) {
	var gotMethod string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	f := mustForwarder(t, server.URL)

	resp, err := f.Forward(context.Background(), "", "/webhooks", "", nil, nil)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	defer resp.Body.Close()

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want %q", gotMethod, http.MethodPost)
	}
}

func TestForwarder_Forward_AppendsDestinationPathToTargetPath(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := mustForwarder(t, server.URL+"/local").Forward(
		context.Background(),
		http.MethodPost,
		"/webhooks/shopify",
		"",
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	defer resp.Body.Close()

	if got, want := gotPath, "/local/webhooks/shopify"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestForwarderDestinationURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		path string
		want string
	}{
		{"destination path", "http://localhost:4000", "/webhooks/shopify", "http://localhost:4000/webhooks/shopify"},
		{"base path", "http://localhost:4000/local/", "/webhooks/shopify", "http://localhost:4000/local/webhooks/shopify"},
		{"path without slash", "http://localhost:4000", "webhooks/shopify", "http://localhost:4000/webhooks/shopify"},
		{"empty path", "http://localhost:4000", "", "http://localhost:4000/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := mustForwarder(t, tt.base)
			got, err := f.DestinationURL(tt.path, "")
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Fatalf("DestinationURL(%q, %q) = %q, want %q", tt.base, tt.path, got, tt.want)
			}
		})
	}
}

func TestForwarderRejectsDestinationPathsThatWouldBeRewritten(t *testing.T) {
	forwarder := mustForwarder(t, "http://localhost:4000")
	for _, path := range []string{"/two words", "/path?second=query", "/path#fragment", "/line\nbreak"} {
		if _, err := forwarder.DestinationURL(path, ""); err == nil {
			t.Fatalf("DestinationURL(%q) unexpectedly succeeded", path)
		}
	}
}

func TestNewValidatesForwardTarget(t *testing.T) {
	for _, valid := range []string{"localhost:4000", "http://localhost:4000", "https://example.invalid/prefix"} {
		f, err := New(valid)
		if err != nil {
			t.Fatalf("New(%q): %v", valid, err)
		}
		if !strings.Contains(valid, "://") && f.String() != "http://"+valid {
			t.Fatalf("String() = %q", f.String())
		}
	}
	for _, invalid := range []string{
		"", "ftp://example.invalid", "http:///missing", "http://user@example.invalid",
		"http://example.invalid?query=1", "http://example.invalid/#fragment", "http://example.invalid#",
		"http://example.invalid:", "http://example.invalid:99999", "http://example.invalid/a b",
	} {
		if _, err := New(invalid); err == nil {
			t.Fatalf("New(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestForwarderReturnsRedirectWithoutFollowing(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			redirected := false
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirected = true
			}))
			defer destination.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", destination.URL)
				w.WriteHeader(status)
			}))
			defer origin.Close()

			resp, err := mustForwarder(t, origin.URL).Forward(context.Background(), http.MethodPost, "/hook", "", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != status || redirected {
				t.Fatalf("status = %d, redirected = %v", resp.StatusCode, redirected)
			}
		})
	}
}

func TestForwarderPreservesRequestAndRemovesHopHeaders(t *testing.T) {
	var requestURI, ordinary, removed, host string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI = r.RequestURI
		ordinary = r.Header.Get("X-Ordinary")
		removed = r.Header.Get("X-Remove")
		host = r.Host
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	headers := http.Header{
		"connection":        {"keep-alive, X-Remove"},
		"keep-alive":        {"timeout=5"},
		"transfer-encoding": {"chunked"},
		"x-ReMoVe":          {"secret"},
		"x-ordinary":        {"kept"},
		"host":              {"untrusted.invalid"},
	}
	originalHeaders := headers.Clone()
	resp, err := mustForwarder(t, server.URL+"/prefix").Forward(
		context.Background(), http.MethodPost, "/hooks/a%2Fb", "x=1%2F2&literal=%23", nil, headers,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if requestURI != "/prefix/hooks/a%2Fb?x=1%2F2&literal=%23" {
		t.Fatalf("request URI = %q", requestURI)
	}
	if ordinary != "kept" || removed != "" {
		t.Fatalf("ordinary = %q, removed = %q", ordinary, removed)
	}
	if host != strings.TrimPrefix(server.URL, "http://") {
		t.Fatalf("host = %q, want target host", host)
	}
	if !reflect.DeepEqual(headers, originalHeaders) {
		t.Fatalf("caller headers mutated: got %#v, want %#v", headers, originalHeaders)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timed out" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestClassifyTransportErrorWalksWrappedNetworkErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want TransportErrorKind
	}{
		{
			name: "connection refused",
			err:  fmt.Errorf("request failed: %w", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}),
			want: TransportConnectionRefused,
		},
		{
			name: "timeout",
			err:  fmt.Errorf("request failed: %w", timeoutError{}),
			want: TransportTimeout,
		},
		{
			name: "DNS",
			err:  fmt.Errorf("request failed: %w", &net.DNSError{Name: "missing.invalid", Err: "no such host"}),
			want: TransportDNS,
		},
		{
			name: "TLS",
			err: fmt.Errorf("request failed: %w", x509.HostnameError{
				Certificate: &x509.Certificate{},
				Host:        "localhost",
			}),
			want: TransportTLS,
		},
		{
			name: "other",
			err:  errors.New("broken response body"),
			want: TransportOther,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyTransportError(test.err); got != test.want {
				t.Fatalf("ClassifyTransportError() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFailureRetainsOriginalError(t *testing.T) {
	original := fmt.Errorf("wrapped: %w", syscall.ECONNREFUSED)
	failure := Failure(original)
	if failure.Kind != TransportConnectionRefused {
		t.Fatalf("kind = %q, want %q", failure.Kind, TransportConnectionRefused)
	}
	if !errors.Is(failure, syscall.ECONNREFUSED) {
		t.Fatal("failure does not retain wrapped original error")
	}
}
