package routing

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"sync"

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
// Возвращает работающие станции с хотя бы одним из preferredConnectors, ближайшие первыми, не больше limit штук.
type StationFinder interface {
	GetAvailableNearbyStations(ctx context.Context, lat, lon, radiusMeters float64, preferredConnectors []string, limit int) ([]station.ChargingStation, error)
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

	// CandidateParallelism: сколько станций-кандидатов проверять одновременно (каждая проверка - вызов провайдера маршрутов).
	// 0 и 1 - по одной, как раньше: меньше вызовов платного провайдера, но дольше ждать.
	// Больше 1 - быстрее ответ, но кандидаты шага отката проверяются сразу все, а не до первой удачной: вызовов провайдера
	// становится больше (часть из них отменяется, если ближайшая станция успела ответить раньше).
	// Результат от значения не зависит: побеждает первый по порядку (ближайший) достижимый кандидат.
	CandidateParallelism int
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
	if c.CandidateParallelism < 0 {
		return nil, errors.New("candidate parallelism must not be negative")
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

// straightLineSlack: нижняя оценка длины пути до станции - это 97% расстояния по прямой.
// Дорога не бывает короче прямой, а 3% запаса прощают провайдерам и тестовым данным с чуть иным радиусом Земли.
const straightLineSlack = 0.97

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

		// Ищем доступные станции в окрестности текущей точки отката. Фильтр по разъёму и ограничение числа строк
		// выполняет база: уже опробованные станции (tried) отсеиваются здесь, поэтому просим MaxStationCandidates + len(tried) -
		// даже если все опробованные окажутся ближе всех, свежих кандидатов хватит.
		limit := s.config.MaxStationCandidates + len(tried)
		found, err := s.stationFinder.GetAvailableNearbyStations(ctx, p.Point.Lat, p.Point.Lon, s.config.SearchRadiusMeters, []string{ev.ConnectorType}, limit)
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
		for _, c := range fresh {
			tried[c.ID] = struct{}{}
		}

		// Проверяем кандидатов на физическую достижимость с текущим зарядом
		c, leg, ok, err := s.firstReachable(ctx, current, fresh, ev, charge)
		if err != nil {
			return station.ChargingStation{}, nil, err
		}
		if ok {
			return c, leg, nil
		}
	}

	// Обработка тупиковых ситуаций
	if foundAny {
		return station.ChargingStation{}, nil, ErrStationUnreachable // Станции есть, но до них не доехать
	}
	return station.ChargingStation{}, nil, ErrNoStationNearby // Вблизи критической зоны вообще нет станций
}

// candidateResult - итог проверки одного кандидата.
type candidateResult struct {
	leg *RouteAnalysis
	err error
}

// firstReachable возвращает первую по порядку станцию, до которой хватает заряда, вместе с участком пути к ней.
// ok == false без ошибки значит, что ни одна из станций недостижима.
//
// Сначала отсеиваются станции, до которых не доехать даже по прямой: для них не нужен вызов провайдера маршрутов.
// Остальные проверяются по одной или параллельно (CandidateParallelism); выбор от этого не зависит.
func (s *RouteService) firstReachable(ctx context.Context, current route.RoutePoint, candidates []station.ChargingStation, ev evmodel.EVModel, charge float64) (st station.ChargingStation, leg *RouteAnalysis, ok bool, err error) {
	viable := make([]station.ChargingStation, 0, len(candidates))
	for _, c := range candidates {
		if !s.hopeless(current, route.RoutePoint{Lat: c.Latitude, Lon: c.Longitude}, ev, charge) {
			viable = append(viable, c)
		}
	}
	if len(viable) == 0 {
		return station.ChargingStation{}, nil, false, nil
	}

	// Строим подмаршрут до станции и симулируем поездку к ней.
	check := func(ctx context.Context, c station.ChargingStation) (*RouteAnalysis, error) {
		return s.analyzeRoute(ctx, current, route.RoutePoint{Lat: c.Latitude, Lon: c.Longitude}, ev, charge)
	}
	// Если доехали и остаток заряда выше или равен минимальному порогу приезда - станция найдена!
	reached := func(a *RouteAnalysis) bool {
		return a.last().RemainingBatteryPercent >= s.config.MinArrivalPercent
	}

	if s.config.CandidateParallelism <= 1 || len(viable) == 1 {
		for _, c := range viable {
			a, err := check(ctx, c)
			if err != nil {
				return station.ChargingStation{}, nil, false, err
			}
			if reached(a) {
				return c, a, true, nil
			}
		}
		return station.ChargingStation{}, nil, false, nil
	}

	// Параллельная проверка. Каждый кандидат получает свой контекст: когда найден достижимый кандидат с индексом i,
	// кандидаты правее него уже не могут победить, и их проверки отменяются, а кандидаты левее продолжают -
	// среди них может найтись более близкая станция, которая должна победить, как и при последовательной проверке.
	results := make([]candidateResult, len(viable))
	ctxs := make([]context.Context, len(viable))
	cancels := make([]context.CancelFunc, len(viable))
	for i := range viable {
		ctxs[i], cancels[i] = context.WithCancel(ctx)
	}
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()

	var (
		mu   sync.Mutex
		best = len(viable) // наименьший индекс кандидата, про которого уже известно, что он достижим
		sem  = make(chan struct{}, s.config.CandidateParallelism)
		wg   sync.WaitGroup
	)
	for i := range viable {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Паника в горутине не попадает под chi Recoverer и уронила бы весь процесс, поэтому превращаем её в ошибку.
			defer func() {
				if r := recover(); r != nil {
					results[i] = candidateResult{err: fmt.Errorf("check station %d: panic: %v", viable[i].ID, r)}
				}
			}()

			sem <- struct{}{}
			defer func() { <-sem }()

			mu.Lock()
			superseded := i > best
			mu.Unlock()
			if superseded || ctxs[i].Err() != nil {
				err := ctxs[i].Err()
				if err == nil {
					err = context.Canceled
				}
				results[i] = candidateResult{err: err}
				return
			}

			a, err := check(ctxs[i], viable[i])
			results[i] = candidateResult{leg: a, err: err}
			if err == nil && reached(a) {
				mu.Lock()
				if i < best {
					best = i
					for j := i + 1; j < len(viable); j++ {
						cancels[j]()
					}
				}
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	// Читаем результаты в порядке кандидатов - ровно так же, как делала последовательная проверка.
	// Отменённые нами кандидаты стоят правее победителя, до них дело не доходит.
	for i, c := range viable {
		res := results[i]
		if res.err != nil {
			return station.ChargingStation{}, nil, false, res.err
		}
		if res.leg == nil {
			return station.ChargingStation{}, nil, false, fmt.Errorf("check station %d: no result", c.ID)
		}
		if reached(res.leg) {
			return c, res.leg, true, nil
		}
	}
	return station.ChargingStation{}, nil, false, nil
}

// hopeless сообщает, что до точки to заведомо не доехать с зарядом charge: даже по прямой (кратчайший из возможных путей)
// остаток при прибытии окажется ниже MinArrivalPercent. Это отсев без обращения к провайдеру маршрутов.
// Данные, которые не прошли бы проверку в analyzeRoute, не отсеиваются: пусть ошибку вернёт он.
func (s *RouteService) hopeless(from, to route.RoutePoint, ev evmodel.EVModel, charge float64) bool {
	if ev.BatteryCapacityKwh <= 0 || charge < 0 || charge > 100 {
		return false
	}
	minKm := greatCircleKm(from, to) * straightLineSlack
	minUsedPercent := (minKm / 100) * ev.AvgConsumptionKwh100km / ev.BatteryCapacityKwh * 100
	return charge-minUsedPercent < s.config.MinArrivalPercent
}

// greatCircleKm - расстояние по большому кругу между точками (формула гаверсинусов).
func greatCircleKm(a, b route.RoutePoint) float64 {
	const earthRadiusKm = 6371.0
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := rad(b.Lat - a.Lat)
	dLon := rad(b.Lon - a.Lon)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(a.Lat))*math.Cos(rad(b.Lat))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusKm * math.Asin(math.Min(1, math.Sqrt(h)))
}
