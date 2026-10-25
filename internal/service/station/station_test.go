package station

import (
	"context"
	"errors"
	"testing"

	"github.com/IvanLobanok/evnavigation/internal/models/station"
)

var errBoom = errors.New("boom")

type fakeRepo struct {
	stations []station.ChargingStation
	err      error

	calls                     int
	gotLat, gotLon, gotRadius float64
}

func (f *fakeRepo) FindNearbyStations(_ context.Context, lat, lon, radiusMeters float64) ([]station.ChargingStation, error) {
	f.calls++
	f.gotLat, f.gotLon, f.gotRadius = lat, lon, radiusMeters
	return f.stations, f.err
}

func st(id int64, connectors ...string) station.ChargingStation {
	return station.ChargingStation{ID: id, ConnectorTypes: connectors}
}

func ids(list []station.ChargingStation) []int64 {
	out := make([]int64, 0, len(list))
	for _, s := range list {
		out = append(out, s.ID)
	}
	return out
}

func equalIDs(got []station.ChargingStation, want ...int64) bool {
	g := ids(got)
	if len(g) != len(want) {
		return false
	}
	for i := range g {
		if g[i] != want[i] {
			return false
		}
	}
	return true
}

func TestGetAvailableNearbyStations_PassesQueryToRepository(t *testing.T) {
	repo := &fakeRepo{}
	_, err := NewService(repo).GetAvailableNearbyStations(context.Background(), 53.9, 27.5, 7500, nil)

	if err != nil {
		t.Fatal(err)
	}
	if repo.calls != 1 || repo.gotLat != 53.9 || repo.gotLon != 27.5 || repo.gotRadius != 7500 {
		t.Errorf("repo got calls=%d lat=%v lon=%v radius=%v", repo.calls, repo.gotLat, repo.gotLon, repo.gotRadius)
	}
}

func TestGetAvailableNearbyStations_ConnectorFilter(t *testing.T) {
	all := []station.ChargingStation{
		st(1, "CCS Combo 2"),
		st(2, "CHAdeMO"),
		st(3, "GB/T DC", "CCS Combo 2"),
		st(4), // без разъёмов
		st(5, "Type 2"),
		st(6, "CHAdeMO", "GB/T DC"),
	}

	tests := []struct {
		name      string
		preferred []string
		want      []int64
	}{
		{"nil filter returns everything", nil, []int64{1, 2, 3, 4, 5, 6}},
		{"empty filter returns everything", []string{}, []int64{1, 2, 3, 4, 5, 6}},
		{"single connector", []string{"CCS Combo 2"}, []int64{1, 3}},
		{"station with several connectors matches by any of them", []string{"GB/T DC"}, []int64{3, 6}},
		{"several preferred connectors: any of them is enough", []string{"CHAdeMO", "Type 2"}, []int64{2, 5, 6}},
		{"unknown connector matches nothing", []string{"Tesla NACS"}, nil},
		{"matching is exact and case-sensitive", []string{"ccs combo 2"}, nil},
		{"a station without connectors never matches a filter", []string{""}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewService(&fakeRepo{stations: all}).GetAvailableNearbyStations(context.Background(), 0, 0, 1000, tc.preferred)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !equalIDs(got, tc.want...) {
				t.Errorf("got %v, want %v", ids(got), tc.want)
			}
		})
	}
}

func TestGetAvailableNearbyStations_KeepsRepositoryOrder(t *testing.T) {
	// репозиторий отдаёт станции от ближайшей к дальней; фильтр не должен менять порядок
	repo := &fakeRepo{stations: []station.ChargingStation{st(9, "CCS Combo 2"), st(3, "CCS Combo 2"), st(7, "CCS Combo 2"), st(1, "CCS Combo 2")}}

	got, err := NewService(repo).GetAvailableNearbyStations(context.Background(), 0, 0, 1000, []string{"CCS Combo 2"})
	if err != nil {
		t.Fatal(err)
	}
	if !equalIDs(got, 9, 3, 7, 1) {
		t.Errorf("order changed: %v", ids(got))
	}
}

func TestGetAvailableNearbyStations_DoesNotModifyRepositoryResult(t *testing.T) {
	original := []station.ChargingStation{st(1, "CHAdeMO"), st(2, "CCS Combo 2"), st(3, "CHAdeMO")}
	repo := &fakeRepo{stations: original}

	if _, err := NewService(repo).GetAvailableNearbyStations(context.Background(), 0, 0, 1000, []string{"CCS Combo 2"}); err != nil {
		t.Fatal(err)
	}
	if !equalIDs(original, 1, 2, 3) {
		t.Errorf("filtering must not rewrite the slice it received: %v", ids(original))
	}
}

func TestGetAvailableNearbyStations_NoStations(t *testing.T) {
	got, err := NewService(&fakeRepo{}).GetAvailableNearbyStations(context.Background(), 0, 0, 1000, []string{"CCS Combo 2"})

	if err != nil || len(got) != 0 {
		t.Errorf("got %d stations, err=%v", len(got), err)
	}
}

func TestGetAvailableNearbyStations_RepositoryErrorIsWrapped(t *testing.T) {
	got, err := NewService(&fakeRepo{err: errBoom}).GetAvailableNearbyStations(context.Background(), 0, 0, 1000, nil)

	if !errors.Is(err, errBoom) {
		t.Fatalf("got %v, want wrapped errBoom", err)
	}
	if got != nil {
		t.Errorf("no stations expected on error, got %d", len(got))
	}
}

func TestHasCommonConnector(t *testing.T) {
	tests := []struct {
		name      string
		preferred []string
		available []string
		want      bool
	}{
		{"match", []string{"A"}, []string{"A", "B"}, true},
		{"match on the second available", []string{"B"}, []string{"A", "B"}, true},
		{"no overlap", []string{"A"}, []string{"B"}, false},
		{"empty preferred", nil, []string{"A"}, false},
		{"empty available", []string{"A"}, nil, false},
		{"both empty", nil, nil, false},
		{"case differs", []string{"a"}, []string{"A"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasCommonConnector(tc.preferred, tc.available); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
