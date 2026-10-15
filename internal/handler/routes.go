package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/IvanLobanok/evnavigation/internal/models/evmodel"
	"github.com/IvanLobanok/evnavigation/internal/models/route"
	"github.com/IvanLobanok/evnavigation/internal/service/routing"
)

const maxBodyBytes = 1 << 20

type RoutePlanner interface {
	PlanRoute(ctx context.Context, from, to route.RoutePoint, ev evmodel.EVModel, startPercent float64) (*routing.RoutePlan, error)
}

type EVModelProvider interface {
	GetEVModelByID(ctx context.Context, id int) (*evmodel.EVModel, error)
}

type RoutesHandler struct {
	planner RoutePlanner
	models  EVModelProvider
	log     *slog.Logger
}

func NewRoutesHandler(planner RoutePlanner, models EVModelProvider, log *slog.Logger) *RoutesHandler {
	if log == nil {
		log = slog.Default()
	}
	return &RoutesHandler{
		planner: planner,
		models:  models,
		log:     log,
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
	EVModelID            int           `json:"ev_model_id"`
	CurrentChargePercent *float64      `json:"current_charge_percent"`
}

type validatedRequest struct {
	from, to      route.RoutePoint
	evModelID     int
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
		evModelID: r.EVModelID,
	}

	if r.EVModelID <= 0 {
		details["ev_model_id"] = "is required and must be a positive integer"
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
	req, ok := decodeRequest(w, r)
	if !ok {
		return
	}

	v, details := req.validate()
	if details != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: errorBody{
			Code: "validation_error", Message: "request validation failed", Details: details,
		}})
		return
	}

	ev, err := h.models.GetEVModelByID(r.Context(), v.evModelID)
	switch {
	case errors.Is(err, evmodel.ErrNotFound):
		writeError(w, http.StatusNotFound, "ev_model_not_found", "ev model with the given id does not exist")
		return
	case err != nil:
		h.internalError(w, r, "get ev model", err)
		return
	}

	plan, err := h.planner.PlanRoute(r.Context(), v.from, v.to, *ev, v.chargePercent)
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

func decodeRequest(w http.ResponseWriter, r *http.Request) (planRouteRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req planRouteRequest
	err := dec.Decode(&req)
	if err == nil {
		// после объекта не должно быть ничего, кроме пробелов
		if _, extra := dec.Token(); !errors.Is(extra, io.EOF) {
			err = errors.New("unexpected data after JSON object")
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large")
			return req, false
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON or contains unknown fields")
		return req, false
	}
	return req, true
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
		h.internalError(w, r, "plan route", err)
		return
	}

	resp := errorResponse{Error: errorBody{Code: code, Message: message}}
	if plan != nil && (len(plan.Legs) > 0 || len(plan.Stops) > 0) {
		partial := toResponse(plan)
		resp.PartialPlan = &partial
	}
	writeJSON(w, http.StatusUnprocessableEntity, resp)
}

func (h *RoutesHandler) internalError(w http.ResponseWriter, r *http.Request, op string, err error) {
	h.log.Error("request failed", "op", op, "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}
