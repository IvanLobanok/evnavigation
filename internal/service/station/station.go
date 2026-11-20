package station

import (
	"context"
	"fmt"

	"github.com/IvanLobanok/evnavigation/internal/models/station"
)

type Repository interface {
	// FindNearbyStations: работающие станции в радиусе, ближайшие первыми, не больше limit штук;
	// непустой connectors оставляет только станции хотя бы с одним из этих разъёмов.
	FindNearbyStations(ctx context.Context, lat, lon, radiusMeters float64, connectors []string, limit int) ([]station.ChargingStation, error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// GetAvailableNearbyStations возвращает до limit ближайших работающих станций с подходящими разъёмами.
// Фильтрация и ограничение числа строк выполняются в репозитории (в SQL), а не здесь:
// так из базы не приезжают станции, которые всё равно будут отброшены.
func (s *Service) GetAvailableNearbyStations(ctx context.Context, lat, lon, radiusMeters float64, preferredConnectors []string, limit int) ([]station.ChargingStation, error) {
	stations, err := s.repo.FindNearbyStations(ctx, lat, lon, radiusMeters, preferredConnectors, limit)
	if err != nil {
		return nil, fmt.Errorf("station service error: %w", err)
	}
	return stations, nil
}
