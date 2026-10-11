package station

import (
	"context"
	"evnavigation/internal/domain"
	"fmt"
	"slices"
)

type Repository interface {
	FindNearByStations(ctx context.Context, lat, lon, radiusMeters float64) ([]domain.ChargingStation, error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetAvailableNearbyStations(ctx context.Context, lat, lon, radiusMeters float64, preferredConnectors []string) ([]domain.ChargingStation, error) {
	stations, err := s.repo.FindNearByStations(ctx, lat, lon, radiusMeters)
	if err != nil {
		return nil, fmt.Errorf("station service error: %w", err)
	}

	if len(preferredConnectors) == 0 {
		return stations, nil
	}

	var filtered []domain.ChargingStation
	for _, station := range stations {
		if hasCommonConnector(preferredConnectors, station.ConnectorTypes) {
			filtered = append(filtered, station)
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
