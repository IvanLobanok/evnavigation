package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/evmodel"
	"github.com/IvanLobanok/evnavigation/internal/models/user"
	"github.com/IvanLobanok/evnavigation/internal/service/vehicle"
	"github.com/go-chi/chi/v5"
)

// VehicleService - каталог моделей и машины пользователя (реализует vehicle.Service).
type VehicleService interface {
	ListModels(ctx context.Context) ([]evmodel.EVModel, error)
	AddVehicle(ctx context.Context, userID, evModelID int, nickname string) (*user.VehicleDetails, error)
	GetVehicle(ctx context.Context, userID, vehicleID int) (*user.VehicleDetails, error)
	ListVehicles(ctx context.Context, userID int) ([]user.VehicleDetails, error)
}

type VehicleHandler struct {
	svc VehicleService
	log *slog.Logger
}

func NewVehicleHandler(svc VehicleService, log *slog.Logger) *VehicleHandler {
	if log == nil {
		log = slog.Default()
	}
	return &VehicleHandler{svc: svc, log: log}
}

// ---------------------------------------------------------------------------
// DTO
// ---------------------------------------------------------------------------

type evModelDTO struct {
	ID                     int     `json:"id"`
	Brand                  string  `json:"brand"`
	Model                  string  `json:"model"`
	BatteryCapacityKwh     float64 `json:"battery_capacity_kwh"`
	AvgConsumptionKwh100km float64 `json:"avg_consumption_kwh_100km"`
	ConnectorType          string  `json:"connector_type"`
	MaxChargingPowerKw     float64 `json:"max_charging_power_kw"`
}

type vehicleDTO struct {
	ID        int        `json:"id"`
	Nickname  *string    `json:"nickname"`
	CreatedAt time.Time  `json:"created_at"`
	Model     evModelDTO `json:"model"`
}

func toModelDTO(m evmodel.EVModel) evModelDTO {
	return evModelDTO{
		ID: m.ID, Brand: m.Brand, Model: m.Model,
		BatteryCapacityKwh:     m.BatteryCapacityKwh,
		AvgConsumptionKwh100km: m.AvgConsumptionKwh100km,
		ConnectorType:          m.ConnectorType,
		MaxChargingPowerKw:     m.MaxChargingPowerKw,
	}
}

func toVehicleDTO(d user.VehicleDetails) vehicleDTO {
	return vehicleDTO{ID: d.ID, Nickname: d.Nickname, CreatedAt: d.CreatedAt, Model: toModelDTO(d.EVModel)}
}

type createVehicleRequest struct {
	EVModelID int     `json:"ev_model_id"`
	Nickname  *string `json:"nickname"`
}

// ---------------------------------------------------------------------------
// GET /ev-models (публичный)
// ---------------------------------------------------------------------------

func (h *VehicleHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.svc.ListModels(r.Context())
	if err != nil {
		internalError(w, r, h.log, "list ev models", err)
		return
	}
	out := make([]evModelDTO, 0, len(models))
	for _, m := range models {
		out = append(out, toModelDTO(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out})
}

// ---------------------------------------------------------------------------
// POST /vehicles
// ---------------------------------------------------------------------------

func (h *VehicleHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req createVehicleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.EVModelID <= 0 {
		writeValidationError(w, map[string]string{"ev_model_id": "is required and must be a positive integer"})
		return
	}
	nickname := ""
	if req.Nickname != nil {
		nickname = *req.Nickname
	}

	d, err := h.svc.AddVehicle(r.Context(), userID, req.EVModelID, nickname)
	switch {
	case errors.Is(err, vehicle.ErrInvalidNickname):
		writeValidationError(w, map[string]string{
			"nickname": fmt.Sprintf("must be at most %d characters", vehicle.MaxNicknameLen),
		})
		return
	case errors.Is(err, evmodel.ErrNotFound):
		writeError(w, http.StatusNotFound, "ev_model_not_found", "ev model with the given id does not exist")
		return
	case err != nil:
		internalError(w, r, h.log, "add vehicle", err)
		return
	}

	w.Header().Set("Location", "/vehicles/"+strconv.Itoa(d.ID))
	writeJSON(w, http.StatusCreated, toVehicleDTO(*d))
}

// ---------------------------------------------------------------------------
// GET /vehicles и GET /vehicles/{id}
// ---------------------------------------------------------------------------

func (h *VehicleHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	list, err := h.svc.ListVehicles(r.Context(), userID)
	if err != nil {
		internalError(w, r, h.log, "list vehicles", err)
		return
	}
	out := make([]vehicleDTO, 0, len(list))
	for _, d := range list {
		out = append(out, toVehicleDTO(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"vehicles": out})
}

func (h *VehicleHandler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_id", "vehicle id must be a positive integer")
		return
	}

	d, err := h.svc.GetVehicle(r.Context(), userID, id)
	switch {
	case errors.Is(err, user.ErrVehicleNotFound):
		// чужая и несуществующая машины выглядят одинаково
		writeError(w, http.StatusNotFound, "vehicle_not_found", "vehicle not found")
		return
	case err != nil:
		internalError(w, r, h.log, "get vehicle", err)
		return
	}
	writeJSON(w, http.StatusOK, toVehicleDTO(*d))
}

// requireUser достаёт id пользователя из контекста. Без middleware Authenticate его там нет:
// это ошибка конфигурации роутера, и отвечать на неё нужно закрыто (401), а не пускать анонима.
func requireUser(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, ok := UserIDFromContext(r.Context())
	if !ok {
		unauthorized(w)
	}
	return id, ok
}
