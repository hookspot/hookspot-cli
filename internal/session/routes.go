package session

import (
	"slices"

	"hookspot/internal/api"
	"hookspot/internal/ws"
)

// knownRoute reports whether delivery came through a route of a source this
// run listens to.
func knownRoute(sources []api.Source, delivery ws.Delivery) bool {
	return slices.ContainsFunc(sources, func(source api.Source) bool {
		return source.UID == delivery.SourceUID && slices.ContainsFunc(source.Routes, func(route api.Route) bool {
			return route.UID == delivery.RouteUID
		})
	})
}

// RouteLabel is the route's name, or its destination path when unnamed.
func RouteLabel(route api.Route) string {
	if route.Name != nil && *route.Name != "" {
		return *route.Name
	}
	return route.Destination.Path
}
