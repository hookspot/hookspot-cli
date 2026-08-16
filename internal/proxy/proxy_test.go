package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

	f := New(server.URL)

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

	f := New(server.URL)

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

	resp, err := New(server.URL+"/local").Forward(
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

func TestForwardURL(t *testing.T) {
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
			if got := ForwardURL(tt.base, tt.path); got != tt.want {
				t.Fatalf("ForwardURL(%q, %q) = %q, want %q", tt.base, tt.path, got, tt.want)
			}
		})
	}
}
