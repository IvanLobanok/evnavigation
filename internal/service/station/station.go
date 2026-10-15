package station

import (
	"context"
	"fmt"
	"slices"

	"github.com/IvanLobanok/evnavigation/internal/models/station"
)

type Repository interface {
	FindNearbyStations(ctx context.Context, lat, lon, radiusMeters float64) ([]station.ChargingStation, error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetAvailableNearbyStations(ctx context.Context, lat, lon, radiusMeters float64, preferredConnectors []string) ([]station.ChargingStation, error) {
	stations, err := s.repo.FindNearbyStations(ctx, lat, lon, radiusMeters)
	if err != nil {
		return nil, fmt.Errorf("station service error: %w", err)
	}

	if len(preferredConnectors) == 0 {
		return stations, nil
	}

	var filtered []station.ChargingStation
	for _, st := range stations {
		if hasCommonConnector(preferredConnectors, st.ConnectorTypes) {
			filtered = append(filtered, st)
		}
	}
	return filtered, nil

}

func hasCommonConnector(preferred, available []string) bool {
	for _, v := range available {
		if slices.Contains(preferred, v) {
			return true
		}
	}
	return false
}
