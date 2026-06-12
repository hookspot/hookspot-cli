package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestForwarder_Forward_SendsRequestToTarget(t *testing.T) {
	var gotPath, gotBody, gotHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
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

	resp, err := f.Forward(context.Background(), "/webhooks", []byte(`{"id":"evt_123"}`), headers)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if gotPath != "/webhooks" {
		t.Fatalf("path = %q, want %q", gotPath, "/webhooks")
	}
	if gotBody != `{"id":"evt_123"}` {
		t.Fatalf("body = %q, want %q", gotBody, `{"id":"evt_123"}`)
	}
	if gotHeader != "evt_123" {
		t.Fatalf("header = %q, want %q", gotHeader, "evt_123")
	}
}
