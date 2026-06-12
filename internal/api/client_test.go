package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_Me_ReturnsUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" {
			t.Errorf("path = %q, want /api/me", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-token")
		}
		if err := json.NewEncoder(w).Encode(User{ID: "usr_1", Email: "dev@example.com"}); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}))
	defer server.Close()

	client := New(server.URL, "test-token")

	user, err := client.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if user.Email != "dev@example.com" {
		t.Fatalf("Email = %q, want %q", user.Email, "dev@example.com")
	}
}
