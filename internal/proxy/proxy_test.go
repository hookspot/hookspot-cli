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
