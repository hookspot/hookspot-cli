package session

import (
	"testing"

	"hookspot/internal/api"
	"hookspot/internal/ws"
)

func TestRouteFor(t *testing.T) {
	sources := []api.Source{
		{UID: "src_stripe", Routes: []api.Route{
			{UID: "rte_orders", Destination: api.Destination{Path: "/hooks"}},
			{UID: "rte_billing", Destination: api.Destination{Path: "/hooks"}},
			{UID: "rte_refunds", Destination: api.Destination{Path: "/refunds"}},
		}},
		{UID: "src_github", Routes: []api.Route{
			{UID: "rte_github", Destination: api.Destination{Path: "/github"}},
		}},
	}
	tests := []struct {
		name     string
		delivery ws.Delivery
		want     string
	}{
		{"route uid", ws.Delivery{SourceUID: "src_stripe", RouteUID: "rte_billing", Path: "/hooks"}, "rte_billing"},
		{"unknown route uid", ws.Delivery{SourceUID: "src_stripe", RouteUID: "rte_deleted", Path: "/refunds"}, ""},
		{"route uid of another source", ws.Delivery{SourceUID: "src_stripe", RouteUID: "rte_github", Path: "/github"}, ""},
		{"unknown source", ws.Delivery{SourceUID: "src_other", RouteUID: "rte_orders", Path: "/hooks"}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			route, ok := routeFor(sources, test.delivery)
			if route.UID != test.want || ok != (test.want != "") {
				t.Fatalf("routeFor() = %q, %v; want %q", route.UID, ok, test.want)
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
		{"name", api.Route{Name: &name, DisplayName: "shopify -> fallback", Destination: destination}, "orders"},
		{"no name", api.Route{DisplayName: "shopify -> /webhooks/shopify", Destination: destination}, "/webhooks/shopify"},
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
