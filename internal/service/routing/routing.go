package routing

import (
	"context"
	"fmt"

	"evnavigation/internal/domain"
)

type Service struct {
	routeProvider         domain.RouteProvider
	criticalChargePercent float64
}

func NewService(routeProvider domain.RouteProvider, criticalChargePercent float64) *Service {
	return &Service{
		routeProvider:         routeProvider,
		criticalChargePercent: criticalChargePercent,
	}
}

type RoutePointAnalysis struct {
	Segment                 domain.RouteSegment `json:"point"`
	CumulativeDistanceKm    float64             `json:"cumulative_distance_km"`
	RemainingBatteryKwh     float64             `json:"remaining_battery_kwh"`
	RemainingBatteryPercent float64             `json:"remaining_percent"`
	IsCritical              bool                `json:"is_critical"`
}

type RouteAnalysis struct {
	PointsAnalyzed       []RoutePointAnalysis `json:"points"`
	TotalDistanceKm      float64              `json:"total_distance_km"`
	ReachesDestination   bool                 `json:"reaches_destination"`    // хватает ли заряда доехать до конца без остановок
	CriticalPointIndexes []int                `json:"critical_point_indexes"` // индексы точек, где нужна подзарядка
}

func (s *Service) AnalyzeRoute(ctx context.Context, from, to domain.RoutePoint, evModel domain.EVModel, currentBatteryPercent float64) (*RouteAnalysis, error) {
	segments, err := s.routeProvider.BuildRoute(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to build route: %w", err)
	}

	fullRouteAnalyzed := &RouteAnalysis{
		PointsAnalyzed:       make([]RoutePointAnalysis, 0, len(segments)),
		TotalDistanceKm:      0.0,
		ReachesDestination:   true,
		CriticalPointIndexes: make([]int, 0),
	}

	remainingBatteryKwh := (currentBatteryPercent / 100) * evModel.BatteryCapacityKwh
	totalDistanceKm := 0.0

	for i, seg := range segments {
		segDistanceKm := seg.DistanceMeters / 1000.0
		segConsumption := (segDistanceKm / 100.0) * evModel.AvgConsumptionKwh100km

		remainingBatteryKwh -= segConsumption
		if remainingBatteryKwh < 0 {
			remainingBatteryKwh = 0
		}
		remainingBatteryPercent := (remainingBatteryKwh / evModel.BatteryCapacityKwh) * 100

		isCritical := remainingBatteryPercent <= s.criticalChargePercent
		if isCritical && fullRouteAnalyzed.ReachesDestination {
			fullRouteAnalyzed.ReachesDestination = false
			fullRouteAnalyzed.CriticalPointIndexes = append(fullRouteAnalyzed.CriticalPointIndexes, i)
		}

		totalDistanceKm += seg.DistanceMeters / 1000

		point := RoutePointAnalysis{
			Segment:                 seg,
			CumulativeDistanceKm:    totalDistanceKm,
			RemainingBatteryKwh:     remainingBatteryKwh,
			RemainingBatteryPercent: remainingBatteryPercent,
			IsCritical:              isCritical,
		}

		fullRouteAnalyzed.PointsAnalyzed = append(fullRouteAnalyzed.PointsAnalyzed, point)
	}

	fullRouteAnalyzed.TotalDistanceKm = totalDistanceKm

	return fullRouteAnalyzed, nil
}
