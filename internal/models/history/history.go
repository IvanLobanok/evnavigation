package history

import (
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/route"
)

// Итог расчёта маршрута. Значения совпадают с error.code в ответах API.
const (
	StatusOK                 = "ok"
	StatusNoStationNearby    = "no_station_nearby"
	StatusStationUnreachable = "station_unreachable"
	StatusTooManyStops       = "too_many_stops"
	StatusError              = "error" // внутренний сбой: провайдер маршрутов, БД и т.п.
)

// RouteRequest — один запрос на построение маршрута и чем он закончился.
type RouteRequest struct {
	ID                   int
	UserID               int
	VehicleID            int
	From                 route.RoutePoint
	To                   route.RoutePoint
	InitialChargePercent float64
	Status               string
	StopsCount           int
	TotalDistanceKm      *float64 // nil, если маршрут не построен
	CreatedAt            time.Time
}
