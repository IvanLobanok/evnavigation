package mock

import (
	"context"
	"math"

	"github.com/IvanLobanok/evnavigation/internal/models/route"
)

const segmentLengthMeters = 5000

type MockProvider struct{}

func (mp *MockProvider) BuildRoute(ctx context.Context, from, to route.RoutePoint) ([]route.RouteSegment, error) {
	total := haversineDistanceMeters(from, to)

	n := int(math.Ceil(total / segmentLengthMeters))
	if n < 1 {
		n = 1 // если from == to, всё равно вернём один сегмент
	}

	segments := make([]route.RouteSegment, 0, n)
	for i := 0; i < n; i++ {
		a := interpolate(from, to, float64(i)/float64(n))   // начало сегмента
		b := interpolate(from, to, float64(i+1)/float64(n)) // конец сегмента
		segments = append(segments, route.RouteSegment{
			From:           a,
			To:             b,
			DistanceMeters: haversineDistanceMeters(a, b),
		})
	}
	return segments, nil
}

// interpolate возвращает точку на доле t (0..1) прямой между from и to.
// Для коротких расстояний MVP линейной интерполяции по координатам достаточно.
func interpolate(from, to route.RoutePoint, t float64) route.RoutePoint {
	return route.RoutePoint{
		Lat: from.Lat + (to.Lat-from.Lat)*t,
		Lon: from.Lon + (to.Lon-from.Lon)*t,
	}
}

func haversineDistanceMeters(a, b route.RoutePoint) float64 {
	const earthRadiusM = 6371000
	lat1, lon1 := a.Lat*math.Pi/180, a.Lon*math.Pi/180
	lat2, lon2 := b.Lat*math.Pi/180, b.Lon*math.Pi/180
	dLat := lat2 - lat1
	dLon := lon2 - lon1
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Sqrt(h))

}
