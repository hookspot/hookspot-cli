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
	base, err := endpoint.Parse(raw)
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

func TestClient_ListProjectSources_ReturnsSourcesWithRoutes(t *testing.T) {
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
				"routes": [{
					"active": true,
					"name": "local-shopify",
					"uid": "rte_1",
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
	if len(sources[0].Routes) != 1 {
		t.Fatalf("routes = %+v, want one route", sources[0].Routes)
	}
	route := sources[0].Routes[0]
	if route.Destination.Path != "/webhooks/shopify" {
		t.Fatalf("Destination.Path = %q, want /webhooks/shopify", route.Destination.Path)
	}
	if route.Name == nil || *route.Name != "local-shopify" {
		t.Fatalf("Name = %v, want local-shopify", route.Name)
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

func TestClient_StartLogin_SendsDeviceNameWithoutCLIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/cli/auth" {
			t.Errorf("path = %q, want /cli/auth", r.URL.Path)
		}
		if got := r.Header.Get("X-CLI-KEY"); got != "" {
			t.Errorf("X-CLI-KEY = %q, want empty", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		var body struct {
			DeviceName string `json:"device_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.DeviceName != "mbp" {
			t.Errorf("device_name = %q, want %q", body.DeviceName, "mbp")
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"browser_token":"browser-token","poll_token":"poll-token","code":"ABCD-1234","expires_in":600}`)
	}))
	defer server.Close()

	attempt, err := New(testEndpoint(t, server.URL), "").StartLogin(context.Background(), "mbp")
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if attempt.BrowserToken != "browser-token" {
		t.Fatalf("BrowserToken = %q, want %q", attempt.BrowserToken, "browser-token")
	}
	if attempt.PollToken != "poll-token" {
		t.Fatalf("PollToken = %q, want %q", attempt.PollToken, "poll-token")
	}
	if attempt.Code != "ABCD-1234" {
		t.Fatalf("Code = %q, want %q", attempt.Code, "ABCD-1234")
	}
	if attempt.ExpiresIn != 600 {
		t.Fatalf("ExpiresIn = %d, want 600", attempt.ExpiresIn)
	}
}

func TestClient_StartLogin_PreservesDeploymentPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/hookspot/cli/auth" {
			t.Errorf("path = %q, want /gateway/hookspot/cli/auth", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"browser_token":"browser-token","poll_token":"poll-token"}`)
	}))
	defer server.Close()

	_, err := New(testEndpoint(t, server.URL+"/gateway/hookspot/"), "").StartLogin(context.Background(), "mbp")
	if err != nil {
		t.Fatal(err)
	}
}

func TestClient_StartLogin_ReturnsStructuredAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"not found"}`)
	}))
	defer server.Close()

	_, err := New(testEndpoint(t, server.URL), "").StartLogin(context.Background(), "mbp")
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want *api.Error", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", apiErr.StatusCode)
	}
	if apiErr.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", apiErr.Method)
	}
	if apiErr.URL != server.URL+"/cli/auth" {
		t.Fatalf("URL = %q, want %q", apiErr.URL, server.URL+"/cli/auth")
	}
}

func TestClient_PollLogin_Pending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/cli/auth/poll" {
			t.Errorf("path = %q, want /cli/auth/poll", r.URL.Path)
		}
		if got := r.Header.Get("X-CLI-KEY"); got != "" {
			t.Errorf("X-CLI-KEY = %q, want empty", got)
		}
		var body struct {
			PollToken string `json:"poll_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.PollToken != "poll-token" {
			t.Errorf("poll_token = %q, want %q", body.PollToken, "poll-token")
		}
		fmt.Fprint(w, `{"status":"pending"}`)
	}))
	defer server.Close()

	result, err := New(testEndpoint(t, server.URL), "").PollLogin(context.Background(), "poll-token")
	if err != nil {
		t.Fatalf("PollLogin: %v", err)
	}
	if result.Status != "pending" {
		t.Fatalf("Status = %q, want %q", result.Status, "pending")
	}
	if result.Project != nil {
		t.Fatalf("Project = %+v, want nil", result.Project)
	}
}

func TestClient_PollLogin_ApprovedWithProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"status": "approved",
			"user": {"uid": "usr_1", "email": "dev@example.com", "cli_key": "cli-secret"},
			"project": {
				"uid": "proj_1",
				"name": "Payments",
				"organization": {"uid": "org_1", "name": "Acme"}
			}
		}`)
	}))
	defer server.Close()

	result, err := New(testEndpoint(t, server.URL), "").PollLogin(context.Background(), "poll-token")
	if err != nil {
		t.Fatalf("PollLogin: %v", err)
	}
	if result.Status != "approved" {
		t.Fatalf("Status = %q, want %q", result.Status, "approved")
	}
	if result.User.UID != "usr_1" || result.User.Email != "dev@example.com" {
		t.Fatalf("User = %+v", result.User)
	}
	if result.User.CLIKey != "cli-secret" {
		t.Fatalf("User.CLIKey = %q, want %q", result.User.CLIKey, "cli-secret")
	}
	if result.Project == nil {
		t.Fatal("Project = nil, want project")
	}
	if result.Project.UID != "proj_1" || result.Project.Name != "Payments" {
		t.Fatalf("Project = %+v", result.Project)
	}
	if result.Project.Organization.Name != "Acme" {
		t.Fatalf("Organization.Name = %q, want %q", result.Project.Organization.Name, "Acme")
	}
}

func TestClient_PollLogin_ApprovedWithoutProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"status": "approved",
			"user": {"uid": "usr_1", "email": "dev@example.com", "cli_key": "cli-secret"},
			"project": null
		}`)
	}))
	defer server.Close()

	result, err := New(testEndpoint(t, server.URL), "").PollLogin(context.Background(), "poll-token")
	if err != nil {
		t.Fatalf("PollLogin: %v", err)
	}
	if result.Status != "approved" {
		t.Fatalf("Status = %q, want %q", result.Status, "approved")
	}
	if result.User.CLIKey != "cli-secret" {
		t.Fatalf("User.CLIKey = %q, want %q", result.User.CLIKey, "cli-secret")
	}
	if result.Project != nil {
		t.Fatalf("Project = %+v, want nil", result.Project)
	}
}

func TestClient_PollLogin_ReturnsStructuredAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"status":"not_found"}`)
	}))
	defer server.Close()

	_, err := New(testEndpoint(t, server.URL), "").PollLogin(context.Background(), "poll-token")
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want *api.Error", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", apiErr.StatusCode)
	}
	if apiErr.Method != http.MethodPost || apiErr.URL != server.URL+"/cli/auth/poll" {
		t.Fatalf("request context = %s %s", apiErr.Method, apiErr.URL)
	}
}

func TestClient_PollLogin_RejectsOversizedSuccessfulJSONResponse(t *testing.T) {
	padding := bytes.Repeat([]byte{'x'}, 1024*1024+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"approved","user":{"email":"`))
		_, _ = w.Write(padding)
		_, _ = w.Write([]byte(`"}}`))
	}))
	defer server.Close()

	_, err := New(testEndpoint(t, server.URL), "").PollLogin(context.Background(), "poll-token")
	if err == nil || !strings.Contains(err.Error(), "response exceeds 1 MiB limit") {
		t.Fatalf("PollLogin error = %v, want response limit error", err)
	}
	if strings.Contains(err.Error(), strings.Repeat("x", 64)) {
		t.Fatal("response limit error exposed response data")
	}
}
