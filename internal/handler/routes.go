package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/IvanLobanok/evnavigation/internal/models/evmodel"
	"github.com/IvanLobanok/evnavigation/internal/models/route"
	"github.com/IvanLobanok/evnavigation/internal/models/user"
	"github.com/IvanLobanok/evnavigation/internal/service/routing"
)

type RoutePlanner interface {
	PlanRoute(ctx context.Context, from, to route.RoutePoint, ev evmodel.EVModel, startPercent float64) (*routing.RoutePlan, error)
}

// VehicleResolver отдаёт машину пользователя вместе с моделью (реализует vehicle.Service).
// Чужая машина не находится: ownership проверяется на уровне запроса.
type VehicleResolver interface {
	GetVehicle(ctx context.Context, userID, vehicleID int) (*user.VehicleDetails, error)
}

type RoutesHandler struct {
	planner  RoutePlanner
	vehicles VehicleResolver
	log      *slog.Logger
}

func NewRoutesHandler(planner RoutePlanner, vehicles VehicleResolver, log *slog.Logger) *RoutesHandler {
	if log == nil {
		log = slog.Default()
	}
	return &RoutesHandler{
		planner:  planner,
		vehicles: vehicles,
		log:      log,
	}
}

// request

type pointRequest struct {
	Lat *float64 `json:"lat"`
	Lon *float64 `json:"lon"`
}

type planRouteRequest struct {
	From                 *pointRequest `json:"from"`
	To                   *pointRequest `json:"to"`
	VehicleID            int           `json:"vehicle_id"`
	CurrentChargePercent *float64      `json:"current_charge_percent"`
}

type validatedRequest struct {
	from, to      route.RoutePoint
	vehicleID     int
	chargePercent float64
}

func (r planRouteRequest) validate() (validatedRequest, map[string]string) {
	details := map[string]string{}

	point := func(name string, p *pointRequest) route.RoutePoint {
		if p == nil {
			details[name] = "is required"
			return route.RoutePoint{}
		}
		if p.Lat == nil || *p.Lat < -90 || *p.Lat > 90 {
			details[name+".lat"] = "is required and must be between -90 and 90"
		}
		if p.Lon == nil || *p.Lon < -180 || *p.Lon > 180 {
			details[name+".lon"] = "is required and must be between -180 and 180"
		}
		if p.Lat == nil || p.Lon == nil {
			return route.RoutePoint{}
		}
		return route.RoutePoint{Lat: *p.Lat, Lon: *p.Lon}
	}

	v := validatedRequest{
		from:      point("from", r.From),
		to:        point("to", r.To),
		vehicleID: r.VehicleID,
	}

	if r.VehicleID <= 0 {
		details["vehicle_id"] = "is required and must be a positive integer"
	}
	if r.CurrentChargePercent == nil || *r.CurrentChargePercent < 0 || *r.CurrentChargePercent > 100 {
		details["current_charge_percent"] = "is required and must be between 0 and 100"
	} else {
		v.chargePercent = *r.CurrentChargePercent
	}

	if len(details) > 0 {
		return validatedRequest{}, details
	}
	return v, nil

}

//response

type planRouteResponse struct {
	TotalDistanceKm      float64   `json:"total_distance_km"`
	ArrivalChargePercent *float64  `json:"arrival_charge_percent,omitempty"` // только для полностью построенного маршрута
	Legs                 []legDTO  `json:"legs"`
	Stops                []stopDTO `json:"stops"`
}

type legDTO struct {
	DistanceKm float64          `json:"distance_km"`
	Points     []pointChargeDTO `json:"points"`
}

type pointChargeDTO struct {
	Lat           float64 `json:"lat"`
	Lon           float64 `json:"lon"`
	DistanceKm    float64 `json:"distance_km"`    // от начала этого участка
	ChargePercent float64 `json:"charge_percent"` // прогноз остатка заряда
}

type stopDTO struct {
	AfterLeg             int        `json:"after_leg"` // индекс участка, после которого идёт остановка
	Station              stationDTO `json:"station"`
	ArrivalChargePercent float64    `json:"arrival_charge_percent"`
	DepartChargePercent  float64    `json:"depart_charge_percent"`
}

type stationDTO struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Lat            float64  `json:"lat"`
	Lon            float64  `json:"lon"`
	PowerKW        float64  `json:"power_kw"`
	ConnectorTypes []string `json:"connector_types"`
}

func toResponse(plan *routing.RoutePlan) planRouteResponse {
	resp := planRouteResponse{
		TotalDistanceKm: round2(plan.TotalDistanceKm),
		Legs:            make([]legDTO, 0, len(plan.Legs)),
		Stops:           make([]stopDTO, 0, len(plan.Stops)),
	}

	for _, leg := range plan.Legs {
		points := make([]pointChargeDTO, 0, len(leg.Points))
		for _, p := range leg.Points {
			points = append(points, pointChargeDTO{
				Lat:           p.Point.Lat,
				Lon:           p.Point.Lon,
				DistanceKm:    round2(p.CumulativeDistanceKm),
				ChargePercent: round2(p.RemainingBatteryPercent),
			})
		}
		resp.Legs = append(resp.Legs, legDTO{DistanceKm: round2(leg.TotalDistanceKm), Points: points})
	}

	for i, stop := range plan.Stops {
		resp.Stops = append(resp.Stops, stopDTO{
			AfterLeg: i,
			Station: stationDTO{
				ID:             stop.Station.ID,
				Name:           stop.Station.Name,
				Lat:            stop.Station.Latitude,
				Lon:            stop.Station.Longitude,
				PowerKW:        stop.Station.PowerKW,
				ConnectorTypes: stop.Station.ConnectorTypes,
			},
			ArrivalChargePercent: round2(stop.ArrivalPercent),
			DepartChargePercent:  round2(stop.DepartPercent),
		})
	}
	return resp
}

// post /routes

func (h *RoutesHandler) PlanRoute(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req planRouteRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	v, details := req.validate()
	if details != nil {
		writeValidationError(w, details)
		return
	}

	// машина ищется среди машин ИМЕННО этого пользователя: чужой vehicle_id даёт 404, а не данные соседа
	d, err := h.vehicles.GetVehicle(r.Context(), userID, v.vehicleID)
	switch {
	case errors.Is(err, user.ErrVehicleNotFound):
		writeError(w, http.StatusNotFound, "vehicle_not_found", "vehicle not found")
		return
	case err != nil:
		internalError(w, r, h.log, "get vehicle", err)
		return
	}

	plan, err := h.planner.PlanRoute(r.Context(), v.from, v.to, d.EVModel, v.chargePercent)
	if err != nil {
		h.writePlanError(w, r, plan, err)
		return
	}

	resp := toResponse(plan)
	if n := len(plan.Legs); n > 0 {
		arrival := round2(plan.Legs[n-1].Points[len(plan.Legs[n-1].Points)-1].RemainingBatteryPercent)
		resp.ArrivalChargePercent = &arrival
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *RoutesHandler) writePlanError(w http.ResponseWriter, r *http.Request, plan *routing.RoutePlan, err error) {
	var code, message string
	switch {
	case errors.Is(err, routing.ErrNoStationNearby):
		code, message = "no_station_nearby", "no compatible charging station found before the battery runs low"
	case errors.Is(err, routing.ErrStationUnreachable):
		code, message = "station_unreachable", "charging stations exist nearby but cannot be reached with the remaining charge"
	case errors.Is(err, routing.ErrTooManyStops):
		code, message = "too_many_stops", "the route requires more charging stops than allowed"
	default:
		internalError(w, r, h.log, "plan route", err)
		return
	}

	resp := errorResponse{Error: errorBody{Code: code, Message: message}}
	if plan != nil && (len(plan.Legs) > 0 || len(plan.Stops) > 0) {
		partial := toResponse(plan)
		resp.PartialPlan = &partial
	}
	writeJSON(w, http.StatusUnprocessableEntity, resp)
}
