package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeTokens принимает единственный токен "good-token" и считает его токеном пользователя testUserID.
type fakeTokens struct{}

func (fakeTokens) ParseToken(token string) (int, error) {
	if token == "good-token" {
		return testUserID, nil
	}
	return 0, errBadToken
}

func newTestRouter() http.Handler {
	vehicles := &fakeVehicles{details: testVehicle}
	return NewRouter(Handlers{
		Auth:     NewAuthHandler(&fakeAuth{}, nil),
		Vehicles: NewVehicleHandler(&fakeVehicleService{}, nil),
		Stations: NewStationHandler(&fakeStationFinder{stations: manyStations(2)}, nil),
		Routes:   NewRoutesHandler(&fakePlanner{plan: samplePlan()}, vehicles, &fakeRecorder{}, nil),
		History:  NewHistoryHandler(&fakeHistory{}, nil),
		Tokens:   fakeTokens{},
	})
}

func serve(method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, req)
	return rec
}

func TestRouter_PublicEndpointsNeedNoToken(t *testing.T) {
	tests := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodGet, "/healthz", "", http.StatusOK},
		{http.MethodGet, "/ev-models", "", http.StatusOK},
		{http.MethodGet, "/stations/nearby?lat=53.9&lon=27.5", "", http.StatusOK},
		{http.MethodPost, "/auth/register", `{"name":"Ivan","email":"i@x.io","password":"password123"}`, http.StatusCreated},
		{http.MethodPost, "/auth/login", `{"email":"i@x.io","password":"password123"}`, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			if rec := serve(tc.method, tc.path, tc.body, ""); rec.Code != tc.want {
				t.Errorf("status: got %d, want %d; body: %s", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

func TestRouter_ProtectedEndpointsRejectAnonymous(t *testing.T) {
	tests := []struct{ method, path, body string }{
		{http.MethodPost, "/routes", validBody},
		{http.MethodGet, "/routes/history", ""},
		{http.MethodPost, "/vehicles", `{"ev_model_id":1}`},
		{http.MethodGet, "/vehicles", ""},
		{http.MethodGet, "/vehicles/1", ""},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := serve(tc.method, tc.path, tc.body, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status: got %d, want 401", rec.Code)
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("401 must carry a WWW-Authenticate header")
			}

			// и с неверным токеном - тоже 401
			if rec := serve(tc.method, tc.path, tc.body, "bad-token"); rec.Code != http.StatusUnauthorized {
				t.Errorf("bad token: got %d, want 401", rec.Code)
			}
		})
	}
}

func TestRouter_ProtectedEndpointsWorkWithToken(t *testing.T) {
	tests := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/routes", validBody, http.StatusOK},
		{http.MethodGet, "/routes/history", "", http.StatusOK},
		{http.MethodGet, "/vehicles", "", http.StatusOK},
		{http.MethodGet, "/vehicles/7", "", http.StatusOK},
		{http.MethodPost, "/vehicles", `{"ev_model_id":3}`, http.StatusCreated},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			if rec := serve(tc.method, tc.path, tc.body, "good-token"); rec.Code != tc.want {
				t.Errorf("status: got %d, want %d; body: %s", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

func TestRouter_MethodsAndPaths(t *testing.T) {
	tests := []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodGet, "/auth/login", http.StatusMethodNotAllowed},
		{http.MethodPost, "/ev-models", http.StatusMethodNotAllowed},
		{http.MethodPost, "/nope", http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			if rec := serve(tc.method, tc.path, "", "good-token"); rec.Code != tc.want {
				t.Errorf("status: got %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
