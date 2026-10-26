package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IvanLobanok/evnavigation/internal/models/history"
	"github.com/IvanLobanok/evnavigation/internal/models/route"
	"github.com/IvanLobanok/evnavigation/internal/models/station"
	"github.com/IvanLobanok/evnavigation/internal/models/user"
	"github.com/IvanLobanok/evnavigation/internal/service/routing"
)

type fakeRecorder struct {
	recorded []history.RouteRequest
	err      error

	ctxErrAtRecord error // ctx.Err() в момент записи
	hasDeadline    bool
}

func (f *fakeRecorder) Record(ctx context.Context, rr history.RouteRequest) error {
	f.recorded = append(f.recorded, rr)
	f.ctxErrAtRecord = ctx.Err()
	_, f.hasDeadline = ctx.Deadline()
	return f.err
}

func postRoute(h *RoutesHandler, body string) *httptest.ResponseRecorder {
	return post(h, body)
}

// ---------------------------------------------------------------------------
// Что записывается
// ---------------------------------------------------------------------------

func TestRecord_SuccessfulRoute(t *testing.T) {
	rec := &fakeRecorder{}
	h := NewRoutesHandler(&fakePlanner{plan: samplePlan()}, &fakeVehicles{details: testVehicle}, rec, nil)

	if got := postRoute(h, validBody); got.Code != http.StatusOK {
		t.Fatalf("status %d: %s", got.Code, got.Body)
	}

	if len(rec.recorded) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(rec.recorded))
	}
	rr := rec.recorded[0]
	if rr.UserID != testUserID || rr.VehicleID != 7 {
		t.Errorf("owner/vehicle: user=%d vehicle=%d, want %d and 7", rr.UserID, rr.VehicleID, testUserID)
	}
	if rr.From != (route.RoutePoint{Lat: 53.9, Lon: 27.56}) || rr.To != (route.RoutePoint{Lat: 53.5, Lon: 27.1}) {
		t.Errorf("points: from=%+v to=%+v", rr.From, rr.To)
	}
	if rr.InitialChargePercent != 60 {
		t.Errorf("initial charge: got %v, want 60", rr.InitialChargePercent)
	}
	if rr.Status != history.StatusOK || rr.StopsCount != 1 {
		t.Errorf("status=%q stops=%d, want ok and 1", rr.Status, rr.StopsCount)
	}
	if rr.TotalDistanceKm == nil || *rr.TotalDistanceKm != 400 {
		t.Errorf("total distance: got %v, want 400 (rounded like in the response)", rr.TotalDistanceKm)
	}
}

func TestRecord_RoutingFailuresAreRecordedWithTheirStatus(t *testing.T) {
	partial := samplePlan()
	partial.Legs = partial.Legs[:1]

	tests := []struct {
		name       string
		err        error
		plan       *routing.RoutePlan
		wantStatus string
		wantStops  int
		wantHTTP   int
	}{
		{"no station", routing.ErrNoStationNearby, partial, history.StatusNoStationNearby, 1, http.StatusUnprocessableEntity},
		{"unreachable", routing.ErrStationUnreachable, &routing.RoutePlan{}, history.StatusStationUnreachable, 0, http.StatusUnprocessableEntity},
		{"too many stops", routing.ErrTooManyStops, partial, history.StatusTooManyStops, 1, http.StatusUnprocessableEntity},
		{"wrapped routing error", fmt.Errorf("plan: %w", routing.ErrNoStationNearby), &routing.RoutePlan{}, history.StatusNoStationNearby, 0, http.StatusUnprocessableEntity},
		{"internal failure", errors.New("provider is down"), nil, history.StatusError, 0, http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &fakeRecorder{}
			h := NewRoutesHandler(&fakePlanner{plan: tc.plan, err: tc.err}, &fakeVehicles{details: testVehicle}, rec, nil)

			got := postRoute(h, validBody)

			if got.Code != tc.wantHTTP {
				t.Fatalf("http status: got %d, want %d", got.Code, tc.wantHTTP)
			}
			if len(rec.recorded) != 1 {
				t.Fatalf("a failed calculation must still be recorded, got %d records", len(rec.recorded))
			}
			rr := rec.recorded[0]
			if rr.Status != tc.wantStatus || rr.StopsCount != tc.wantStops {
				t.Errorf("status=%q stops=%d, want %q and %d", rr.Status, rr.StopsCount, tc.wantStatus, tc.wantStops)
			}
			if rr.TotalDistanceKm != nil {
				t.Errorf("distance must be null when the route was not built, got %v", *rr.TotalDistanceKm)
			}
		})
	}
}

func TestRecord_StatusMatchesTheErrorCodeInTheResponse(t *testing.T) {
	// история и ответ API говорят об одном и том же одним и тем же словом
	for _, err := range []error{routing.ErrNoStationNearby, routing.ErrStationUnreachable, routing.ErrTooManyStops} {
		rec := &fakeRecorder{}
		h := NewRoutesHandler(&fakePlanner{plan: &routing.RoutePlan{}, err: err}, &fakeVehicles{details: testVehicle}, rec, nil)

		got := postRoute(h, validBody)

		if code := decodeErr(t, got).Error.Code; code != rec.recorded[0].Status {
			t.Errorf("response code %q != recorded status %q", code, rec.recorded[0].Status)
		}
	}
}

// ---------------------------------------------------------------------------
// Что НЕ записывается
// ---------------------------------------------------------------------------

func TestRecord_NothingIsRecordedWhenNoCalculationHappened(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		vehicles *fakeVehicles
		ctx      func(*http.Request) *http.Request
	}{
		{"invalid json", `{`, &fakeVehicles{details: testVehicle}, nil},
		{"validation error", `{"vehicle_id":0}`, &fakeVehicles{details: testVehicle}, nil},
		{"vehicle not found", validBody, &fakeVehicles{err: user.ErrVehicleNotFound}, nil},
		{"vehicle lookup failure", validBody, &fakeVehicles{err: errors.New("db down")}, nil},
		{"anonymous request", validBody, &fakeVehicles{details: testVehicle}, func(r *http.Request) *http.Request {
			return r.WithContext(context.Background()) // без пользователя
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &fakeRecorder{}
			h := NewRoutesHandler(&fakePlanner{plan: samplePlan()}, tc.vehicles, rec, nil)

			req := httptest.NewRequest(http.MethodPost, "/routes", strings.NewReader(tc.body))
			req = req.WithContext(withUserID(req.Context(), testUserID))
			if tc.ctx != nil {
				req = tc.ctx(req)
			}
			h.PlanRoute(httptest.NewRecorder(), req)

			if len(rec.recorded) != 0 {
				t.Errorf("nothing must be recorded, got %+v", rec.recorded)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Надёжность записи
// ---------------------------------------------------------------------------

func TestRecord_FailureDoesNotBreakTheResponse(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	rec := &fakeRecorder{err: errors.New("pq: connection refused")}
	h := NewRoutesHandler(&fakePlanner{plan: samplePlan()}, &fakeVehicles{details: testVehicle}, rec, logger)

	got := postRoute(h, validBody)

	if got.Code != http.StatusOK {
		t.Fatalf("a history failure must not fail the request: status %d, body %s", got.Code, got.Body)
	}
	if strings.Contains(got.Body.String(), "connection refused") {
		t.Errorf("recording error leaked to the client: %s", got.Body)
	}
	if !strings.Contains(logs.String(), "failed to record route request") || !strings.Contains(logs.String(), "connection refused") {
		t.Errorf("the failure must be logged, got: %s", logs.String())
	}
}

func TestRecord_FailureDoesNotHideARoutingError(t *testing.T) {
	rec := &fakeRecorder{err: errors.New("db down")}
	h := NewRoutesHandler(&fakePlanner{plan: &routing.RoutePlan{}, err: routing.ErrNoStationNearby}, &fakeVehicles{details: testVehicle}, rec, nil)

	got := postRoute(h, validBody)

	if got.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422", got.Code)
	}
}

func TestRecord_SurvivesClientDisconnect(t *testing.T) {
	rec := &fakeRecorder{}
	h := NewRoutesHandler(&fakePlanner{plan: samplePlan()}, &fakeVehicles{details: testVehicle}, rec, nil)

	// клиент уже оборвал соединение: контекст запроса отменён
	ctx, cancel := context.WithCancel(withUserID(context.Background(), testUserID))
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/routes", strings.NewReader(validBody)).WithContext(ctx)
	h.PlanRoute(httptest.NewRecorder(), req)

	if len(rec.recorded) != 1 {
		t.Fatalf("the request must still be recorded, got %d", len(rec.recorded))
	}
	if rec.ctxErrAtRecord != nil {
		t.Errorf("recording must not inherit the cancelled request context: %v", rec.ctxErrAtRecord)
	}
	if !rec.hasDeadline {
		t.Error("recording must have its own timeout so it cannot hang forever")
	}
}

func TestRecord_UsesTheStationOfThePlanOnlyForCounting(t *testing.T) {
	// защита от паники: у частичного плана могут быть остановки без участков и наоборот
	plan := &routing.RoutePlan{Stops: []routing.ChargeStop{{Station: station.ChargingStation{ID: 1}}, {Station: station.ChargingStation{ID: 2}}}}
	rec := &fakeRecorder{}
	h := NewRoutesHandler(&fakePlanner{plan: plan, err: routing.ErrTooManyStops}, &fakeVehicles{details: testVehicle}, rec, nil)

	postRoute(h, validBody)

	if rec.recorded[0].StopsCount != 2 {
		t.Errorf("stops: got %d, want 2", rec.recorded[0].StopsCount)
	}
}
