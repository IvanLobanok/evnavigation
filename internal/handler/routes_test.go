package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IvanLobanok/evnavigation/internal/models/evmodel"
	"github.com/IvanLobanok/evnavigation/internal/models/route"
	"github.com/IvanLobanok/evnavigation/internal/models/station"
	"github.com/IvanLobanok/evnavigation/internal/models/user"
	"github.com/IvanLobanok/evnavigation/internal/service/routing"
)

// ---------------------------------------------------------------------------
// Заглушки
// ---------------------------------------------------------------------------

type fakePlanner struct {
	plan  *routing.RoutePlan
	err   error
	calls int

	gotFrom, gotTo route.RoutePoint
	gotEV          evmodel.EVModel
	gotPercent     float64
}

func (f *fakePlanner) PlanRoute(_ context.Context, from, to route.RoutePoint, ev evmodel.EVModel, percent float64) (*routing.RoutePlan, error) {
	f.calls++
	f.gotFrom, f.gotTo, f.gotEV, f.gotPercent = from, to, ev, percent
	return f.plan, f.err
}

const testUserID = 5

type fakeVehicles struct {
	details *user.VehicleDetails
	err     error

	gotUserID, gotVehicleID int
}

func (f *fakeVehicles) GetVehicle(_ context.Context, userID, vehicleID int) (*user.VehicleDetails, error) {
	f.gotUserID, f.gotVehicleID = userID, vehicleID
	return f.details, f.err
}

var testModel = &evmodel.EVModel{ID: 3, Brand: "Geely", Model: "EX5", BatteryCapacityKwh: 60, AvgConsumptionKwh100km: 15, ConnectorType: "CCS2"}

var testVehicle = &user.VehicleDetails{
	UserVehicle: user.UserVehicle{ID: 7, UserID: testUserID, EVModelID: 3},
	EVModel:     *testModel,
}

const validBody = `{
	"from": {"lat": 53.9, "lon": 27.56},
	"to":   {"lat": 53.5, "lon": 27.1},
	"vehicle_id": 7,
	"current_charge_percent": 60
}`

func newHandler(p *fakePlanner, v *fakeVehicles) *RoutesHandler {
	return NewRoutesHandler(p, v, nil)
}

func post(h *RoutesHandler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/routes", strings.NewReader(body))
	req = req.WithContext(withUserID(req.Context(), testUserID)) // как после middleware Authenticate
	rec := httptest.NewRecorder()
	h.PlanRoute(rec, req)
	return rec
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) errorResponse {
	t.Helper()
	var e errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("cannot decode error body %q: %v", rec.Body.String(), err)
	}
	return e
}

func samplePlan() *routing.RoutePlan {
	leg1 := &routing.RouteAnalysis{
		TotalDistanceKm: 228.004,
		Points: []routing.PointAnalysis{
			{Point: route.RoutePoint{Lat: 53.9, Lon: 27.56}, CumulativeDistanceKm: 0, RemainingBatteryPercent: 60},
			{Point: route.RoutePoint{Lat: 53.7, Lon: 27.3}, CumulativeDistanceKm: 228.004, RemainingBatteryPercent: 14.4049},
		},
	}
	leg2 := &routing.RouteAnalysis{
		TotalDistanceKm: 172,
		Points: []routing.PointAnalysis{
			{Point: route.RoutePoint{Lat: 53.7, Lon: 27.3}, CumulativeDistanceKm: 0, RemainingBatteryPercent: 80},
			{Point: route.RoutePoint{Lat: 53.5, Lon: 27.1}, CumulativeDistanceKm: 172, RemainingBatteryPercent: 45.6},
		},
	}
	return &routing.RoutePlan{
		Legs: []*routing.RouteAnalysis{leg1, leg2},
		Stops: []routing.ChargeStop{{
			Station: station.ChargingStation{
				ID: 42, Name: "Fast Charge", Latitude: 53.7, Longitude: 27.3,
				PowerKW: 120, ConnectorTypes: []string{"CCS2"},
			},
			ArrivalPercent: 14.4049, DepartPercent: 80,
		}},
		TotalDistanceKm: 400.004,
	}
}

// ---------------------------------------------------------------------------
// Успешный сценарий
// ---------------------------------------------------------------------------

func TestPlanRoute_Success(t *testing.T) {
	planner := &fakePlanner{plan: samplePlan()}
	vehicles := &fakeVehicles{details: testVehicle}

	rec := post(newHandler(planner, vehicles), validBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: got %q", ct)
	}

	// запрос корректно передан вниз по слоям: машина ищется среди машин текущего пользователя
	if vehicles.gotVehicleID != 7 || vehicles.gotUserID != testUserID {
		t.Errorf("vehicle lookup: got user=%d vehicle=%d, want user=%d vehicle=7", vehicles.gotUserID, vehicles.gotVehicleID, testUserID)
	}
	if planner.gotEV.Model != "EX5" {
		t.Errorf("planner got wrong ev model: %+v", planner.gotEV)
	}
	if planner.gotFrom != (route.RoutePoint{Lat: 53.9, Lon: 27.56}) || planner.gotTo != (route.RoutePoint{Lat: 53.5, Lon: 27.1}) {
		t.Errorf("planner got wrong points: from=%+v to=%+v", planner.gotFrom, planner.gotTo)
	}
	if planner.gotPercent != 60 {
		t.Errorf("start percent: got %v, want 60", planner.gotPercent)
	}

	var resp planRouteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalDistanceKm != 400 {
		t.Errorf("total_distance_km: got %v, want 400 (rounded)", resp.TotalDistanceKm)
	}
	if len(resp.Legs) != 2 || len(resp.Stops) != 1 {
		t.Fatalf("legs/stops: got %d/%d, want 2/1", len(resp.Legs), len(resp.Stops))
	}
	if got := resp.Legs[0].Points[1].ChargePercent; got != 14.4 {
		t.Errorf("rounded charge percent: got %v, want 14.4", got)
	}
	if resp.ArrivalChargePercent == nil || math.Abs(*resp.ArrivalChargePercent-45.6) > 1e-9 {
		t.Errorf("arrival_charge_percent: got %v, want 45.6", resp.ArrivalChargePercent)
	}

	stop := resp.Stops[0]
	if stop.AfterLeg != 0 || stop.Station.ID != 42 || stop.DepartChargePercent != 80 || stop.ArrivalChargePercent != 14.4 {
		t.Errorf("unexpected stop: %+v", stop)
	}
	if len(stop.Station.ConnectorTypes) != 1 || stop.Station.ConnectorTypes[0] != "CCS2" {
		t.Errorf("connector types lost: %+v", stop.Station)
	}
}

func TestPlanRoute_NoStopsReturnsEmptyArraysNotNull(t *testing.T) {
	plan := &routing.RoutePlan{
		Legs: []*routing.RouteAnalysis{{
			TotalDistanceKm: 10,
			Points:          []routing.PointAnalysis{{RemainingBatteryPercent: 90}, {RemainingBatteryPercent: 88}},
		}},
		TotalDistanceKm: 10,
	}
	rec := post(newHandler(&fakePlanner{plan: plan}, &fakeVehicles{details: testVehicle}), validBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	// Android-клиенту удобнее "stops": [], чем "stops": null
	if !strings.Contains(rec.Body.String(), `"stops":[]`) {
		t.Errorf("stops must be an empty array, body: %s", rec.Body)
	}
}

func TestPlanRoute_ZeroCoordinatesAndZeroChargeAreValid(t *testing.T) {
	body := `{"from":{"lat":0,"lon":0},"to":{"lat":0,"lon":1},"vehicle_id":1,"current_charge_percent":0}`
	planner := &fakePlanner{plan: samplePlan()}

	rec := post(newHandler(planner, &fakeVehicles{details: testVehicle}), body)

	if rec.Code != http.StatusOK {
		t.Fatalf("zeros must be accepted, got %d: %s", rec.Code, rec.Body)
	}
	if planner.gotPercent != 0 {
		t.Errorf("percent: got %v, want 0", planner.gotPercent)
	}
}

// ---------------------------------------------------------------------------
// Некорректный запрос
// ---------------------------------------------------------------------------

func TestPlanRoute_BadJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"not json", `hello`},
		{"empty body", ``},
		{"unknown field", `{"from":{"lat":1,"lon":1},"to":{"lat":1,"lon":1},"vehicle_id":1,"current_charge_percent":50,"extra":1}`},
		{"wrong type", `{"from":{"lat":"x","lon":1},"to":{"lat":1,"lon":1},"vehicle_id":1,"current_charge_percent":50}`},
		{"trailing garbage", validBody + ` {"again": true}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			planner := &fakePlanner{plan: samplePlan()}
			rec := post(newHandler(planner, &fakeVehicles{details: testVehicle}), tc.body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want 400", rec.Code)
			}
			if code := decodeErr(t, rec).Error.Code; code != "invalid_json" {
				t.Errorf("code: got %q, want invalid_json", code)
			}
			if planner.calls != 0 {
				t.Error("planner must not be called")
			}
		})
	}
}

func TestPlanRoute_Validation(t *testing.T) {
	body := func(from, to, rest string) string {
		return fmt.Sprintf(`{"from":%s,"to":%s,%s}`, from, to, rest)
	}
	okPt := `{"lat":53.9,"lon":27.5}`
	okRest := `"vehicle_id":1,"current_charge_percent":50`

	tests := []struct {
		name      string
		body      string
		wantField string
	}{
		{"missing from", `{"to":` + okPt + `,` + okRest + `}`, "from"},
		{"missing to", `{"from":` + okPt + `,` + okRest + `}`, "to"},
		{"lat too high", body(`{"lat":91,"lon":0}`, okPt, okRest), "from.lat"},
		{"lat too low", body(okPt, `{"lat":-90.5,"lon":0}`, okRest), "to.lat"},
		{"lon too high", body(`{"lat":0,"lon":181}`, okPt, okRest), "from.lon"},
		{"missing lon", body(`{"lat":10}`, okPt, okRest), "from.lon"},
		{"missing vehicle_id", body(okPt, okPt, `"current_charge_percent":50`), "vehicle_id"},
		{"negative vehicle_id", body(okPt, okPt, `"vehicle_id":-3,"current_charge_percent":50`), "vehicle_id"},
		{"missing charge", body(okPt, okPt, `"vehicle_id":1`), "current_charge_percent"},
		{"charge above 100", body(okPt, okPt, `"vehicle_id":1,"current_charge_percent":100.5`), "current_charge_percent"},
		{"negative charge", body(okPt, okPt, `"vehicle_id":1,"current_charge_percent":-1`), "current_charge_percent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			planner := &fakePlanner{plan: samplePlan()}
			vehicles := &fakeVehicles{details: testVehicle}

			rec := post(newHandler(planner, vehicles), tc.body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want 400; body: %s", rec.Code, rec.Body)
			}
			e := decodeErr(t, rec)
			if e.Error.Code != "validation_error" {
				t.Errorf("code: got %q, want validation_error", e.Error.Code)
			}
			if _, ok := e.Error.Details[tc.wantField]; !ok {
				t.Errorf("details must mention %q, got %v", tc.wantField, e.Error.Details)
			}
			if planner.calls != 0 || vehicles.gotVehicleID != 0 {
				t.Error("neither repository nor planner may be called on invalid input")
			}
		})
	}
}

func TestPlanRoute_ValidationReportsAllProblemsAtOnce(t *testing.T) {
	rec := post(newHandler(&fakePlanner{}, &fakeVehicles{}), `{"vehicle_id":0}`)

	details := decodeErr(t, rec).Error.Details
	for _, field := range []string{"from", "to", "vehicle_id", "current_charge_percent"} {
		if _, ok := details[field]; !ok {
			t.Errorf("expected problem for %q, got %v", field, details)
		}
	}
}

func TestPlanRoute_BodyTooLarge(t *testing.T) {
	huge := `{"from":{"lat":1,"lon":1},"pad":"` + strings.Repeat("a", maxBodyBytes+10) + `"}`

	rec := post(newHandler(&fakePlanner{}, &fakeVehicles{}), huge)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d, want 413", rec.Code)
	}
	if code := decodeErr(t, rec).Error.Code; code != "body_too_large" {
		t.Errorf("code: got %q", code)
	}
}

// ---------------------------------------------------------------------------
// Ошибки зависимостей
// ---------------------------------------------------------------------------

func TestPlanRoute_VehicleNotFound(t *testing.T) {
	planner := &fakePlanner{plan: samplePlan()}
	// сервис может обернуть ошибку: errors.Is должен всё равно сработать
	vehicles := &fakeVehicles{err: fmt.Errorf("get vehicle: %w", user.ErrVehicleNotFound)}

	rec := post(newHandler(planner, vehicles), validBody)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rec.Code)
	}
	if code := decodeErr(t, rec).Error.Code; code != "vehicle_not_found" {
		t.Errorf("code: got %q", code)
	}
	if planner.calls != 0 {
		t.Error("planner must not be called")
	}
}

func TestPlanRoute_WithoutAuthenticatedUserIsRejected(t *testing.T) {
	planner := &fakePlanner{plan: samplePlan()}
	vehicles := &fakeVehicles{details: testVehicle}

	// запрос без withUserID в контексте: так выглядит роутер, где забыли middleware Authenticate
	req := httptest.NewRequest(http.MethodPost, "/routes", strings.NewReader(validBody))
	rec := httptest.NewRecorder()
	newHandler(planner, vehicles).PlanRoute(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
	if planner.calls != 0 || vehicles.gotVehicleID != 0 {
		t.Error("nothing may be called for an anonymous request")
	}
}

func TestPlanRoute_InternalErrorsDoNotLeakDetails(t *testing.T) {
	secret := "pq: password authentication failed for user postgres"

	tests := []struct {
		name     string
		planner  *fakePlanner
		vehicles *fakeVehicles
	}{
		{"repository failure", &fakePlanner{}, &fakeVehicles{err: errors.New(secret)}},
		{"planner failure", &fakePlanner{err: fmt.Errorf("failed to build route: %w", errors.New(secret))}, &fakeVehicles{details: testVehicle}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := post(newHandler(tc.planner, tc.vehicles), validBody)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status: got %d, want 500", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "password") {
				t.Errorf("internal error text leaked to client: %s", rec.Body)
			}
			if code := decodeErr(t, rec).Error.Code; code != "internal_error" {
				t.Errorf("code: got %q", code)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Ошибки планирования маршрута (422)
// ---------------------------------------------------------------------------

func TestPlanRoute_RoutingErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string
	}{
		{"no station nearby", routing.ErrNoStationNearby, "no_station_nearby"},
		{"station unreachable", routing.ErrStationUnreachable, "station_unreachable"},
		{"too many stops", routing.ErrTooManyStops, "too_many_stops"},
		{"wrapped error is still recognised", fmt.Errorf("plan: %w", routing.ErrNoStationNearby), "no_station_nearby"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := post(newHandler(&fakePlanner{plan: &routing.RoutePlan{}, err: tc.err}, &fakeVehicles{details: testVehicle}), validBody)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status: got %d, want 422", rec.Code)
			}
			if code := decodeErr(t, rec).Error.Code; code != tc.wantCode {
				t.Errorf("code: got %q, want %q", code, tc.wantCode)
			}
		})
	}
}

func TestPlanRoute_PartialPlanIsReturnedWithRoutingError(t *testing.T) {
	// удалось спланировать первый участок и остановку, дальше станций нет
	partial := samplePlan()
	partial.Legs = partial.Legs[:1]

	rec := post(newHandler(&fakePlanner{plan: partial, err: routing.ErrNoStationNearby}, &fakeVehicles{details: testVehicle}), validBody)

	e := decodeErr(t, rec)
	if e.PartialPlan == nil {
		t.Fatal("partial_plan must be present")
	}
	if len(e.PartialPlan.Legs) != 1 || len(e.PartialPlan.Stops) != 1 {
		t.Errorf("partial plan: got %d legs / %d stops, want 1/1", len(e.PartialPlan.Legs), len(e.PartialPlan.Stops))
	}
	if e.PartialPlan.ArrivalChargePercent != nil {
		t.Error("a partial plan does not arrive anywhere: arrival_charge_percent must be omitted")
	}
}

func TestPlanRoute_EmptyPlanIsNotReturnedAsPartial(t *testing.T) {
	rec := post(newHandler(&fakePlanner{plan: &routing.RoutePlan{}, err: routing.ErrNoStationNearby}, &fakeVehicles{details: testVehicle}), validBody)

	if strings.Contains(rec.Body.String(), "partial_plan") {
		t.Errorf("empty plan must not appear in the response: %s", rec.Body)
	}
}
