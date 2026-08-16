package cmd

import (
	"bytes"
	"reflect"
	"testing"

	"hookspot/internal/api"
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
		"Events ────────────────────────────────────────\n" +
		"\n" +
		"Waiting for events...\n"
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
		"Events ────────────────────────────────────────\n" +
		"\n" +
		"Waiting for events...\n"
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
