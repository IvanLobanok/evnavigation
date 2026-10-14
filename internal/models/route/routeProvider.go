package route

import (
	"context"
)

type RoutePoint struct {
	Lat float64
	Lon float64
}

type RouteSegment struct {
	From           RoutePoint
	To             RoutePoint
	DistanceMeters float64
}

type RouteProvider interface {
	BuildRoute(ctx context.Context, from, to RoutePoint) ([]RouteSegment, error)
}
