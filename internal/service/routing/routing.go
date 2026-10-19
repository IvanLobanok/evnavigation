package routing

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"

	"github.com/IvanLobanok/evnavigation/internal/models/evmodel"
	"github.com/IvanLobanok/evnavigation/internal/models/route"
	"github.com/IvanLobanok/evnavigation/internal/models/station"
)

var (
	ErrNoStationNearby    = errors.New("no compatible station in lookback zone")
	ErrStationUnreachable = errors.New("no station reachable with remaining charge")
	ErrTooManyStops       = errors.New("route requires too many charging stops")
)

// StationFinder - интерфейс для поиска зарядных станций (изолирует базу данных / репозиторий).
type StationFinder interface {
	GetAvailableNearbyStations(ctx context.Context, lat, lon, radiusMeters float64, preferredConnectors []string) ([]station.ChargingStation, error)
}

// Config содержит все константы и параметры для симуляции и построения маршрута.
type Config struct {
	CriticalChargePercent float64 // Порог, ниже которого батарея считается критической (напр. 15%)
	MinArrivalPercent     float64 // Минимально допустимый остаток батареи при прибытии на станцию (напр. 5%)
	TargetChargePercent   float64 // Целевой уровень заряда после подзарядки на станции (напр. 80%)
	SearchRadiusMeters    float64 // Радиус поиска зарядных станций вокруг точки (напр. 5000м)
	MaxStops              int     // Максимальное количество остановок на маршруте (защита от бесконечного цикла)
	MaxStationCandidates  int     // Максимальное количество станций для одновременной проверки
	LookbackStepKm        float64 // Шаг сканирования назад по маршруту при поиске станции
	MaxLookbackKm         float64 // Максимальная глубина отката назад в км
}

// RouteService реализует бизнес-логику построения и симуляции электромобильных маршрутов.
type RouteService struct {
	routeProvider route.RouteProvider
	stationFinder StationFinder
	config        Config
}

// NewService создает новый экземпляр RouteService.
func NewService(routeProvider route.RouteProvider, stationFinder StationFinder, c Config) (*RouteService, error) {
	if c.TargetChargePercent <= c.CriticalChargePercent {
		return nil, errors.New("target charge must be above critical charge")
	}
	if c.TargetChargePercent <= 0 || c.TargetChargePercent >= 100 {
		return nil, errors.New("target charge must be in (0, 100)")
	}
	if c.MinArrivalPercent >= c.CriticalChargePercent {
		return nil, errors.New("min arrival must be below critical charge")
	}
	if c.MaxStops <= 0 || c.MaxStationCandidates <= 0 {
		return nil, errors.New("max stops and candidates must be positive")
	}
	return &RouteService{
		routeProvider: routeProvider,
		stationFinder: stationFinder,
		config:        c,
	}, nil
}

// PointAnalysis хранит детальный отчет о состоянии машины в конкретной точке маршрута.
type PointAnalysis struct {
	Point                   route.RoutePoint
	CumulativeDistanceKm    float64 // Пройденное расстояние от начала текущего лега
	RemainingBatteryKwh     float64 // Остаток энергии в кВт·ч
	RemainingBatteryPercent float64 // Остаток энергии в процентах
	IsCritical              bool    // Флаг: уровень заряда упал ниже критического
}

// RouteAnalysis содержит результаты симуляции для одного непрерывного отрезка пути (лега).
type RouteAnalysis struct {
	Points               []PointAnalysis
	TotalDistanceKm      float64
	ReachesDestination   bool  // Флаг: удалось ли доехать до конца отрезка без остановок
	CriticalPointIndexes []int // Индексы точек в Points, где заряд упал до критического уровня
}

// last возвращает последнюю проанализированную точку текущего отрезка.
func (a *RouteAnalysis) last() PointAnalysis {
	return a.Points[len(a.Points)-1]
}

// ChargeStop описывает информацию об остановке на зарядной станции.
type ChargeStop struct {
	Station        station.ChargingStation
	ArrivalPercent float64 // Процент заряда при прибытии на станцию
	DepartPercent  float64 // Процент заряда при отправлении со станции
}

// RoutePlan представляет итоговый план поездки, включая все отрезки пути и запланированные остановки.
type RoutePlan struct {
	Legs            []*RouteAnalysis
	Stops           []ChargeStop
	TotalDistanceKm float64
}

// addLeg добавляет очередной сегмент пути в общий план поездки.
func (p *RoutePlan) addLeg(a *RouteAnalysis) {
	p.Legs = append(p.Legs, a)
	p.TotalDistanceKm += a.TotalDistanceKm
}

// PlanRoute координирует построение сложного маршрута с учетом промежуточных зарядок.
// Работает итеративно: если заряда не хватает до конца, находит оптимальную станцию,
// добавляет остановку и продолжает расчет оставшегося пути.
func (s *RouteService) PlanRoute(ctx context.Context, from, to route.RoutePoint, ev evmodel.EVModel, startPercent float64) (*RoutePlan, error) {
	plan := &RoutePlan{}
	current, charge := from, startPercent

	used := make(map[int64]struct{})

	for {
		// 1. Симулируем поездку из текущей точки до конечной
		analysis, err := s.analyzeRoute(ctx, current, to, ev, charge)
		if err != nil {
			return nil, err
		}

		// 2. Если заряд позволяет доехать до конца без остановок - завершаем построение
		if analysis.ReachesDestination {
			plan.addLeg(analysis)
			return plan, nil
		}

		// 3. Защита от слишком длинных/невыполнимых маршрутов с бесконечным числом остановок
		if len(plan.Stops) >= s.config.MaxStops {
			return plan, ErrTooManyStops
		}

		// 4. Если доехать нельзя, ищем достижимую зарядную станцию с помощью стратегии отката
		st, leg, err := s.findReachableStation(ctx, current, analysis, ev, charge, used)
		if err != nil {
			return plan, err
		}
		used[st.ID] = struct{}{}

		// 5. Фиксируем отрезок пути до станции и саму остановку
		plan.addLeg(leg)
		plan.Stops = append(plan.Stops, ChargeStop{
			Station:        st,
			ArrivalPercent: leg.last().RemainingBatteryPercent,
			DepartPercent:  s.config.TargetChargePercent,
		})

		// 6. Смещаем текущую позицию на найденную станцию и обновляем заряд после подзарядки
		current = route.RoutePoint{
			Lat: st.Latitude,
			Lon: st.Longitude,
		}
		charge = s.config.TargetChargePercent
	}
}

// analyzeRoute выполняет посегментную физическую симуляцию расхода энергии электромобиля.
// Вычисляет остаток заряда в каждой точке и выявляет критические участки, где требуется подзарядка.
func (s *RouteService) analyzeRoute(ctx context.Context, from, to route.RoutePoint, evModel evmodel.EVModel, currentBatteryPercent float64) (*RouteAnalysis, error) {
	if evModel.BatteryCapacityKwh <= 0 {
		return nil, fmt.Errorf("invalid battery capacity: %v", evModel.BatteryCapacityKwh)
	}
	if currentBatteryPercent < 0 || currentBatteryPercent > 100 {
		return nil, fmt.Errorf("invalid battery percent: %v", currentBatteryPercent)
	}

	segments, err := s.routeProvider.BuildRoute(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to build route: %w", err)
	}

	analysis := &RouteAnalysis{
		Points:               make([]PointAnalysis, 0, len(segments)+1),
		ReachesDestination:   true,
		CriticalPointIndexes: make([]int, 0),
	}

	toPercent := func(kwh float64) float64 {
		return (kwh / evModel.BatteryCapacityKwh) * 100
	}

	remainingKwh := (currentBatteryPercent / 100) * evModel.BatteryCapacityKwh
	totalKm := 0.0

	// addPoint фиксирует состояние машины в конце каждого сегмента пути
	addPoint := func(p route.RoutePoint) {
		percent := toPercent(remainingKwh)
		isCritical := percent <= s.config.CriticalChargePercent

		if isCritical && analysis.ReachesDestination {
			analysis.ReachesDestination = false
			analysis.CriticalPointIndexes = append(analysis.CriticalPointIndexes, len(analysis.Points))
		}

		analysis.Points = append(analysis.Points, PointAnalysis{
			Point:                   p,
			CumulativeDistanceKm:    totalKm,
			RemainingBatteryKwh:     remainingKwh,
			RemainingBatteryPercent: percent,
			IsCritical:              isCritical,
		})
	}

	// Точка 0 - начальная позиция
	addPoint(from)

	// Симуляция движения по каждому сегменту маршрута
	for _, seg := range segments {
		segKm := seg.DistanceMeters / 1000.0
		consumptionKwh := (segKm / 100.0) * evModel.AvgConsumptionKwh100km

		remainingKwh = math.Max(0, remainingKwh-consumptionKwh)
		totalKm += segKm

		addPoint(seg.To)
	}

	analysis.TotalDistanceKm = totalKm
	return analysis, nil
}

// findReachableStation реализует стратегию "Lookback" (откат назад от критической точки).
// Если впереди батарея разряжается, метод идет назад по точкам маршрута, ищет станции в радиусе
// и проверяет физическую достижимость каждой из них с учетом минимального безопасного остатка.
func (s *RouteService) findReachableStation(ctx context.Context, current route.RoutePoint, analysis *RouteAnalysis, ev evmodel.EVModel, charge float64, used map[int64]struct{}) (station.ChargingStation, *RouteAnalysis, error) {

	critIdx := analysis.CriticalPointIndexes[0]
	critKm := analysis.Points[critIdx].CumulativeDistanceKm

	tried := maps.Clone(used)
	foundAny := false
	lastSearchKm := math.Inf(1)

	// Идем назад от критической точки к началу маршрута
	for i := critIdx; i >= 0; i-- {
		p := analysis.Points[i]

		// Если отошли назад дальше допустимого лимита - прекращаем поиск
		if critKm-p.CumulativeDistanceKm > s.config.MaxLookbackKm {
			break
		}
		// Пропускаем точки, которые слишком близко друг к другу (соблюдаем шаг LookbackStepKm)
		if lastSearchKm-p.CumulativeDistanceKm < s.config.LookbackStepKm {
			continue
		}
		lastSearchKm = p.CumulativeDistanceKm

		// Ищем доступные станции в окрестности текущей точки отката
		found, err := s.stationFinder.GetAvailableNearbyStations(ctx, p.Point.Lat, p.Point.Lon, s.config.SearchRadiusMeters, []string{ev.ConnectorType})
		if err != nil {
			return station.ChargingStation{}, nil, fmt.Errorf("find stations: %w", err)
		}

		// Фильтруем станции, которые уже пробовали на предыдущих итерациях
		fresh := make([]station.ChargingStation, 0, len(found))
		for _, c := range found {
			if _, seen := tried[c.ID]; !seen {
				fresh = append(fresh, c)
			}
		}
		if len(fresh) > s.config.MaxStationCandidates {
			fresh = fresh[:s.config.MaxStationCandidates]
		}
		if len(fresh) > 0 {
			foundAny = true
		}

		// Проверяем каждого кандидата на физическую достижимость с текущим зарядом
		for _, c := range fresh {
			tried[c.ID] = struct{}{}

			// Строим подмаршрут до станции и симулируем поездку к ней
			leg, err := s.analyzeRoute(ctx, current, route.RoutePoint{Lat: c.Latitude, Lon: c.Longitude}, ev, charge)
			if err != nil {
				return station.ChargingStation{}, nil, err
			}
			// Если доехали и остаток заряда выше или равен минимальному порогу приезда - станция найдена!
			if leg.last().RemainingBatteryPercent >= s.config.MinArrivalPercent {
				return c, leg, nil
			}
		}
	}

	// Обработка тупиковых ситуаций
	if foundAny {
		return station.ChargingStation{}, nil, ErrStationUnreachable // Станции есть, но до них не доехать
	}
	return station.ChargingStation{}, nil, ErrNoStationNearby // Вблизи критической зоны вообще нет станций
}
