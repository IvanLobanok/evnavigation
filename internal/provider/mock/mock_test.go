package mock

import (
	"context"
	"math"
	"testing"

	"github.com/IvanLobanok/evnavigation/internal/models/route"
)

// провайдер обязан подходить под интерфейс, который ждёт routing.Service
var _ route.RouteProvider = (*MockProvider)(nil)

var (
	minskCenter = route.RoutePoint{Lat: 53.9006, Lon: 27.5590}
	dzerzhinsk  = route.RoutePoint{Lat: 53.6833, Lon: 27.1333}
	brest       = route.RoutePoint{Lat: 52.0976, Lon: 23.7341}
)

func build(t *testing.T, from, to route.RoutePoint) []route.RouteSegment {
	t.Helper()
	segs, err := (&MockProvider{}).BuildRoute(context.Background(), from, to)
	if err != nil {
		t.Fatalf("BuildRoute: %v", err)
	}
	return segs
}

func total(segs []route.RouteSegment) float64 {
	sum := 0.0
	for _, s := range segs {
		sum += s.DistanceMeters
	}
	return sum
}

// ---------------------------------------------------------------------------
// haversineDistanceMeters
// ---------------------------------------------------------------------------

func TestHaversineDistanceMeters(t *testing.T) {
	tests := []struct {
		name            string
		a, b            route.RoutePoint
		want, tolerance float64
	}{
		{"same point", minskCenter, minskCenter, 0, 0},
		{"one degree of latitude", route.RoutePoint{}, route.RoutePoint{Lat: 1}, 111195, 50},
		{"one degree of longitude at the equator", route.RoutePoint{}, route.RoutePoint{Lon: 1}, 111195, 50},
		{"longitude shrinks with latitude (cos 60° = 0.5)", route.RoutePoint{Lat: 60}, route.RoutePoint{Lat: 60, Lon: 1}, 55597, 50},
		{"Minsk - Dzerzhinsk", minskCenter, dzerzhinsk, 36955, 100},
		{"Minsk - Brest", minskCenter, brest, 325056, 500},
		{"antipodes: half of the equator", route.RoutePoint{}, route.RoutePoint{Lon: 180}, math.Pi * 6371000, 1},
		{"pole to pole", route.RoutePoint{Lat: 90}, route.RoutePoint{Lat: -90}, math.Pi * 6371000, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := haversineDistanceMeters(tc.a, tc.b)
			if math.IsNaN(got) || math.Abs(got-tc.want) > tc.tolerance {
				t.Errorf("got %.1f, want %.1f ± %.0f", got, tc.want, tc.tolerance)
			}
			if back := haversineDistanceMeters(tc.b, tc.a); math.Abs(back-got) > 1e-6 {
				t.Errorf("distance must be symmetric: %v vs %v", got, back)
			}
		})
	}
}

func TestInterpolate(t *testing.T) {
	from, to := route.RoutePoint{Lat: 10, Lon: 20}, route.RoutePoint{Lat: 20, Lon: 40}

	tests := []struct {
		t    float64
		want route.RoutePoint
	}{
		{0, from},
		{0.5, route.RoutePoint{Lat: 15, Lon: 30}},
		{0.25, route.RoutePoint{Lat: 12.5, Lon: 25}},
		{1, to},
	}
	for _, tc := range tests {
		got := interpolate(from, to, tc.t)
		if math.Abs(got.Lat-tc.want.Lat) > 1e-12 || math.Abs(got.Lon-tc.want.Lon) > 1e-12 {
			t.Errorf("t=%v: got %+v, want %+v", tc.t, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// BuildRoute
// ---------------------------------------------------------------------------

func TestBuildRoute_SegmentCount(t *testing.T) {
	// сегменты нарезаются по ~5 км, последний неполный округляется вверх
	tests := []struct {
		name     string
		from, to route.RoutePoint
		want     int
	}{
		{"1 km", route.RoutePoint{}, route.RoutePoint{Lat: 1.0 / 111.195}, 1},
		{"4.9 km", route.RoutePoint{}, route.RoutePoint{Lat: 4.9 / 111.195}, 1},
		{"5.1 km", route.RoutePoint{}, route.RoutePoint{Lat: 5.1 / 111.195}, 2},
		{"12 km", route.RoutePoint{}, route.RoutePoint{Lat: 12.0 / 111.195}, 3},
		{"20.1 km", route.RoutePoint{}, route.RoutePoint{Lat: 20.1 / 111.195}, 5},
		{"Minsk - Dzerzhinsk (~37 km)", minskCenter, dzerzhinsk, 8},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(build(t, tc.from, tc.to)); got != tc.want {
				t.Errorf("segments: got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestBuildRoute_SamePointGivesOneZeroLengthSegment(t *testing.T) {
	segs := build(t, minskCenter, minskCenter)

	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1", len(segs))
	}
	if segs[0].DistanceMeters != 0 || math.IsNaN(segs[0].DistanceMeters) {
		t.Errorf("distance: got %v, want 0", segs[0].DistanceMeters)
	}
	if segs[0].From != minskCenter || segs[0].To != minskCenter {
		t.Errorf("both ends must be the point itself: %+v", segs[0])
	}
}

func TestBuildRoute_IsContinuous(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to route.RoutePoint
	}{
		{"Minsk - Dzerzhinsk", minskCenter, dzerzhinsk},
		{"Minsk - Brest", minskCenter, brest},
		{"reverse direction", brest, minskCenter},
		{"southern and western hemispheres", route.RoutePoint{Lat: -33.9, Lon: -70.7}, route.RoutePoint{Lat: -34.6, Lon: -58.4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			segs := build(t, tc.from, tc.to)

			if segs[0].From != tc.from {
				t.Errorf("the route must start at the origin: %+v vs %+v", segs[0].From, tc.from)
			}
			last := segs[len(segs)-1].To
			if math.Abs(last.Lat-tc.to.Lat) > 1e-9 || math.Abs(last.Lon-tc.to.Lon) > 1e-9 {
				t.Errorf("the route must end at the destination: %+v vs %+v", last, tc.to)
			}
			for i := 0; i+1 < len(segs); i++ {
				if segs[i].To != segs[i+1].From {
					t.Fatalf("gap between segment %d and %d: %+v -> %+v", i, i+1, segs[i].To, segs[i+1].From)
				}
			}
		})
	}
}

func TestBuildRoute_SegmentsAreEvenAndAddUp(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to route.RoutePoint
	}{
		{"Minsk - Dzerzhinsk", minskCenter, dzerzhinsk},
		{"Minsk - Brest", minskCenter, brest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			segs := build(t, tc.from, tc.to)
			direct := haversineDistanceMeters(tc.from, tc.to)

			// маршрут — ломаная вдоль прямой в координатах, а не по дуге большого круга:
			// сумма чуть больше прямого расстояния, но для сотен километров расхождение мизерное
			if sum := total(segs); math.Abs(sum-direct)/direct > 0.005 {
				t.Errorf("sum of segments %.0f m differs from the direct distance %.0f m by more than 0.5%%", sum, direct)
			}

			avg := total(segs) / float64(len(segs))
			for i, s := range segs {
				if s.DistanceMeters <= 0 || s.DistanceMeters > segmentLengthMeters*1.01 {
					t.Errorf("segment %d: length %.0f m is outside (0, %v]", i, s.DistanceMeters, segmentLengthMeters*1.01)
				}
				// длины чуть «плывут»: интерполяция идёт по градусам, а длина градуса долготы зависит от широты
				if math.Abs(s.DistanceMeters-avg)/avg > 0.03 {
					t.Errorf("segment %d: %.0f m is not even (average %.0f m)", i, s.DistanceMeters, avg)
				}
			}
		})
	}
}

func TestBuildRoute_BothDirectionsAreEquivalent(t *testing.T) {
	forward := build(t, minskCenter, brest)
	backward := build(t, brest, minskCenter)

	if len(forward) != len(backward) {
		t.Fatalf("segment counts differ: %d vs %d", len(forward), len(backward))
	}
	if math.Abs(total(forward)-total(backward)) > 1 {
		t.Errorf("distances differ: %.1f vs %.1f", total(forward), total(backward))
	}
}

func TestBuildRoute_IsDeterministic(t *testing.T) {
	a := build(t, minskCenter, brest)
	b := build(t, minskCenter, brest)

	if len(a) != len(b) {
		t.Fatal("different lengths")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("segment %d differs between runs", i)
		}
	}
}

func TestBuildRoute_NeverFails(t *testing.T) {
	// мок не ходит в сеть, поэтому ошибок быть не должно, даже при отменённом контексте
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	segs, err := (&MockProvider{}).BuildRoute(ctx, minskCenter, dzerzhinsk)
	if err != nil || len(segs) == 0 {
		t.Errorf("got %d segments, err=%v", len(segs), err)
	}
}
