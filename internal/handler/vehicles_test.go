package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/evmodel"
	"github.com/IvanLobanok/evnavigation/internal/models/user"
	"github.com/IvanLobanok/evnavigation/internal/service/vehicle"
	"github.com/go-chi/chi/v5"
)

type fakeVehicleService struct {
	models  []evmodel.EVModel
	list    []user.VehicleDetails
	details *user.VehicleDetails
	err     error

	gotUserID, gotModelID, gotVehicleID int
	gotNickname                         string
	calls                               int
}

func (f *fakeVehicleService) ListModels(context.Context) ([]evmodel.EVModel, error) {
	f.calls++
	if f.models == nil && f.err == nil {
		return []evmodel.EVModel{*testModel}, nil
	}
	return f.models, f.err
}

func (f *fakeVehicleService) AddVehicle(_ context.Context, userID, modelID int, nickname string) (*user.VehicleDetails, error) {
	f.calls++
	f.gotUserID, f.gotModelID, f.gotNickname = userID, modelID, nickname
	if f.err != nil {
		return nil, f.err
	}
	if f.details != nil {
		return f.details, nil
	}
	d := *testVehicle
	return &d, nil
}

func (f *fakeVehicleService) GetVehicle(_ context.Context, userID, vehicleID int) (*user.VehicleDetails, error) {
	f.calls++
	f.gotUserID, f.gotVehicleID = userID, vehicleID
	if f.err != nil {
		return nil, f.err
	}
	if f.details != nil {
		return f.details, nil
	}
	return testVehicle, nil
}

func (f *fakeVehicleService) ListVehicles(_ context.Context, userID int) ([]user.VehicleDetails, error) {
	f.calls++
	f.gotUserID = userID
	return f.list, f.err
}

// authed делает запрос от имени testUserID, как будто middleware уже отработал.
func authed(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	return req.WithContext(withUserID(req.Context(), testUserID))
}

func withURLParam(req *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// ---------------------------------------------------------------------------
// GET /ev-models
// ---------------------------------------------------------------------------

func TestListModels(t *testing.T) {
	svc := &fakeVehicleService{models: []evmodel.EVModel{
		{ID: 1, Brand: "BYD", Model: "Dolphin", BatteryCapacityKwh: 60.4, AvgConsumptionKwh100km: 14.5, ConnectorType: "CCS Combo 2", MaxChargingPowerKw: 88},
		{ID: 2, Brand: "Geely", Model: "EX5"},
	}}
	rec := httptest.NewRecorder()
	NewVehicleHandler(svc, nil).ListModels(rec, httptest.NewRequest(http.MethodGet, "/ev-models", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"brand":"BYD"`, `"battery_capacity_kwh":60.4`, `"avg_consumption_kwh_100km":14.5`, `"connector_type":"CCS Combo 2"`, `"max_charging_power_kw":88`, `"model":"EX5"`} {
		if !strings.Contains(body, want) {
			t.Errorf("response must contain %s, got %s", want, body)
		}
	}
}

func TestListModels_EmptyAndError(t *testing.T) {
	rec := httptest.NewRecorder()
	NewVehicleHandler(&fakeVehicleService{models: []evmodel.EVModel{}}, nil).ListModels(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), `"models":[]`) {
		t.Errorf("empty catalog must be [] not null: %s", rec.Body)
	}

	rec = httptest.NewRecorder()
	NewVehicleHandler(&fakeVehicleService{err: errors.New("db password=hunter2")}, nil).ListModels(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("want clean 500, got %d %s", rec.Code, rec.Body)
	}
}

// ---------------------------------------------------------------------------
// POST /vehicles
// ---------------------------------------------------------------------------

func TestCreateVehicle_Success(t *testing.T) {
	nick := "my car"
	svc := &fakeVehicleService{details: &user.VehicleDetails{
		UserVehicle: user.UserVehicle{ID: 11, UserID: testUserID, EVModelID: 3, Nickname: &nick, CreatedAt: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)},
		EVModel:     *testModel,
	}}
	rec := httptest.NewRecorder()
	NewVehicleHandler(svc, nil).Create(rec, authed(http.MethodPost, "/vehicles", `{"ev_model_id":3,"nickname":"my car"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body: %s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/vehicles/11" {
		t.Errorf("Location: got %q", loc)
	}
	if svc.gotUserID != testUserID || svc.gotModelID != 3 || svc.gotNickname != "my car" {
		t.Errorf("service got user=%d model=%d nickname=%q", svc.gotUserID, svc.gotModelID, svc.gotNickname)
	}
	body := rec.Body.String()
	for _, want := range []string{`"id":11`, `"nickname":"my car"`, `"brand":"Geely"`, `"battery_capacity_kwh":60`} {
		if !strings.Contains(body, want) {
			t.Errorf("response must contain %s, got %s", want, body)
		}
	}
}

func TestCreateVehicle_NicknameIsOptional(t *testing.T) {
	svc := &fakeVehicleService{}
	rec := httptest.NewRecorder()
	NewVehicleHandler(svc, nil).Create(rec, authed(http.MethodPost, "/vehicles", `{"ev_model_id":3}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if svc.gotNickname != "" {
		t.Errorf("missing nickname must reach the service as empty, got %q", svc.gotNickname)
	}
	if !strings.Contains(rec.Body.String(), `"nickname":null`) {
		t.Errorf("absent nickname must serialize as null: %s", rec.Body)
	}
}

func TestCreateVehicle_OwnerComesFromTokenNotFromBody(t *testing.T) {
	svc := &fakeVehicleService{}
	rec := httptest.NewRecorder()
	// клиент пытается создать машину «за другого пользователя»
	NewVehicleHandler(svc, nil).Create(rec, authed(http.MethodPost, "/vehicles", `{"ev_model_id":3,"user_id":999}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field user_id must be rejected, got %d", rec.Code)
	}
	if svc.calls != 0 {
		t.Error("service must not be called")
	}
}

func TestCreateVehicle_Errors(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		svcErr     error
		wantStatus int
		wantCode   string
		wantField  string
	}{
		{"missing model id", `{}`, nil, 400, "validation_error", "ev_model_id"},
		{"zero model id", `{"ev_model_id":0}`, nil, 400, "validation_error", "ev_model_id"},
		{"negative model id", `{"ev_model_id":-1}`, nil, 400, "validation_error", "ev_model_id"},
		{"model id of wrong type", `{"ev_model_id":"3"}`, nil, 400, "invalid_json", ""},
		{"nickname too long", `{"ev_model_id":3,"nickname":"x"}`, vehicle.ErrInvalidNickname, 400, "validation_error", "nickname"},
		{"unknown model", `{"ev_model_id":999}`, evmodel.ErrNotFound, 404, "ev_model_not_found", ""},
		{"wrapped unknown model", `{"ev_model_id":999}`, fmt.Errorf("add: %w", evmodel.ErrNotFound), 404, "ev_model_not_found", ""},
		{"internal error", `{"ev_model_id":3}`, errors.New("pq: broken"), 500, "internal_error", ""},
		{"invalid json", `{`, nil, 400, "invalid_json", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			NewVehicleHandler(&fakeVehicleService{err: tc.svcErr}, nil).Create(rec, authed(http.MethodPost, "/vehicles", tc.body))

			if rec.Code != tc.wantStatus {
				t.Fatalf("status: got %d, want %d; body: %s", rec.Code, tc.wantStatus, rec.Body)
			}
			e := decodeErr(t, rec)
			if e.Error.Code != tc.wantCode {
				t.Errorf("code: got %q, want %q", e.Error.Code, tc.wantCode)
			}
			if tc.wantField != "" {
				if _, ok := e.Error.Details[tc.wantField]; !ok {
					t.Errorf("details must mention %q: %v", tc.wantField, e.Error.Details)
				}
			}
			if strings.Contains(rec.Body.String(), "pq:") {
				t.Errorf("internal error leaked: %s", rec.Body)
			}
		})
	}
}

func TestVehicleEndpoints_RejectRequestsWithoutUser(t *testing.T) {
	h := NewVehicleHandler(&fakeVehicleService{}, nil)
	anon := func(method, target, body string) *http.Request {
		return httptest.NewRequest(method, target, strings.NewReader(body))
	}

	tests := map[string]func(http.ResponseWriter){
		"create": func(w http.ResponseWriter) { h.Create(w, anon("POST", "/vehicles", `{"ev_model_id":1}`)) },
		"list":   func(w http.ResponseWriter) { h.List(w, anon("GET", "/vehicles", "")) },
		"get":    func(w http.ResponseWriter) { h.Get(w, withURLParam(anon("GET", "/vehicles/1", ""), "id", "1")) },
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			call(rec)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status: got %d, want 401", rec.Code)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// GET /vehicles/{id}
// ---------------------------------------------------------------------------

func TestGetVehicle_Success(t *testing.T) {
	svc := &fakeVehicleService{}
	rec := httptest.NewRecorder()
	NewVehicleHandler(svc, nil).Get(rec, withURLParam(authed(http.MethodGet, "/vehicles/7", ""), "id", "7"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if svc.gotUserID != testUserID || svc.gotVehicleID != 7 {
		t.Errorf("service got user=%d vehicle=%d, want %d and 7", svc.gotUserID, svc.gotVehicleID, testUserID)
	}
	if !strings.Contains(rec.Body.String(), `"id":7`) {
		t.Errorf("body: %s", rec.Body)
	}
}

func TestGetVehicle_InvalidID(t *testing.T) {
	for _, id := range []string{"abc", "0", "-1", "1.5", "", "99999999999999999999", "1;DROP TABLE"} {
		t.Run(id, func(t *testing.T) {
			svc := &fakeVehicleService{}
			rec := httptest.NewRecorder()
			NewVehicleHandler(svc, nil).Get(rec, withURLParam(authed(http.MethodGet, "/vehicles/x", ""), "id", id))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want 400", rec.Code)
			}
			if code := decodeErr(t, rec).Error.Code; code != "invalid_id" {
				t.Errorf("code: got %q", code)
			}
			if svc.calls != 0 {
				t.Error("service must not be called")
			}
		})
	}
}

func TestGetVehicle_NotFoundAndForeignLookTheSame(t *testing.T) {
	// репозиторий возвращает ErrVehicleNotFound и для чужой машины, и для несуществующей
	rec := httptest.NewRecorder()
	NewVehicleHandler(&fakeVehicleService{err: user.ErrVehicleNotFound}, nil).
		Get(rec, withURLParam(authed(http.MethodGet, "/vehicles/5", ""), "id", "5"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rec.Code)
	}
	if code := decodeErr(t, rec).Error.Code; code != "vehicle_not_found" {
		t.Errorf("code: got %q", code)
	}
}

func TestGetVehicle_InternalError(t *testing.T) {
	rec := httptest.NewRecorder()
	NewVehicleHandler(&fakeVehicleService{err: errors.New("pq: boom")}, nil).
		Get(rec, withURLParam(authed(http.MethodGet, "/vehicles/5", ""), "id", "5"))

	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "pq:") {
		t.Errorf("want clean 500, got %d %s", rec.Code, rec.Body)
	}
}

// ---------------------------------------------------------------------------
// GET /vehicles
// ---------------------------------------------------------------------------

func TestListVehicles(t *testing.T) {
	svc := &fakeVehicleService{list: []user.VehicleDetails{
		{UserVehicle: user.UserVehicle{ID: 1}, EVModel: *testModel},
		{UserVehicle: user.UserVehicle{ID: 2}, EVModel: *testModel},
	}}
	rec := httptest.NewRecorder()
	NewVehicleHandler(svc, nil).List(rec, authed(http.MethodGet, "/vehicles", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if svc.gotUserID != testUserID {
		t.Errorf("service got user %d, want %d", svc.gotUserID, testUserID)
	}
	if !strings.Contains(rec.Body.String(), `"id":1`) || !strings.Contains(rec.Body.String(), `"id":2`) {
		t.Errorf("body: %s", rec.Body)
	}
}

func TestListVehicles_EmptyIsArrayNotNull(t *testing.T) {
	rec := httptest.NewRecorder()
	NewVehicleHandler(&fakeVehicleService{}, nil).List(rec, authed(http.MethodGet, "/vehicles", ""))

	if !strings.Contains(rec.Body.String(), `"vehicles":[]`) {
		t.Errorf("empty list must be []: %s", rec.Body)
	}
}
