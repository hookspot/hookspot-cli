package session

import (
	"hookspot/internal/api"
	"hookspot/internal/ws"
)

// routeFor returns the route of the delivery's source that produced it: the
// one named by RouteUID, else the first whose destination path equals the
// delivery path, because older servers send no RouteUID. False means unmatched.
func routeFor(sources []api.Source, delivery ws.Delivery) (api.Route, bool) {
	for _, source := range sources {
		if source.UID != delivery.SourceUID {
			continue
		}
		if delivery.RouteUID != "" {
			for _, route := range source.Routes {
				if route.UID == delivery.RouteUID {
					return route, true
				}
			}
		}
		for _, route := range source.Routes {
			if route.Destination.Path == delivery.Path {
				return route, true
			}
		}
	}
	return api.Route{}, false
}

// RouteLabel is the route's name, or its destination path when unnamed.
func RouteLabel(route api.Route) string {
	if route.Name != nil && *route.Name != "" {
		return *route.Name
	}
	return route.Destination.Path
}
