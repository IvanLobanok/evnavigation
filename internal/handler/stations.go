package handler

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/IvanLobanok/evnavigation/internal/models/station"
)

const (
	defaultRadiusKm = 5.0
	maxRadiusKm     = 50.0
	defaultLimit    = 20
	maxLimit        = 100

	earthRadiusMeters = 6371008.8
)

// StationFinder ищет доступные зарядные станции вокруг точки (реализует station.Service).
// Станции приходят отсортированными по расстоянию от точки.
type StationFinder interface {
	GetAvailableNearbyStations(ctx context.Context, lat, lon, radiusMeters float64, preferredConnectors []string) ([]station.ChargingStation, error)
}

type StationHandler struct {
	finder StationFinder
	log    *slog.Logger
}

func NewStationHandler(finder StationFinder, log *slog.Logger) *StationHandler {
	if log == nil {
		log = slog.Default()
	}
	return &StationHandler{finder: finder, log: log}
}

type nearbyStationDTO struct {
	stationDTO
	DistanceM int `json:"distance_m"` // приблизительно: по сфере, от точки запроса
}

type nearbyResponse struct {
	Stations  []nearbyStationDTO `json:"stations"`
	RadiusKm  float64            `json:"radius_km"`
	Truncated bool               `json:"truncated"` // true, если нашлось больше станций, чем влезло в limit
}

type nearbyQuery struct {
	lat, lon   float64
	radiusKm   float64
	limit      int
	connectors []string
}

// Nearby — GET /stations/nearby?lat=&lon=[&radius_km=5][&limit=20][&connector=CCS+Combo+2...]
func (h *StationHandler) Nearby(w http.ResponseWriter, r *http.Request) {
	q, details := parseNearbyQuery(r)
	if details != nil {
		writeValidationError(w, details)
		return
	}

	found, err := h.finder.GetAvailableNearbyStations(r.Context(), q.lat, q.lon, q.radiusKm*1000, q.connectors)
	if err != nil {
		internalError(w, r, h.log, "find nearby stations", err)
		return
	}

	resp := nearbyResponse{
		Stations:  make([]nearbyStationDTO, 0, min(len(found), q.limit)),
		RadiusKm:  q.radiusKm,
		Truncated: len(found) > q.limit,
	}
	for i, s := range found {
		if i == q.limit {
			break
		}
		resp.Stations = append(resp.Stations, nearbyStationDTO{
			stationDTO: stationDTO{
				ID: s.ID, Name: s.Name, Lat: s.Latitude, Lon: s.Longitude,
				PowerKW: s.PowerKW, ConnectorTypes: nonNil(s.ConnectorTypes),
			},
			DistanceM: int(math.Round(distanceMeters(q.lat, q.lon, s.Latitude, s.Longitude))),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func parseNearbyQuery(r *http.Request) (nearbyQuery, map[string]string) {
	values := r.URL.Query()
	details := map[string]string{}
	q := nearbyQuery{radiusKm: defaultRadiusKm, limit: defaultLimit}

	q.lat = requiredFloat(values.Get("lat"), "lat", -90, 90, details)
	q.lon = requiredFloat(values.Get("lon"), "lon", -180, 180, details)

	if raw := values.Get("radius_km"); raw != "" {
		v, err := parseFinite(raw)
		if err != nil || v <= 0 || v > maxRadiusKm {
			details["radius_km"] = fmt.Sprintf("must be a number greater than 0 and at most %g", maxRadiusKm)
		} else {
			q.radiusKm = v
		}
	}

	if raw := values.Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > maxLimit {
			details["limit"] = fmt.Sprintf("must be an integer between 1 and %d", maxLimit)
		} else {
			q.limit = v
		}
	}

	for _, c := range values["connector"] {
		if c != "" {
			q.connectors = append(q.connectors, c)
		}
	}

	if len(details) > 0 {
		return nearbyQuery{}, details
	}
	return q, nil
}

func requiredFloat(raw, name string, lo, hi float64, details map[string]string) float64 {
	v, err := parseFinite(raw)
	if raw == "" || err != nil || v < lo || v > hi {
		details[name] = fmt.Sprintf("is required and must be a number between %g and %g", lo, hi)
		return 0
	}
	return v
}

// parseFinite отвергает NaN и ±Inf: strconv.ParseFloat принимает строки "nan" и "inf".
func parseFinite(raw string) (float64, error) {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("not a finite number: %q", raw)
	}
	return v, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// distanceMeters — расстояние по большому кругу (формула гаверсинусов).
func distanceMeters(lat1, lon1, lat2, lon2 float64) float64 {
	toRad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusMeters * math.Asin(math.Min(1, math.Sqrt(a)))
}
