package station

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/IvanLobanok/evnavigation/internal/models/station"
)

var errBoom = errors.New("boom")

type fakeRepo struct {
	stations []station.ChargingStation
	err      error

	calls                     int
	gotLat, gotLon, gotRadius float64
	gotConnectors             []string
	gotLimit                  int
}

func (f *fakeRepo) FindNearbyStations(_ context.Context, lat, lon, radiusMeters float64, connectors []string, limit int) ([]station.ChargingStation, error) {
	f.calls++
	f.gotLat, f.gotLon, f.gotRadius, f.gotConnectors, f.gotLimit = lat, lon, radiusMeters, connectors, limit
	return f.stations, f.err
}

func TestGetAvailableNearbyStations_PassesQueryToRepository(t *testing.T) {
	repo := &fakeRepo{}
	_, err := NewService(repo).GetAvailableNearbyStations(context.Background(), 53.9, 27.5, 7500, []string{"CCS Combo 2", "Type 2"}, 13)

	if err != nil {
		t.Fatal(err)
	}
	if repo.calls != 1 || repo.gotLat != 53.9 || repo.gotLon != 27.5 || repo.gotRadius != 7500 || repo.gotLimit != 13 {
		t.Errorf("repo got calls=%d lat=%v lon=%v radius=%v limit=%d", repo.calls, repo.gotLat, repo.gotLon, repo.gotRadius, repo.gotLimit)
	}
	if !slices.Equal(repo.gotConnectors, []string{"CCS Combo 2", "Type 2"}) {
		t.Errorf("connectors must reach the repository unchanged (filtering happens in SQL), got %v", repo.gotConnectors)
	}
}

func TestGetAvailableNearbyStations_ReturnsRepositoryResultAsIs(t *testing.T) {
	// репозиторий уже отфильтровал и отсортировал станции: сервис не должен ни убирать их, ни переставлять
	repo := &fakeRepo{stations: []station.ChargingStation{{ID: 9}, {ID: 3}, {ID: 7}, {ID: 1}}}

	got, err := NewService(repo).GetAvailableNearbyStations(context.Background(), 0, 0, 1000, []string{"CCS Combo 2"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []int64{9, 3, 7, 1}) {
		t.Errorf("got %v, want [9 3 7 1]", ids)
	}
}

func TestGetAvailableNearbyStations_NoStations(t *testing.T) {
	got, err := NewService(&fakeRepo{}).GetAvailableNearbyStations(context.Background(), 0, 0, 1000, []string{"CCS Combo 2"}, 5)

	if err != nil || len(got) != 0 {
		t.Errorf("got %d stations, err=%v", len(got), err)
	}
}

func TestGetAvailableNearbyStations_RepositoryErrorIsWrapped(t *testing.T) {
	got, err := NewService(&fakeRepo{err: errBoom}).GetAvailableNearbyStations(context.Background(), 0, 0, 1000, nil, 5)

	if !errors.Is(err, errBoom) {
		t.Fatalf("got %v, want wrapped errBoom", err)
	}
	if got != nil {
		t.Errorf("no stations expected on error, got %d", len(got))
	}
}
