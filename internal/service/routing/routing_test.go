package routing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

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
// Он потокобезопасен: при параллельной проверке кандидатов BuildRoute вызывается из нескольких горутин.
type fakeProvider struct {
	mu    sync.Mutex
	calls int
	err   error

	// detour умножает длину каждого сегмента (дорога длиннее прямой); 0 означает 1, то есть без изменений.
	detour float64
	// onCall вызывается в начале каждого BuildRoute: может ждать (с учётом ctx), вернуть ошибку или паниковать.
	onCall func(ctx context.Context, from, to route.RoutePoint) error
}

func (f *fakeProvider) BuildRoute(ctx context.Context, from, to route.RoutePoint) ([]route.RouteSegment, error) {
	f.mu.Lock()
	f.calls++
	err, detour, onCall := f.err, f.detour, f.onCall
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if onCall != nil {
		if err := onCall(ctx, from, to); err != nil {
			return nil, err
		}
	}
	if detour == 0 {
		detour = 1
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
			DistanceMeters: step * 1000 * detour,
		})
		done += step
	}
	return segs, nil
}

// fakeFinder ищет станции из заданного списка: по радиусу и разъёму, сортируя по расстоянию и обрезая по limit
// (как настоящий репозиторий, у которого фильтр и LIMIT выполняются в SQL).
type fakeFinder struct {
	stations []station.ChargingStation
	calls    int
	err      error

	gotConnectors [][]string // разъёмы каждого вызова
	gotLimits     []int      // limit каждого вызова
}

func (f *fakeFinder) GetAvailableNearbyStations(_ context.Context, lat, _, radiusMeters float64, connectors []string, limit int) ([]station.ChargingStation, error) {
	f.calls++
	f.gotConnectors = append(f.gotConnectors, connectors)
	f.gotLimits = append(f.gotLimits, limit)
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
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}

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

	// старт 50%, 2 кВт·ч на 10 км: на 170 км остаётся 16%, на 180 км - 14%
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

	// Результат не должен зависеть от того, проверяются кандидаты по одному или параллельно.
	for _, parallelism := range []int{0, 1, 3} {
		for _, tc := range tests {
			t.Run(fmt.Sprintf("parallelism=%d/%s", parallelism, tc.name), func(t *testing.T) {
				runPlanRouteCase(t, parallelism, tc.cfg, tc.stations, tc.startPct, tc.finishKm, tc.wantErr, tc.wantStopIDs, tc.wantLegs)
			})
		}
	}
}

func runPlanRouteCase(t *testing.T, parallelism int, mutate func(*Config), stations []station.ChargingStation, startPct, finishKm float64, wantErr error, wantStopIDs []int64, wantLegs int) {
	t.Helper()
	cfg := testConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	cfg.CandidateParallelism = parallelism
	svc, _, _ := newTestService(t, cfg, stations...)

	plan, err := svc.PlanRoute(context.Background(), pt(0), pt(finishKm), testEV, startPct)

	if !errors.Is(err, wantErr) {
		t.Fatalf("error: got %v, want %v", err, wantErr)
	}
	if plan == nil {
		t.Fatal("plan must not be nil (partial plan is returned together with route errors)")
	}
	if len(plan.Legs) != wantLegs {
		t.Errorf("legs: got %d, want %d", len(plan.Legs), wantLegs)
	}

	var gotIDs []int64
	for _, s := range plan.Stops {
		gotIDs = append(gotIDs, s.Station.ID)
	}
	if len(gotIDs) != len(wantStopIDs) {
		t.Fatalf("stops: got %v, want %v", gotIDs, wantStopIDs)
	}
	for i := range gotIDs {
		if gotIDs[i] != wantStopIDs[i] {
			t.Errorf("stop %d: got station %d, want %d", i, gotIDs[i], wantStopIDs[i])
		}
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

	// первый лег заканчивается на станции, второй - на финише
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
	svc, prov, _ := newTestService(t, cfg, stn(1, 250))
	prov.detour = 1.2 // по прямой станция достижима (250 км < 275 км), но дорога на 20% длиннее, и заряда не хватает

	_, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60)
	if !errors.Is(err, ErrStationUnreachable) {
		t.Fatalf("got %v, want ErrStationUnreachable", err)
	}
	// 1 вызов - общий анализ маршрута, 1 - проверка единственной станции.
	// Повторные находки той же станции при откате не должны порождать новых запросов.
	if prov.calls != 2 {
		t.Errorf("provider calls: got %d, want 2", prov.calls)
	}
}

func TestPlanRoute_HopelessStationDoesNotCostAProviderCall(t *testing.T) {
	cfg := testConfig()
	cfg.SearchRadiusMeters = 70000
	// станция на 290-м км: 60% заряда хватает на 275 км, а прямой путь до неё уже длиннее - провайдер спрашивать незачем
	svc, prov, _ := newTestService(t, cfg, stn(1, 290))

	_, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60)
	if !errors.Is(err, ErrStationUnreachable) {
		t.Fatalf("got %v, want ErrStationUnreachable (stations exist, but out of range)", err)
	}
	if prov.calls != 1 {
		t.Errorf("provider calls: got %d, want 1 (only the whole-route analysis)", prov.calls)
	}
}

func TestPlanRoute_PrefilterNeverRejectsAReachableStation(t *testing.T) {
	// станция у самой границы достижимости по прямой: 60% - 5% = 55% = 275 км; станция на 270-м км достижима
	cfg := testConfig()
	cfg.SearchRadiusMeters = 70000
	svc, _, _ := newTestService(t, cfg, stn(1, 270))

	plan, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60)
	if err != nil {
		t.Fatalf("a reachable station must be found, got %v", err)
	}
	if len(plan.Stops) == 0 || plan.Stops[0].Station.ID != 1 {
		t.Errorf("want a stop at station 1, got %+v", plan.Stops)
	}
}

func TestPlanRoute_AsksFinderForConnectorAndEnoughRows(t *testing.T) {
	cfg := testConfig()
	cfg.SearchRadiusMeters = 70000
	svc, _, f := newTestService(t, cfg, stn(1, 100), stn(2, 120), stn(3, 140))

	if _, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60); err != nil && !errors.Is(err, ErrStationUnreachable) {
		t.Fatal(err)
	}
	if f.calls == 0 {
		t.Fatal("station finder was not called")
	}
	for i, got := range f.gotConnectors {
		if len(got) != 1 || got[0] != testEV.ConnectorType {
			t.Errorf("call %d: connectors %v, want [%s]: фильтр по разъёму должен уйти в базу", i, got, testEV.ConnectorType)
		}
	}
	// limit = MaxStationCandidates + число уже опробованных станций: растёт вместе с откатом и не меньше MaxStationCandidates
	for i, got := range f.gotLimits {
		if got < cfg.MaxStationCandidates {
			t.Errorf("call %d: limit %d is below MaxStationCandidates %d", i, got, cfg.MaxStationCandidates)
		}
	}
	if f.gotLimits[0] != cfg.MaxStationCandidates {
		t.Errorf("first call: limit %d, want %d (nothing has been tried yet)", f.gotLimits[0], cfg.MaxStationCandidates)
	}
}

func TestPlanRoute_TriedStationsDoNotStarveNewCandidates(t *testing.T) {
	// Много близких станций, до которых не доехать. Ближайшие к точке отката уже опробованы, но из-за запаса в limit
	// (MaxStationCandidates + число опробованных) новые кандидаты всё равно доходят до проверки.
	cfg := testConfig()
	cfg.SearchRadiusMeters = 70000
	cfg.MaxStationCandidates = 2
	stations := []station.ChargingStation{stn(1, 285), stn(2, 286), stn(3, 287), stn(4, 288), stn(5, 225)}
	svc, _, _ := newTestService(t, cfg, stations...)

	plan, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60)
	if err != nil {
		t.Fatalf("station 5 is reachable and must be found despite the nearer unreachable ones: %v", err)
	}
	if plan.Stops[0].Station.ID != 5 {
		t.Errorf("got station %d, want 5", plan.Stops[0].Station.ID)
	}
}

// ---------------------------------------------------------------------------
// Параллельная проверка кандидатов
// ---------------------------------------------------------------------------

func parallelConfig(parallelism int) Config {
	cfg := testConfig()
	cfg.SearchRadiusMeters = 70000
	cfg.CandidateParallelism = parallelism
	return cfg
}

// slowFor задерживает построение маршрутов до станции на заданной широте.
func slowFor(slowKm float64, d time.Duration) func(context.Context, route.RoutePoint, route.RoutePoint) error {
	return func(ctx context.Context, _, to route.RoutePoint) error {
		if math.Abs(kmOf(to)-slowKm) > 1e-6 {
			return nil
		}
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func TestPlanRoute_ParallelPicksNearestReachableEvenIfLaterOneAnswersFirst(t *testing.T) {
	// Первая точка поиска - критическая, 230-й км; станции идут от ближайшей к ней: 1 (229 км), затем 2 (228 км).
	// Обе достижимы. Ответ о станции 1 приходит позже, чем о станции 2, но победить должна первая по порядку,
	// как и при последовательной проверке.
	svc, prov, _ := newTestService(t, parallelConfig(3), stn(1, 229), stn(2, 228))
	prov.onCall = slowFor(229, 80*time.Millisecond)

	plan, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Stops[0].Station.ID; got != 1 {
		t.Errorf("got station %d, want 1 (the nearer one wins regardless of response order)", got)
	}
}

func TestPlanRoute_ParallelSameChoiceAsSequential(t *testing.T) {
	stations := []station.ChargingStation{stn(1, 225), stn(2, 226), stn(3, 227), stn(4, 228)}
	var chosen []int64
	for _, par := range []int{1, 4} {
		svc, _, _ := newTestService(t, parallelConfig(par), stations...)
		plan, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60)
		if err != nil {
			t.Fatal(err)
		}
		chosen = append(chosen, plan.Stops[0].Station.ID)
	}
	if chosen[0] != chosen[1] {
		t.Errorf("sequential chose %d, parallel chose %d", chosen[0], chosen[1])
	}
}

func TestPlanRoute_ParallelChecksCandidatesConcurrently(t *testing.T) {
	// Все три кандидата должны оказаться «в полёте» одновременно: каждый вызов провайдера ждёт, пока начнут остальные.
	// При последовательной проверке такой барьер никогда бы не открылся.
	svc, prov, _ := newTestService(t, parallelConfig(3), stn(1, 229), stn(2, 228), stn(3, 227))

	var started sync.WaitGroup
	started.Add(3)
	allStarted := make(chan struct{})
	go func() { started.Wait(); close(allStarted) }()

	prov.onCall = func(_ context.Context, _, to route.RoutePoint) error {
		if kmOf(to) > 229.5 { // общий анализ маршрута и участок после станции проходят без ожидания
			return nil
		}
		started.Done()
		select {
		case <-allStarted:
			return nil
		case <-time.After(2 * time.Second):
			return errors.New("candidates were not checked concurrently")
		}
	}

	plan, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Stops[0].Station.ID != 1 {
		t.Errorf("got station %d, want 1", plan.Stops[0].Station.ID)
	}
}

func TestPlanRoute_ParallelismIsBounded(t *testing.T) {
	var mu sync.Mutex
	running, peak := 0, 0
	svc, prov, _ := newTestService(t, func() Config { c := parallelConfig(2); c.MaxStationCandidates = 5; return c }(),
		stn(1, 221), stn(2, 222), stn(3, 223), stn(4, 224), stn(5, 225))
	prov.onCall = func(ctx context.Context, _, to route.RoutePoint) error {
		if kmOf(to) > 226 { // общий анализ маршрута не считаем
			return nil
		}
		mu.Lock()
		running++
		peak = max(peak, running)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		running--
		mu.Unlock()
		return nil
	}

	if _, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60); err != nil {
		t.Fatal(err)
	}
	if peak > 2 {
		t.Errorf("up to %d checks ran at once, limit is 2", peak)
	}
}

func TestPlanRoute_ParallelProviderErrorIsReturned(t *testing.T) {
	// порядок кандидатов от критической точки (230-й км): станция 1 (229 км), 2 (228 км), 3 (227 км); все достижимы
	failAt := func(km float64) func(context.Context, route.RoutePoint, route.RoutePoint) error {
		return func(_ context.Context, _, to route.RoutePoint) error {
			if math.Abs(kmOf(to)-km) < 1e-6 {
				return errBoom
			}
			return nil
		}
	}

	t.Run("failure to the right of the winner does not matter", func(t *testing.T) {
		svc, prov, _ := newTestService(t, parallelConfig(3), stn(1, 229), stn(2, 228), stn(3, 227))
		prov.onCall = failAt(228) // станция 2; победитель - станция 1, как при последовательной проверке

		plan, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60)
		if err != nil {
			t.Fatalf("got %v", err)
		}
		if plan.Stops[0].Station.ID != 1 {
			t.Errorf("got station %d, want 1", plan.Stops[0].Station.ID)
		}
	})

	t.Run("failure to the left of the winner is returned", func(t *testing.T) {
		svc, prov, _ := newTestService(t, parallelConfig(3), stn(1, 229), stn(2, 228), stn(3, 227))
		prov.onCall = failAt(229) // станция 1 идёт первой: последовательная проверка тоже споткнулась бы на ней

		if _, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60); !errors.Is(err, errBoom) {
			t.Errorf("got %v, want errBoom", err)
		}
	})
}

func TestPlanRoute_ParallelPanicBecomesError(t *testing.T) {
	svc, prov, _ := newTestService(t, parallelConfig(3), stn(1, 229), stn(2, 228))
	prov.onCall = func(_ context.Context, _, to route.RoutePoint) error {
		if math.Abs(kmOf(to)-229) < 1e-6 {
			panic("provider bug")
		}
		return nil
	}

	_, err := svc.PlanRoute(context.Background(), pt(0), pt(400), testEV, 60)
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("a panic inside a worker must come back as an error (not crash the process), got %v", err)
	}
}

func TestPlanRoute_ParallelStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	svc, prov, _ := newTestService(t, parallelConfig(3), stn(1, 225), stn(2, 226), stn(3, 227))
	prov.onCall = func(ctx context.Context, _, to route.RoutePoint) error {
		if kmOf(to) > 228 { // общий анализ маршрута проходит сразу
			return nil
		}
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}

	done := make(chan error, 1)
	go func() { _, err := svc.PlanRoute(ctx, pt(0), pt(400), testEV, 60); done <- err }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("got %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PlanRoute did not return after the context was cancelled (leaked workers?)")
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
		{"target at 100", func(c *Config) { c.TargetChargePercent = 100 }, true},
		{"target above 100", func(c *Config) { c.TargetChargePercent = 120 }, true},
		{"target just below 100", func(c *Config) { c.TargetChargePercent = 99.9 }, false},
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
