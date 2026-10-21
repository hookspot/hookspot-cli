package session

import (
	"testing"

	"hookspot/internal/api"
	"hookspot/internal/ws"
)

func TestKnownRoute(t *testing.T) {
	sources := []api.Source{
		{UID: "src_stripe", Routes: []api.Route{{UID: "rte_orders"}, {UID: "rte_billing"}}},
		{UID: "src_github", Routes: []api.Route{{UID: "rte_github"}}},
	}
	tests := []struct {
		name     string
		delivery ws.Delivery
		want     bool
	}{
		{"route uid", ws.Delivery{SourceUID: "src_stripe", RouteUID: "rte_billing"}, true},
		{"unknown route uid", ws.Delivery{SourceUID: "src_stripe", RouteUID: "rte_deleted"}, false},
		{"route uid of another source", ws.Delivery{SourceUID: "src_stripe", RouteUID: "rte_github"}, false},
		{"unknown source", ws.Delivery{SourceUID: "src_other", RouteUID: "rte_orders"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := knownRoute(sources, test.delivery); got != test.want {
				t.Fatalf("knownRoute() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRouteLabel(t *testing.T) {
	name := "orders"
	empty := ""
	destination := api.Destination{Path: "/webhooks/shopify"}
	tests := []struct {
		name  string
		route api.Route
		want  string
	}{
		{"name", api.Route{Name: &name, Destination: destination}, "orders"},
		{"no name", api.Route{Destination: destination}, "/webhooks/shopify"},
		{"empty name", api.Route{Name: &empty, Destination: destination}, "/webhooks/shopify"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := RouteLabel(test.route); got != test.want {
				t.Fatalf("RouteLabel() = %q, want %q", got, test.want)
			}
		})
	}
}
