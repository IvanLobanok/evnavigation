package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/history"
	"github.com/IvanLobanok/evnavigation/internal/models/route"
)

type fakeHistory struct {
	items   []history.RouteRequest
	hasMore bool
	err     error

	calls               int
	gotUserID, gotLimit int
	gotBeforeID         int64
}

func (f *fakeHistory) List(_ context.Context, userID, limit int, beforeID int64) ([]history.RouteRequest, bool, error) {
	f.calls++
	f.gotUserID, f.gotLimit, f.gotBeforeID = userID, limit, beforeID
	return f.items, f.hasMore, f.err
}

func getHistory(h *HistoryHandler, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/routes/history?"+query, nil)
	req = req.WithContext(withUserID(req.Context(), testUserID))
	rec := httptest.NewRecorder()
	h.List(rec, req)
	return rec
}

func decodeHistory(t *testing.T, rec *httptest.ResponseRecorder) historyResponse {
	t.Helper()
	var resp historyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return resp
}

func sampleHistory() []history.RouteRequest {
	km := 36.96
	return []history.RouteRequest{
		{
			ID: 12, UserID: testUserID, VehicleID: 7,
			From: route.RoutePoint{Lat: 53.9006, Lon: 27.559}, To: route.RoutePoint{Lat: 53.6833, Lon: 27.1333},
			InitialChargePercent: 60, Status: history.StatusOK, StopsCount: 0, TotalDistanceKm: &km,
			CreatedAt: time.Date(2026, 10, 25, 9, 30, 0, 0, time.UTC),
		},
		{
			ID: 11, UserID: testUserID, VehicleID: 7,
			From: route.RoutePoint{Lat: 53.82, Lon: 27.35}, To: route.RoutePoint{Lat: 53.95, Lon: 27.7},
			InitialChargePercent: 17, Status: history.StatusNoStationNearby, StopsCount: 1,
			CreatedAt: time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC),
		},
	}
}

func TestHistory_Success(t *testing.T) {
	svc := &fakeHistory{items: sampleHistory()}
	rec := getHistory(NewHistoryHandler(svc, nil), "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if svc.gotUserID != testUserID {
		t.Errorf("history must be read for the authenticated user: got %d, want %d", svc.gotUserID, testUserID)
	}

	resp := decodeHistory(t, rec)
	if len(resp.Requests) != 2 || resp.Requests[0].ID != 12 || resp.Requests[1].ID != 11 {
		t.Fatalf("order must be preserved: %+v", resp.Requests)
	}

	first := resp.Requests[0]
	if first.VehicleID != 7 || first.From != (latLonDTO{Lat: 53.9006, Lon: 27.559}) || first.To != (latLonDTO{Lat: 53.6833, Lon: 27.1333}) {
		t.Errorf("fields lost: %+v", first)
	}
	if first.InitialChargePercent != 60 || first.Status != "ok" || first.StopsCount != 0 {
		t.Errorf("fields lost: %+v", first)
	}
	if first.TotalDistanceKm == nil || *first.TotalDistanceKm != 36.96 {
		t.Errorf("total_distance_km: got %v, want 36.96", first.TotalDistanceKm)
	}
	if !first.CreatedAt.Equal(time.Date(2026, 10, 25, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("created_at: %v", first.CreatedAt)
	}

	second := resp.Requests[1]
	if second.Status != "no_station_nearby" || second.StopsCount != 1 || second.TotalDistanceKm != nil {
		t.Errorf("failed request must have null distance: %+v", second)
	}
	if !strings.Contains(rec.Body.String(), `"total_distance_km":null`) {
		t.Errorf("distance of a failed request must be an explicit null: %s", rec.Body)
	}
}

func TestHistory_Defaults(t *testing.T) {
	svc := &fakeHistory{}
	getHistory(NewHistoryHandler(svc, nil), "")

	// пустые параметры: дефолтный размер страницы и «с самого начала»
	if svc.gotLimit != 20 || svc.gotBeforeID != 0 {
		t.Errorf("defaults: limit=%d before_id=%d, want 20 and 0", svc.gotLimit, svc.gotBeforeID)
	}
}

func TestHistory_PaginationParametersAreForwarded(t *testing.T) {
	svc := &fakeHistory{}
	getHistory(NewHistoryHandler(svc, nil), "limit=5&before_id=42")

	if svc.gotLimit != 5 || svc.gotBeforeID != 42 {
		t.Errorf("got limit=%d before_id=%d, want 5 and 42", svc.gotLimit, svc.gotBeforeID)
	}
}

func TestHistory_NextCursor(t *testing.T) {
	t.Run("more pages: cursor is the id of the last item", func(t *testing.T) {
		rec := getHistory(NewHistoryHandler(&fakeHistory{items: sampleHistory(), hasMore: true}, nil), "limit=2")

		resp := decodeHistory(t, rec)
		if resp.NextBeforeID == nil || *resp.NextBeforeID != 11 {
			t.Errorf("next_before_id: got %v, want 11", resp.NextBeforeID)
		}
	})
	t.Run("last page: cursor is null", func(t *testing.T) {
		rec := getHistory(NewHistoryHandler(&fakeHistory{items: sampleHistory(), hasMore: false}, nil), "")

		if !strings.Contains(rec.Body.String(), `"next_before_id":null`) {
			t.Errorf("next_before_id must be an explicit null: %s", rec.Body)
		}
	})
}

func TestHistory_EmptyIsArrayNotNull(t *testing.T) {
	rec := getHistory(NewHistoryHandler(&fakeHistory{}, nil), "")

	if !strings.Contains(rec.Body.String(), `"requests":[]`) {
		t.Errorf("empty history must be []: %s", rec.Body)
	}
}

func TestHistory_Validation(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantField string
	}{
		{"limit zero", "limit=0", "limit"},
		{"limit negative", "limit=-1", "limit"},
		{"limit above max", "limit=101", "limit"},
		{"limit not a number", "limit=many", "limit"},
		{"limit fractional", "limit=2.5", "limit"},
		{"before_id zero", "before_id=0", "before_id"},
		{"before_id negative", "before_id=-5", "before_id"},
		{"before_id not a number", "before_id=abc", "before_id"},
		{"before_id overflows int64", "before_id=99999999999999999999", "before_id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeHistory{}
			rec := getHistory(NewHistoryHandler(svc, nil), tc.query)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want 400", rec.Code)
			}
			if _, ok := decodeErr(t, rec).Error.Details[tc.wantField]; !ok {
				t.Errorf("details must mention %q: %s", tc.wantField, rec.Body)
			}
			if svc.calls != 0 {
				t.Error("service must not be called")
			}
		})
	}
}

func TestHistory_RequiresAuthenticatedUser(t *testing.T) {
	svc := &fakeHistory{}
	rec := httptest.NewRecorder()
	NewHistoryHandler(svc, nil).List(rec, httptest.NewRequest(http.MethodGet, "/routes/history", nil))

	if rec.Code != http.StatusUnauthorized || svc.calls != 0 {
		t.Errorf("status=%d calls=%d, want 401 and 0", rec.Code, svc.calls)
	}
}

func TestHistory_ServiceErrorDoesNotLeak(t *testing.T) {
	rec := getHistory(NewHistoryHandler(&fakeHistory{err: errors.New("pq: password authentication failed")}, nil), "")

	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "password") {
		t.Errorf("want a clean 500, got %d %s", rec.Code, rec.Body)
	}
}
