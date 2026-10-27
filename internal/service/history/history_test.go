package history

import (
	"context"
	"errors"
	"testing"

	"github.com/IvanLobanok/evnavigation/internal/models/history"
)

var errBoom = errors.New("boom")

type fakeStore struct {
	items     []history.RouteRequest
	createErr error
	listErr   error

	created             []history.RouteRequest
	gotUserID, gotLimit int
	gotBeforeID         int64
}

func (f *fakeStore) CreateRouteRequest(_ context.Context, rr history.RouteRequest) (int, error) {
	f.created = append(f.created, rr)
	return len(f.created), f.createErr
}

func (f *fakeStore) ListRouteRequests(_ context.Context, userID, limit int, beforeID int64) ([]history.RouteRequest, error) {
	f.gotUserID, f.gotLimit, f.gotBeforeID = userID, limit, beforeID
	if f.listErr != nil {
		return nil, f.listErr
	}
	if len(f.items) > limit { // как настоящее хранилище: не больше LIMIT
		return f.items[:limit], nil
	}
	return f.items, nil
}

func items(n int) []history.RouteRequest {
	out := make([]history.RouteRequest, n)
	for i := range out {
		out[i] = history.RouteRequest{ID: n - i} // от новых к старым
	}
	return out
}

func TestRecord(t *testing.T) {
	store := &fakeStore{}
	rr := history.RouteRequest{UserID: 3, VehicleID: 9, Status: history.StatusOK}

	if err := NewService(store).Record(context.Background(), rr); err != nil {
		t.Fatal(err)
	}
	if len(store.created) != 1 || store.created[0].UserID != 3 || store.created[0].VehicleID != 9 {
		t.Errorf("store got %+v", store.created)
	}
}

func TestRecord_ErrorIsWrapped(t *testing.T) {
	err := NewService(&fakeStore{createErr: errBoom}).Record(context.Background(), history.RouteRequest{})

	if !errors.Is(err, errBoom) {
		t.Fatalf("got %v, want wrapped errBoom", err)
	}
}

func TestList_LimitIsNormalized(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		wantAsked int // сколько просим у хранилища: limit + 1 (лишняя запись нужна, чтобы узнать, есть ли ещё страницы)
	}{
		{"zero means default", 0, DefaultLimit + 1},
		{"negative means default", -5, DefaultLimit + 1},
		{"normal value", 7, 8},
		{"max", MaxLimit, MaxLimit + 1},
		{"above max is clamped", MaxLimit + 50, MaxLimit + 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			if _, _, err := NewService(store).List(context.Background(), 1, tc.limit, 0); err != nil {
				t.Fatal(err)
			}
			if store.gotLimit != tc.wantAsked {
				t.Errorf("store asked for %d rows, want %d", store.gotLimit, tc.wantAsked)
			}
		})
	}
}

func TestList_HasMore(t *testing.T) {
	tests := []struct {
		name        string
		inStore     int
		limit       int
		wantItems   int
		wantHasMore bool
	}{
		{"nothing", 0, 5, 0, false},
		{"fewer than a page", 3, 5, 3, false},
		{"exactly a page: nothing more", 5, 5, 5, false},
		{"one more than a page", 6, 5, 5, true},
		{"many more", 50, 5, 5, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, hasMore, err := NewService(&fakeStore{items: items(tc.inStore)}).List(context.Background(), 1, tc.limit, 0)

			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.wantItems || hasMore != tc.wantHasMore {
				t.Errorf("got %d items hasMore=%v, want %d and %v", len(got), hasMore, tc.wantItems, tc.wantHasMore)
			}
		})
	}
}

func TestList_TrimmedPageKeepsTheNewestRecords(t *testing.T) {
	got, _, err := NewService(&fakeStore{items: items(6)}).List(context.Background(), 1, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	// из шести записей (id 6..1) страница из пяти — это id 6..2, а «лишняя» самая старая отброшена
	if got[0].ID != 6 || got[len(got)-1].ID != 2 {
		t.Errorf("page is %d..%d, want 6..2", got[0].ID, got[len(got)-1].ID)
	}
}

func TestList_PassesOwnerAndCursor(t *testing.T) {
	store := &fakeStore{}
	if _, _, err := NewService(store).List(context.Background(), 42, 10, 777); err != nil {
		t.Fatal(err)
	}
	if store.gotUserID != 42 || store.gotBeforeID != 777 {
		t.Errorf("store got user=%d before_id=%d, want 42 and 777", store.gotUserID, store.gotBeforeID)
	}
}

func TestList_ErrorIsWrapped(t *testing.T) {
	got, hasMore, err := NewService(&fakeStore{listErr: errBoom}).List(context.Background(), 1, 10, 0)

	if !errors.Is(err, errBoom) {
		t.Fatalf("got %v, want wrapped errBoom", err)
	}
	if got != nil || hasMore {
		t.Error("nothing must be returned on error")
	}
}
