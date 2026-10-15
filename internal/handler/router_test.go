package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestRouter() http.Handler {
	return NewRouter(newHandler(&fakePlanner{plan: samplePlan()}, &fakeModels{model: testModel}))
}

func TestRouter_Healthz(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
}

func TestRouter_PostRoutes(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/routes", strings.NewReader(validBody)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rec.Code, rec.Body)
	}
}

func TestRouter_MethodsAndPaths(t *testing.T) {
	tests := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/routes", http.StatusMethodNotAllowed},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodPost, "/nope", http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newTestRouter().ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))

			if rec.Code != tc.want {
				t.Errorf("status: got %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
