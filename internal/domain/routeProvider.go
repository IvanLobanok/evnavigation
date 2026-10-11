package domain

import (
	"context"
)

type RoutePoint struct {
	Lat, Lon float64
}

type RouteSegment struct {
	From, To       RoutePoint
	DistanceMeters float64
}

type RouteProvider interface {
	BuildRoute(ctx context.Context, from, to RoutePoint) ([]RouteSegment, error)
}
