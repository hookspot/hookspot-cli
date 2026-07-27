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
		if r.URL.Path != "/cli/me" {
			t.Errorf("path = %q, want /cli/me", r.URL.Path)
		}
		if got := r.Header.Get("X-CLI-KEY"); got != "test-key" {
			t.Errorf("X-CLI-KEY = %q, want %q", got, "test-key")
		}
		if err := json.NewEncoder(w).Encode(User{UID: "usr_1", Email: "dev@example.com"}); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}))
	defer server.Close()

	client := New(server.URL, "test-key")

	user, err := client.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if user.Email != "dev@example.com" {
		t.Fatalf("Email = %q, want %q", user.Email, "dev@example.com")
	}
}

func TestClient_ListProjects_ReturnsProjects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cli/projects" {
			t.Errorf("path = %q, want /cli/projects", r.URL.Path)
		}
		projects := []Project{
			{UID: "proj_1", Name: "Production"},
			{UID: "proj_2", Name: "Staging"},
		}
		if err := json.NewEncoder(w).Encode(projects); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}))
	defer server.Close()

	client := New(server.URL, "test-key")

	projects, err := client.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("len(projects) = %d, want 2", len(projects))
	}
	if projects[0].UID != "proj_1" || projects[1].UID != "proj_2" {
		t.Fatalf("unexpected projects: %+v", projects)
	}
}

func TestClient_GetProject_ReturnsProjectWithOrganization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cli/projects/proj_1" {
			t.Errorf("path = %q, want /cli/projects/proj_1", r.URL.Path)
		}
		if got := r.Header.Get("X-CLI-KEY"); got != "test-key" {
			t.Errorf("X-CLI-KEY = %q, want %q", got, "test-key")
		}
		project := Project{
			UID:          "proj_1",
			Name:         "Production",
			Organization: Organization{UID: "org_1", Name: "Acme"},
		}
		if err := json.NewEncoder(w).Encode(project); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}))
	defer server.Close()

	client := New(server.URL, "test-key")

	project, err := client.GetProject(context.Background(), "proj_1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if project.Name != "Production" {
		t.Fatalf("Name = %q, want %q", project.Name, "Production")
	}
	if project.Organization.Name != "Acme" {
		t.Fatalf("Organization.Name = %q, want %q", project.Organization.Name, "Acme")
	}
}
