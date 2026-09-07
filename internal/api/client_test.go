package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"hookspot/internal/endpoint"
)

func testEndpoint(t *testing.T, raw string) endpoint.Base {
	t.Helper()
	base, err := endpoint.Parse(raw, "dev")
	if err != nil {
		t.Fatal(err)
	}
	return base
}

func TestClientReturnsStructuredAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"CLI key expired"}}`)
	}))
	defer server.Close()

	_, err := New(testEndpoint(t, server.URL), "expired-key").Me(context.Background())
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want *api.Error", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", apiErr.StatusCode)
	}
	if apiErr.Method != http.MethodGet || apiErr.URL != server.URL+"/cli/me" {
		t.Fatalf("request context = %s %s", apiErr.Method, apiErr.URL)
	}
	if apiErr.Message != "CLI key expired" {
		t.Fatalf("message = %q, want CLI key expired", apiErr.Message)
	}
	if got, want := apiErr.Status(), "401 Unauthorized"; got != want {
		t.Fatalf("Status() = %q, want %q", got, want)
	}
}

func TestAPIErrorMessageFormats(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "message", body: `{"message":"bad request"}`, want: "bad request"},
		{name: "reason", body: `{"reason":"missing project"}`, want: "missing project"},
		{name: "string error", body: `{"error":"forbidden"}`, want: "forbidden"},
		{name: "nested reason", body: `{"error":{"reason":"expired"}}`, want: "expired"},
		{name: "plain text", body: "upstream unavailable", want: "upstream unavailable"},
		{name: "empty", body: "", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := apiErrorMessage([]byte(test.body)); got != test.want {
				t.Fatalf("apiErrorMessage() = %q, want %q", got, test.want)
			}
		})
	}
}

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

	client := New(testEndpoint(t, server.URL), "test-key")

	user, err := client.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if user.Email != "dev@example.com" {
		t.Fatalf("Email = %q, want %q", user.Email, "dev@example.com")
	}
}

func TestClientRejectsOversizedSuccessfulJSONResponse(t *testing.T) {
	padding := bytes.Repeat([]byte{'x'}, 1024*1024+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"uid":"usr_1","padding":"`))
		_, _ = w.Write(padding)
		_, _ = w.Write([]byte(`"}`))
	}))
	defer server.Close()

	_, err := New(testEndpoint(t, server.URL), "test-key").Me(context.Background())
	if err == nil || !strings.Contains(err.Error(), "response exceeds 1 MiB limit") {
		t.Fatalf("Me error = %v, want response limit error", err)
	}
	if strings.Contains(err.Error(), strings.Repeat("x", 64)) {
		t.Fatal("response limit error exposed response data")
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

	client := New(testEndpoint(t, server.URL), "test-key")

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
			UID:  "proj_1",
			Name: "Production",
			Slug: "production",
			Organization: Organization{
				UID:  "org_1",
				Name: "Acme Incorporated",
				Slug: "acme",
			},
			Sources: []Source{
				{
					UID:    "src_1",
					Name:   "Shopify",
					URL:    "https://events.example.com/src_1",
					Active: true,
				},
			},
		}
		if err := json.NewEncoder(w).Encode(project); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}))
	defer server.Close()

	client := New(testEndpoint(t, server.URL), "test-key")

	project, err := client.GetProject(context.Background(), "proj_1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if project.Name != "Production" {
		t.Fatalf("Name = %q, want %q", project.Name, "Production")
	}
	if project.Slug != "production" {
		t.Fatalf("Slug = %q, want %q", project.Slug, "production")
	}
	if project.Organization.Name != "Acme Incorporated" {
		t.Fatalf("Organization.Name = %q, want %q", project.Organization.Name, "Acme Incorporated")
	}
	if project.Organization.Slug != "acme" {
		t.Fatalf("Organization.Slug = %q, want %q", project.Organization.Slug, "acme")
	}
	if len(project.Sources) != 1 {
		t.Fatalf("len(Sources) = %d, want 1", len(project.Sources))
	}
	if project.Sources[0].UID != "src_1" {
		t.Fatalf("Sources[0].UID = %q, want %q", project.Sources[0].UID, "src_1")
	}
	if project.Sources[0].URL != "https://events.example.com/src_1" {
		t.Fatalf("Sources[0].URL = %q, want %q", project.Sources[0].URL, "https://events.example.com/src_1")
	}
}

func TestClient_ListProjectSources_ReturnsSourcesWithConnections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cli/projects/proj_1/sources" {
			t.Errorf("path = %q, want /cli/projects/proj_1/sources", r.URL.Path)
		}
		if got := r.Header.Get("X-CLI-KEY"); got != "test-key" {
			t.Errorf("X-CLI-KEY = %q, want %q", got, "test-key")
		}
		fmt.Fprint(w, `[
			{
				"active": true,
				"name": "shopify",
				"uid": "src_1",
				"url": "https://events.example.com/src_1",
				"connections": [{
					"active": true,
					"name": "local-shopify",
					"uid": "conn_1",
					"display_name": "shopify -> local-shopify",
					"destination": {"active": true, "path": "/webhooks/shopify", "uid": "dst_1"}
				}]
			}
		]`)
	}))
	defer server.Close()

	sources, err := New(testEndpoint(t, server.URL), "test-key").ListProjectSources(context.Background(), "proj_1")
	if err != nil {
		t.Fatalf("ListProjectSources: %v", err)
	}
	if len(sources) != 1 || sources[0].UID != "src_1" {
		t.Fatalf("sources = %+v, want source src_1", sources)
	}
	if len(sources[0].Connections) != 1 {
		t.Fatalf("connections = %+v, want one connection", sources[0].Connections)
	}
	connection := sources[0].Connections[0]
	if connection.Destination.Path != "/webhooks/shopify" {
		t.Fatalf("Destination.Path = %q, want /webhooks/shopify", connection.Destination.Path)
	}
	if connection.Name == nil || *connection.Name != "local-shopify" {
		t.Fatalf("Name = %v, want local-shopify", connection.Name)
	}
}

func TestClient_GetProjectBySlugs_ReturnsMatchingProject(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		switch r.URL.Path {
		case "/cli/projects":
			projects := []Project{
				{
					UID:  "proj_1",
					Name: "Storefront",
					Slug: "storefront",
					Organization: Organization{
						Name: "Acme",
						Slug: "acme",
					},
				},
				{
					UID:  "proj_2",
					Name: "Payments",
					Slug: "payments",
					Organization: Organization{
						Name: "Acme",
						Slug: "acme",
					},
				},
			}
			if err := json.NewEncoder(w).Encode(projects); err != nil {
				t.Fatalf("encode projects: %v", err)
			}
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	project, err := New(testEndpoint(t, server.URL), "test-key").GetProjectBySlugs(context.Background(), "acme", "payments")
	if err != nil {
		t.Fatalf("GetProjectBySlugs: %v", err)
	}
	if project.UID != "proj_2" {
		t.Fatalf("UID = %q, want %q", project.UID, "proj_2")
	}
	if project.Name != "Payments" || project.Organization.Name != "Acme" {
		t.Fatalf("project metadata = %+v", project)
	}
	if got := requestCount.Load(); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
}

func TestClient_GetProjectBySlugs_ReturnsErrorWhenNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode([]Project{}); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}))
	defer server.Close()

	_, err := New(testEndpoint(t, server.URL), "test-key").GetProjectBySlugs(context.Background(), "acme", "missing")
	if err == nil {
		t.Fatal("GetProjectBySlugs returned nil error")
	}
	if got, want := err.Error(), `project "acme/missing" not found`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestClientPreservesDeploymentPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/hookspot/cli/me" {
			t.Errorf("path = %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"uid":"usr_1"}`)
	}))
	defer server.Close()

	_, err := New(testEndpoint(t, server.URL+"/gateway/hookspot/"), "test-key").Me(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

func TestClientRejectsRedirectsWithoutLeakingCLIKey(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var redirected atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirected.Add(1)
				if r.Header.Get("X-CLI-KEY") != "" {
					t.Error("redirected CLI key")
				}
			}))
			defer destination.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, destination.URL, status)
			}))
			defer origin.Close()

			_, err := New(testEndpoint(t, origin.URL), "credential-sentinel").Me(context.Background())
			var apiErr *Error
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
				t.Fatalf("error = %T %v", err, err)
			}
			if got := redirected.Load(); got != 0 {
				t.Fatalf("redirect destination requests = %d", got)
			}
			if strings.Contains(err.Error(), "credential-sentinel") {
				t.Fatalf("error leaked key: %v", err)
			}
		})
	}
}

func TestClientRejectsInvalidIdentifiersBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	client := New(testEndpoint(t, server.URL), "test-key")

	if _, err := client.GetProject(context.Background(), "../other"); err == nil {
		t.Fatal("GetProject accepted an unsafe UID")
	}
	if _, err := client.ListProjectSources(context.Background(), "project%2fother"); err == nil {
		t.Fatal("ListProjectSources accepted an unsafe UID")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("requests = %d, want 0", got)
	}
}

func TestZeroEndpointDoesNotIssueRequest(t *testing.T) {
	_, err := New(endpoint.Base{}, "test-key").Me(context.Background())
	if err == nil {
		t.Fatal("Me succeeded with zero endpoint")
	}
}
