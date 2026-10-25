package handler

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/IvanLobanok/evnavigation/internal/models/station"
)

type fakeStationFinder struct {
	stations []station.ChargingStation
	err      error
	calls    int

	gotLat, gotLon, gotRadius float64
	gotConnectors             []string
}

func (f *fakeStationFinder) GetAvailableNearbyStations(_ context.Context, lat, lon, radiusMeters float64, connectors []string) ([]station.ChargingStation, error) {
	f.calls++
	f.gotLat, f.gotLon, f.gotRadius, f.gotConnectors = lat, lon, radiusMeters, connectors
	return f.stations, f.err
}

func getNearby(h *StationHandler, query string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.Nearby(rec, httptest.NewRequest(http.MethodGet, "/stations/nearby?"+query, nil))
	return rec
}

func decodeNearby(t *testing.T, rec *httptest.ResponseRecorder) nearbyResponse {
	t.Helper()
	var resp nearbyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return resp
}

func manyStations(n int) []station.ChargingStation {
	out := make([]station.ChargingStation, n)
	for i := range out {
		out[i] = station.ChargingStation{ID: int64(i + 1), Name: "S", Latitude: 53.9, Longitude: 27.5, ConnectorTypes: []string{"CCS Combo 2"}}
	}
	return out
}

// ---------------------------------------------------------------------------
// Успешные запросы
// ---------------------------------------------------------------------------

func TestNearby_Success(t *testing.T) {
	finder := &fakeStationFinder{stations: []station.ChargingStation{
		// ~1112 м к северу от точки запроса (0.01° широты)
		{ID: 7, Name: "Fast Charge", Latitude: 53.91, Longitude: 27.5, PowerKW: 120, ConnectorTypes: []string{"CCS Combo 2", "GB/T DC"}},
		{ID: 8, Name: "Slow Charge", Latitude: 53.9, Longitude: 27.5, PowerKW: 22, ConnectorTypes: []string{"Type 2"}},
	}}
	rec := getNearby(NewStationHandler(finder, nil), "lat=53.9&lon=27.5&radius_km=3")

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d; body: %s", rec.Code, rec.Body)
	}
	if finder.gotLat != 53.9 || finder.gotLon != 27.5 || finder.gotRadius != 3000 {
		t.Errorf("finder got lat=%v lon=%v radius=%v, want 53.9 27.5 3000 (радиус переведён в метры)", finder.gotLat, finder.gotLon, finder.gotRadius)
	}

	resp := decodeNearby(t, rec)
	if resp.RadiusKm != 3 || resp.Truncated || len(resp.Stations) != 2 {
		t.Fatalf("unexpected response: %+v", resp)
	}

	first := resp.Stations[0]
	if first.ID != 7 || first.Name != "Fast Charge" || first.PowerKW != 120 || first.Lat != 53.91 || first.Lon != 27.5 {
		t.Errorf("station fields lost: %+v", first)
	}
	if len(first.ConnectorTypes) != 2 || first.ConnectorTypes[1] != "GB/T DC" {
		t.Errorf("connector types: %v", first.ConnectorTypes)
	}
	if math.Abs(float64(first.DistanceM)-1112) > 3 {
		t.Errorf("distance_m: got %d, want ~1112", first.DistanceM)
	}
	if resp.Stations[1].DistanceM != 0 {
		t.Errorf("station at the query point must be 0 m away, got %d", resp.Stations[1].DistanceM)
	}
	// порядок сохраняется как пришёл от сервиса (он уже отсортирован по расстоянию)
	if resp.Stations[0].ID != 7 || resp.Stations[1].ID != 8 {
		t.Error("order must be preserved")
	}
}

func TestNearby_JSONShape(t *testing.T) {
	finder := &fakeStationFinder{stations: manyStations(1)}
	body := getNearby(NewStationHandler(finder, nil), "lat=53.9&lon=27.5").Body.String()

	// встроенная структура должна «разворачиваться» в плоский объект, а не вкладываться
	for _, want := range []string{`"id":1`, `"lat":53.9`, `"power_kw":0`, `"connector_types":["CCS Combo 2"]`, `"distance_m":0`, `"truncated":false`} {
		if !strings.Contains(body, want) {
			t.Errorf("response must contain %s, got %s", want, body)
		}
	}
	if strings.Contains(body, "stationDTO") {
		t.Errorf("embedded struct leaked into JSON: %s", body)
	}
}

func TestNearby_Defaults(t *testing.T) {
	finder := &fakeStationFinder{}
	rec := getNearby(NewStationHandler(finder, nil), "lat=53.9&lon=27.5")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if finder.gotRadius != 5000 {
		t.Errorf("default radius: got %v m, want 5000", finder.gotRadius)
	}
	if len(finder.gotConnectors) != 0 {
		t.Errorf("no connector filter expected, got %v", finder.gotConnectors)
	}
	if decodeNearby(t, rec).RadiusKm != 5 {
		t.Error("response must report the applied radius")
	}
}

func TestNearby_BoundaryValuesAreAccepted(t *testing.T) {
	for _, q := range []string{
		"lat=90&lon=180", "lat=-90&lon=-180", "lat=0&lon=0",
		"lat=53.9&lon=27.5&radius_km=50", "lat=53.9&lon=27.5&radius_km=0.001",
		"lat=53.9&lon=27.5&limit=1", "lat=53.9&lon=27.5&limit=100",
	} {
		if rec := getNearby(NewStationHandler(&fakeStationFinder{}, nil), q); rec.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200; body: %s", q, rec.Code, rec.Body)
		}
	}
}

func TestNearby_EmptyResultIsArrayNotNull(t *testing.T) {
	rec := getNearby(NewStationHandler(&fakeStationFinder{}, nil), "lat=53.9&lon=27.5")

	if !strings.Contains(rec.Body.String(), `"stations":[]`) {
		t.Errorf("empty result must be []: %s", rec.Body)
	}
}

func TestNearby_NilConnectorTypesAreEmptyArray(t *testing.T) {
	finder := &fakeStationFinder{stations: []station.ChargingStation{{ID: 1}}}
	rec := getNearby(NewStationHandler(finder, nil), "lat=53.9&lon=27.5")

	if !strings.Contains(rec.Body.String(), `"connector_types":[]`) {
		t.Errorf("nil connector list must serialize as []: %s", rec.Body)
	}
}

// ---------------------------------------------------------------------------
// Фильтр по разъёму и ограничение количества
// ---------------------------------------------------------------------------

func TestNearby_ConnectorFilter(t *testing.T) {
	finder := &fakeStationFinder{}
	q := url.Values{"lat": {"53.9"}, "lon": {"27.5"}, "connector": {"CCS Combo 2", "", "GB/T DC"}}.Encode()
	getNearby(NewStationHandler(finder, nil), q)

	// повторяющийся параметр собирается в список, пустые значения отбрасываются
	if len(finder.gotConnectors) != 2 || finder.gotConnectors[0] != "CCS Combo 2" || finder.gotConnectors[1] != "GB/T DC" {
		t.Errorf("connectors: got %q", finder.gotConnectors)
	}
}

func TestNearby_Limit(t *testing.T) {
	tests := []struct {
		name          string
		found         int
		query         string
		wantCount     int
		wantTruncated bool
	}{
		{"default limit cuts 30 down to 20", 30, "", 20, true},
		{"explicit limit", 30, "&limit=5", 5, true},
		{"exactly at the limit is not truncated", 5, "&limit=5", 5, false},
		{"fewer than the limit", 3, "&limit=5", 3, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			finder := &fakeStationFinder{stations: manyStations(tc.found)}
			resp := decodeNearby(t, getNearby(NewStationHandler(finder, nil), "lat=53.9&lon=27.5"+tc.query))

			if len(resp.Stations) != tc.wantCount || resp.Truncated != tc.wantTruncated {
				t.Errorf("got %d stations truncated=%v, want %d truncated=%v", len(resp.Stations), resp.Truncated, tc.wantCount, tc.wantTruncated)
			}
			// обрезается хвост: остаются ближайшие
			if len(resp.Stations) > 0 && resp.Stations[0].ID != 1 {
				t.Error("the nearest stations must be kept")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Некорректные параметры
// ---------------------------------------------------------------------------

func TestNearby_Validation(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantField string
	}{
		{"no params", "", "lat"},
		{"missing lat", "lon=27.5", "lat"},
		{"missing lon", "lat=53.9", "lon"},
		{"empty lat", "lat=&lon=27.5", "lat"},
		{"lat not a number", "lat=abc&lon=27.5", "lat"},
		{"lat NaN", "lat=NaN&lon=27.5", "lat"},
		{"lat Inf", "lat=Inf&lon=27.5", "lat"},
		{"lat minus Inf", "lat=-Inf&lon=27.5", "lat"},
		{"lat too high", "lat=90.1&lon=27.5", "lat"},
		{"lat too low", "lat=-91&lon=27.5", "lat"},
		{"lon too high", "lat=53.9&lon=180.5", "lon"},
		{"lon too low", "lat=53.9&lon=-181", "lon"},
		{"lon NaN", "lat=53.9&lon=nan", "lon"},
		{"radius zero", "lat=53.9&lon=27.5&radius_km=0", "radius_km"},
		{"radius negative", "lat=53.9&lon=27.5&radius_km=-1", "radius_km"},
		{"radius above max", "lat=53.9&lon=27.5&radius_km=50.1", "radius_km"},
		{"radius not a number", "lat=53.9&lon=27.5&radius_km=far", "radius_km"},
		{"radius Inf", "lat=53.9&lon=27.5&radius_km=Inf", "radius_km"},
		{"radius NaN", "lat=53.9&lon=27.5&radius_km=NaN", "radius_km"},
		{"limit zero", "lat=53.9&lon=27.5&limit=0", "limit"},
		{"limit above max", "lat=53.9&lon=27.5&limit=101", "limit"},
		{"limit not an integer", "lat=53.9&lon=27.5&limit=2.5", "limit"},
		{"limit not a number", "lat=53.9&lon=27.5&limit=many", "limit"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			finder := &fakeStationFinder{}
			rec := getNearby(NewStationHandler(finder, nil), tc.query)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want 400; body: %s", rec.Code, rec.Body)
			}
			e := decodeErr(t, rec)
			if e.Error.Code != "validation_error" {
				t.Errorf("code: got %q", e.Error.Code)
			}
			if _, ok := e.Error.Details[tc.wantField]; !ok {
				t.Errorf("details must mention %q: %v", tc.wantField, e.Error.Details)
			}
			if finder.calls != 0 {
				t.Error("finder must not be called for invalid input")
			}
		})
	}
}

func TestNearby_ReportsAllProblemsAtOnce(t *testing.T) {
	d := decodeErr(t, getNearby(NewStationHandler(&fakeStationFinder{}, nil), "radius_km=-1&limit=0")).Error.Details

	for _, f := range []string{"lat", "lon", "radius_km", "limit"} {
		if _, ok := d[f]; !ok {
			t.Errorf("missing problem for %q: %v", f, d)
		}
	}
}

func TestNearby_ServiceErrorDoesNotLeak(t *testing.T) {
	finder := &fakeStationFinder{err: errors.New("pq: password authentication failed")}
	rec := getNearby(NewStationHandler(finder, nil), "lat=53.9&lon=27.5")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "password") {
		t.Errorf("internal error leaked: %s", rec.Body)
	}
}

// ---------------------------------------------------------------------------
// distanceMeters
// ---------------------------------------------------------------------------

func TestDistanceMeters(t *testing.T) {
	tests := []struct {
		name                   string
		lat1, lon1, lat2, lon2 float64
		want, tolerance        float64
	}{
		{"same point", 53.9, 27.5, 53.9, 27.5, 0, 0},
		{"0.01 degree of latitude", 53.9, 27.5, 53.91, 27.5, 1112, 2},
		{"one degree of latitude", 0, 0, 1, 0, 111195, 50},
		{"one degree of longitude at the equator", 0, 0, 0, 1, 111195, 50},
		{"longitude shrinks with latitude", 60, 0, 60, 1, 55597, 50}, // cos(60°) = 0.5
		{"Minsk - Brest", 53.9006, 27.5590, 52.0976, 23.7341, 326000, 4000},
		{"antipodes", 0, 0, 0, 180, math.Pi * earthRadiusMeters, 1},
		{"across the date line", 0, 179.5, 0, -179.5, 111195, 50},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := distanceMeters(tc.lat1, tc.lon1, tc.lat2, tc.lon2)
			if math.Abs(got-tc.want) > tc.tolerance {
				t.Errorf("got %.1f, want %.1f ± %.0f", got, tc.want, tc.tolerance)
			}
			// расстояние симметрично
			if back := distanceMeters(tc.lat2, tc.lon2, tc.lat1, tc.lon1); math.Abs(back-got) > 1e-6 {
				t.Errorf("not symmetric: %v vs %v", got, back)
			}
		})
	}
}
