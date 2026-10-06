package session

import (
	"hookspot/internal/api"
	"hookspot/internal/ws"
)

func routeFor(sources []api.Source, delivery ws.Delivery) (api.Route, bool) {
	for _, source := range sources {
		if source.UID != delivery.SourceUID {
			continue
		}
		for _, route := range source.Routes {
			if route.UID == delivery.RouteUID {
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
