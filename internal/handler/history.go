package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/history"
	svchistory "github.com/IvanLobanok/evnavigation/internal/service/history"
)

// HistoryLister отдаёт историю запросов маршрута (реализует history.Service).
type HistoryLister interface {
	List(ctx context.Context, userID, limit int, beforeID int64) (items []history.RouteRequest, hasMore bool, err error)
}

type HistoryHandler struct {
	svc HistoryLister
	log *slog.Logger
}

func NewHistoryHandler(svc HistoryLister, log *slog.Logger) *HistoryHandler {
	if log == nil {
		log = slog.Default()
	}
	return &HistoryHandler{svc: svc, log: log}
}

type latLonDTO struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type historyItemDTO struct {
	ID                   int       `json:"id"`
	VehicleID            int       `json:"vehicle_id"`
	From                 latLonDTO `json:"from"`
	To                   latLonDTO `json:"to"`
	InitialChargePercent float64   `json:"initial_charge_percent"`
	Status               string    `json:"status"`
	StopsCount           int       `json:"stops_count"`
	TotalDistanceKm      *float64  `json:"total_distance_km"` // null, если маршрут не построен
	CreatedAt            time.Time `json:"created_at"`
}

type historyResponse struct {
	Requests []historyItemDTO `json:"requests"`
	// NextBeforeID передаётся как before_id в следующем запросе; null — это последняя страница
	NextBeforeID *int `json:"next_before_id"`
}

// List — GET /routes/history[?limit=20][&before_id=N]: свои запросы маршрутов, новые первыми.
func (h *HistoryHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	limit, beforeID, details := parseHistoryQuery(r)
	if details != nil {
		writeValidationError(w, details)
		return
	}

	items, hasMore, err := h.svc.List(r.Context(), userID, limit, beforeID)
	if err != nil {
		internalError(w, r, h.log, "list route history", err)
		return
	}

	resp := historyResponse{Requests: make([]historyItemDTO, 0, len(items))}
	for _, it := range items {
		resp.Requests = append(resp.Requests, historyItemDTO{
			ID:                   it.ID,
			VehicleID:            it.VehicleID,
			From:                 latLonDTO{Lat: it.From.Lat, Lon: it.From.Lon},
			To:                   latLonDTO{Lat: it.To.Lat, Lon: it.To.Lon},
			InitialChargePercent: it.InitialChargePercent,
			Status:               it.Status,
			StopsCount:           it.StopsCount,
			TotalDistanceKm:      it.TotalDistanceKm,
			CreatedAt:            it.CreatedAt,
		})
	}
	if hasMore && len(items) > 0 {
		next := items[len(items)-1].ID
		resp.NextBeforeID = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

func parseHistoryQuery(r *http.Request) (limit int, beforeID int64, details map[string]string) {
	values := r.URL.Query()
	details = map[string]string{}
	limit = svchistory.DefaultLimit

	if raw := values.Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > svchistory.MaxLimit {
			details["limit"] = fmt.Sprintf("must be an integer between 1 and %d", svchistory.MaxLimit)
		} else {
			limit = v
		}
	}
	if raw := values.Get("before_id"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 1 {
			details["before_id"] = "must be a positive integer"
		} else {
			beforeID = v
		}
	}

	if len(details) > 0 {
		return 0, 0, details
	}
	return limit, beforeID, nil
}
