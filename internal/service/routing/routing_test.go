package routing

import (
	"context"
	"errors"
	"math"
	"sort"
	"testing"

	"github.com/IvanLobanok/evnavigation/internal/models/evmodel"
	"github.com/IvanLobanok/evnavigation/internal/models/route"
	"github.com/IvanLobanok/evnavigation/internal/models/station"
)

// ---------------------------------------------------------------------------
// Тестовый "мир": все точки лежат на одном меридиане, позиция задаётся в км
// от начала координат. Это позволяет писать сценарии в километрах:
// "станция на 228-м км", "финиш на 400-м км".
// ---------------------------------------------------------------------------

const (
	kmPerDeg = 111.0 // условный масштаб: 1° широты = 111 км
	stepKm   = 10.0  // длина сегмента, который отдаёт fakeProvider
)

func pt(km float64) route.RoutePoint  { return route.RoutePoint{Lat: km / kmPerDeg} }
func kmOf(p route.RoutePoint) float64 { return p.Lat * kmPerDeg }

var errBoom = errors.New("boom")

// fakeProvider строит прямой маршрут по меридиану, нарезая его на сегменты по stepKm.
type fakeProvider struct {
	calls int
	err   error
}

func (f *fakeProvider) BuildRoute(_ context.Context, from, to route.RoutePoint) ([]route.RouteSegment, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	a, b := kmOf(from), kmOf(to)
	total := math.Abs(b - a)
	dir := 1.0
	if b < a {
		dir = -1
	}

	var segs []route.RouteSegment
	for done := 0.0; done < total-1e-9; {
		step := math.Min(stepKm, total-done)
		segs = append(segs, route.RouteSegment{
			From:           pt(a + dir*done),
			To:             pt(a + dir*(done+step)),
			DistanceMeters: step * 1000,
		})
		done += step
	}
	return segs, nil
}

// fakeFinder ищет станции из заданного списка: по радиусу и разъёму, сортируя по расстоянию.
type fakeFinder struct {
	stations []station.ChargingStation
	calls    int
	err      error
}

func (f *fakeFinder) GetAvailableNearbyStations(_ context.Context, lat, _, radiusMeters float64, connectors []string) ([]station.ChargingStation, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	center := lat * kmPerDeg

	type item struct {
		st   station.ChargingStation
		dist float64
	}
	var items []item
	for _, st := range f.stations {
		d := math.Abs(kmOf(route.RoutePoint{Lat: st.Latitude}) - center)
		if d*1000 > radiusMeters || !hasConnector(st, connectors) {
			continue
		}
		items = append(items, item{st, d})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].dist < items[j].dist })

	out := make([]station.ChargingStation, 0, len(items))
	for _, it := range items {
		out = append(out, it.st)
	}
	return out, nil
}

func hasConnector(st station.ChargingStation, want []string) bool {
	for _, w := range want {
		for _, have := range st.ConnectorTypes {
			if w == have {
				return true
			}
		}
	}
	return false
}

func stn(id int64, km float64, connectors ...string) station.ChargingStation {
	if len(connectors) == 0 {
		connectors = []string{"CCS2"}
	}
	return station.ChargingStation{
		ID: id, Name: "station", Latitude: km / kmPerDeg,
		ConnectorTypes: connectors, IsOperational: true,
	}
}

// testEV: 100 кВт·ч и 20 кВт·ч/100 км => 1% заряда = 1 кВт·ч = 5 км пути.
var testEV = evmodel.EVModel{
	BatteryCapacityKwh:     100,
	AvgConsumptionKwh100km: 20,
	ConnectorType:          "CCS2",
}

func testConfig() Config {
	return Config{
		CriticalChargePercent: 15,
		MinArrivalPercent:     5,
		TargetChargePercent:   80,
		SearchRadiusMeters:    5000,
		MaxStops:              5,
		MaxStationCandidates:  3,
		LookbackStepKm:        5,
		MaxLookbackKm:         30,
	}
}

func newTestService(t *testing.T, cfg Config, stations ...station.ChargingStation) (*RouteService, *fakeProvider, *fakeFinder) {
	t.Helper()
	p := &fakeProvider{}
	f := &fakeFinder{stations: stations}
	svc, err := NewService(p, f, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, p, f
}

func approx(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s: got %.4f, want %.4f (±%v)", name, got, want, tol)
	}
}

// ---------------------------------------------------------------------------
// analyzeRoute
// ---------------------------------------------------------------------------

func TestAnalyzeRoute(t *testing.T) {
	tests := []struct {
		name          string
		startPercent  float64
		finishKm      float64
		wantPoints    int
		wantEnd       float64 // остаток в конце, %
		wantReaches   bool
		wantFirstCrit int // индекс первой критической точки, -1 если нет
	}{
		{"enough charge, no critical points", 100, 100, 11, 80, true, -1},
		{"crosses critical threshold mid-route", 50, 200, 21, 10, false, 18},
		{"battery clamps at zero, never negative", 10, 100, 11, 0, false, 0},
		{"start already below threshold", 10, 10, 2, 8, false, 0},
		{"from equals to: only the start point", 80, 0, 1, 80, true, -1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := newTestService(t, testConfig())

			got, err := svc.analyzeRoute(context.Background(), pt(0), pt(tc.finishKm), testEV, tc.startPercent)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(got.Points) != tc.wantPoints {
				t.Fatalf("points: got %d, want %d", len(got.Points), tc.wantPoints)
			}
			approx(t, "start percent", got.Points[0].RemainingBatteryPercent, tc.startPercent, 1e-6)
			approx(t, "end percent", got.last().RemainingBatteryPercent, tc.wantEnd, 1e-6)
			approx(t, "total km", got.TotalDistanceKm, tc.finishKm, 1e-6)

			if got.ReachesDestination != tc.wantReaches {
				t.Errorf("ReachesDestination: got %v, want %v", got.ReachesDestination, tc.wantReaches)
			}

			if tc.wantFirstCrit < 0 {
				if len(got.CriticalPointIndexes) != 0 {
					t.Errorf("expected no critical points, got %v", got.CriticalPointIndexes)
				}
				return
			}
			if len(got.CriticalPointIndexes) != 1 || got.CriticalPointIndexes[0] != tc.wantFirstCrit {
				t.Errorf("critical indexes: got %v, want [%d]", got.CriticalPointIndexes, tc.wantFirstCrit)
			}
			for _, p := range got.Points {
				if p.RemainingBatteryKwh < 0 {
					t.Errorf("negative remaining energy: %v", p.RemainingBatteryKwh)
				}
			}
		})
	}
}

func TestAnalyzeRoute_CriticalFlagsAroundThreshold(t *testing.T) {
	svc, _, _ := newTestService(t, testConfig())

	// старт 50%, 2 кВт·ч на 10 км: на 170 км остаётся 16%, на 180 км — 14%
	got, err := svc.analyzeRoute(context.Background(), pt(0), pt(200), testEV, 50)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "point 17 percent", got.Points[17].RemainingBatteryPercent, 16, 1e-6)
	approx(t, "point 18 percent", got.Points[18].RemainingBatteryPercent, 14, 1e-6)
	if got.Points[17].IsCritical {
		t.Error("point 17 (16%) must not be critical")
	}
	if !got.Points[18].IsCritical {
		t.Error("point 18 (14%) must be critical")
	}
}

func TestAnalyzeRoute_InvalidInput(t *testing.T) {
	tests := []struct {
		name     string
		ev       evmodel.EVModel
		startPct float64
	}{
		{"zero battery capacity", evmodel.EVModel{BatteryCapacityKwh: 0, AvgConsumptionKwh100km: 20}, 50},
		{"negative percent", testEV, -1},
		{"percent above 100", testEV, 100.1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, prov, _ := newTestService(t, testConfig())

			got, err := svc.analyzeRoute(context.Background(), pt(0), pt(100), tc.ev, tc.startPct)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got != nil {
				t.Error("expected nil analysis on error")
			}
			if prov.calls != 0 {
				t.Errorf("provider must not be called on invalid input, calls=%d", prov.calls)
			}
		})
	}
}

func TestAnalyzeRoute_ProviderErrorIsWrapped(t *testing.T) {
	svc, prov, _ := newTestService(t, testConfig())
	prov.err = errBoom

	_, err := svc.analyzeRoute(context.Background(), pt(0), pt(100), testEV, 50)
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// PlanRoute
//
// База для большинства сценариев: старт 60% (= 300 км запаса), порог 15%.
// Критическая точка наступает на 230-м км (14% остатка).
// ---------------------------------------------------------------------------

func TestPlanRoute(t *testing.T) {
	wideRadius := func(c *Config) { c.SearchRadiusMeters = 70000 }

	tests := []struct {
		name        string
		cfg         func(*Config)
		stations    []station.ChargingStation
		startPct    float64
		finishKm    float64
		wantErr     error
		wantStopIDs []int64
		wantLegs    int
	}{
		{
			name: "enough charge: no stops", startPct: 100, finishKm: 300,
			wantLegs: 1,
		},
		{
			name:     "one stop, station within radius of critical point",
			stations: []station.ChargingStation{stn(1, 228)},
			startPct: 60, finishKm: 400,
			wantStopIDs: []int64{1}, wantLegs: 2,
		},
		{
			name:     "one stop found by looking back along the route",
			stations: []station.ChargingStation{stn(1, 215)}, // 15 км до критической точки
			startPct: 60, finishKm: 400,
			wantStopIDs: []int64{1}, wantLegs: 2,
		},
		{
			name:     "two stops on a long route",
			stations: []station.ChargingStation{stn(1, 228), stn(2, 540)},
			startPct: 60, finishKm: 800,
			wantStopIDs: []int64{1, 2}, wantLegs: 3,
		},
		{
			name: "skips unreachable candidate and takes the next one",
			cfg:  wideRadius,
			// 1: ближе к критической точке (60 км), но на 290-м км заряд уже ~2% < 5%
			// 2: дальше (65 км), но достижима
			stations: []station.ChargingStation{stn(1, 290), stn(2, 165)},
			startPct: 60, finishKm: 400,
			wantStopIDs: []int64{2}, wantLegs: 2,
		},
		{
			name: "no stations at all", startPct: 60, finishKm: 400,
			wantErr: ErrNoStationNearby,
		},
		{
			name:     "station with incompatible connector is ignored",
			stations: []station.ChargingStation{stn(1, 228, "CHAdeMO")},
			startPct: 60, finishKm: 400,
			wantErr: ErrNoStationNearby,
		},
		{
			name:     "station is farther back than the lookback limit",
			stations: []station.ChargingStation{stn(1, 190)}, // 40 км назад, лимит 30
			startPct: 60, finishKm: 400,
			wantErr: ErrNoStationNearby,
		},
		{
			name: "station found but cannot be reached",
			cfg:  wideRadius, stations: []station.ChargingStation{stn(1, 290)},
			startPct: 60, finishKm: 400,
			wantErr: ErrStationUnreachable,
		},
		{
			name:     "stop limit reached",
			cfg:      func(c *Config) { c.MaxStops = 1 },
			stations: []station.ChargingStation{stn(1, 228), stn(2, 540)},
			startPct: 60, finishKm: 1000,
			wantErr: ErrTooManyStops, wantStopIDs: []int64{1}, wantLegs: 1,
		},
		{
			// Регрессия: после зарядки до 20% следующая критическая точка всего в 30 км,
			// и откат доходит до той самой станции. Её нельзя выбирать повторно.
			name:     "never charges twice at the same station",
			cfg:      func(c *Config) { c.TargetChargePercent = 20 },
			stations: []station.ChargingStation{stn(1, 228)},
			startPct: 60, finishKm: 1000,
			wantErr: ErrNoStationNearby, wantStopIDs: []int64{1}, wantLegs: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			if tc.cfg != nil {
				tc.cfg(&cfg)
			}
			svc, _, _ := newTestService(t, cfg, tc.stations...)

			plan, err := svc.PlanRoute(context.Background(), pt(0), pt(tc.finishKm), testEV, tc.startPct)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tc.wantErr)
			}
			if plan == nil {
				t.Fatal("plan must not be nil (partial plan is returned together with route errors)")
			}
			if len(plan.Legs) != tc.wantLegs {
				t.Errorf("legs: got %d, want %d", len(plan.Legs), tc.wantLegs)
			}

			var gotIDs []int64
			for _, s := range plan.Stops {
				gotIDs = append(gotIDs, s.Station.ID)
			}
			if len(gotIDs) != len(tc.wantStopIDs) {
				t.Fatalf("stops: got %v, want %v", gotIDs, tc.wantStopIDs)
			}
			for i := range gotIDs {
				if gotIDs[i] != tc.wantStopIDs[i] {
					t.Errorf("stop %d: got station %d, want %d", i, gotIDs[i], tc.wantStopIDs[i])
				}
			}
		})
	}
}

func TestPlanRoute_OneStopDetails(t *testing.T) {
	svc, _, _ := newTestService(t, testConfig(), stn(1, 228))

	plan, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Stops) != 1 || len(plan.Legs) != 2 {
		t.Fatalf("want 1 stop and 2 legs, got %d and %d", len(plan.Stops), len(plan.Legs))
	}

	stop := plan.Stops[0]
	// 228 км * 0.2 = 45.6 кВт·ч => 60 - 45.6 = 14.4%
	approx(t, "arrival percent", stop.ArrivalPercent, 14.4, 1e-3)
	approx(t, "depart percent", stop.DepartPercent, 80, 1e-9)

	// первый лег заканчивается на станции, второй — на финише
	approx(t, "leg 0 ends at station (km)", kmOf(plan.Legs[0].last().Point), 228, 1e-3)
	approx(t, "leg 1 ends at finish (km)", kmOf(plan.Legs[1].last().Point), 400, 1e-3)

	// от станции до финиша 172 км = 34.4 кВт·ч => 80 - 34.4 = 45.6%
	approx(t, "arrival at finish", plan.Legs[1].last().RemainingBatteryPercent, 45.6, 1e-3)
	approx(t, "total distance", plan.TotalDistanceKm, 400, 1e-3)

	// второй лег начинается с заряда TargetChargePercent
	approx(t, "leg 1 start percent", plan.Legs[1].Points[0].RemainingBatteryPercent, 80, 1e-9)
}

func TestPlanRoute_UnreachableStationIsCheckedOnlyOnce(t *testing.T) {
	cfg := testConfig()
	cfg.SearchRadiusMeters = 70000 // круги поиска в разных точках отката сильно перекрываются
	svc, prov, _ := newTestService(t, cfg, stn(1, 290))

	_, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60)
	if !errors.Is(err, ErrStationUnreachable) {
		t.Fatalf("got %v, want ErrStationUnreachable", err)
	}
	// 1 вызов — общий анализ маршрута, 1 — проверка единственной станции.
	// Повторные находки той же станции при откате не должны порождать новых запросов.
	if prov.calls != 2 {
		t.Errorf("provider calls: got %d, want 2", prov.calls)
	}
}

func TestPlanRoute_NoStopMeansSingleProviderCall(t *testing.T) {
	svc, prov, f := newTestService(t, testConfig(), stn(1, 228))

	if _, err := svc.PlanRoute(context.Background(), pt(0), pt(300), testEV, 100); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 1 {
		t.Errorf("provider calls: got %d, want 1", prov.calls)
	}
	if f.calls != 0 {
		t.Errorf("station finder must not be called when charge is enough, calls=%d", f.calls)
	}
}

func TestPlanRoute_Errors(t *testing.T) {
	t.Run("provider error: no plan", func(t *testing.T) {
		svc, prov, _ := newTestService(t, testConfig())
		prov.err = errBoom

		plan, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60)
		if !errors.Is(err, errBoom) {
			t.Fatalf("got %v, want errBoom", err)
		}
		if plan != nil {
			t.Error("plan must be nil when the very first analysis fails")
		}
	})

	t.Run("invalid input: no plan", func(t *testing.T) {
		svc, _, _ := newTestService(t, testConfig())

		plan, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 150)
		if err == nil {
			t.Fatal("expected error")
		}
		if plan != nil {
			t.Error("plan must be nil")
		}
	})

	t.Run("station finder error is propagated", func(t *testing.T) {
		svc, _, f := newTestService(t, testConfig())
		f.err = errBoom

		_, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60)
		if !errors.Is(err, errBoom) {
			t.Fatalf("got %v, want wrapped errBoom", err)
		}
	})
}

// ---------------------------------------------------------------------------
// NewService: валидация конфига
// ---------------------------------------------------------------------------

func TestNewService_ConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"valid default config", func(*Config) {}, false},
		{"target below critical", func(c *Config) { c.TargetChargePercent = 10 }, true},
		{"target equal to critical", func(c *Config) { c.TargetChargePercent = c.CriticalChargePercent }, true},
		{"min arrival above critical", func(c *Config) { c.MinArrivalPercent = 20 }, true},
		{"min arrival equal to critical", func(c *Config) { c.MinArrivalPercent = c.CriticalChargePercent }, true},
		{"zero max stops", func(c *Config) { c.MaxStops = 0 }, true},
		{"negative max stops", func(c *Config) { c.MaxStops = -1 }, true},
		{"zero station candidates", func(c *Config) { c.MaxStationCandidates = 0 }, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			tc.mutate(&cfg)

			svc, err := NewService(&fakeProvider{}, &fakeFinder{}, cfg)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if svc != nil {
					t.Error("service must be nil when config is invalid")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if svc == nil {
				t.Fatal("service must not be nil")
			}
		})
	}
}
