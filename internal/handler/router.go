package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const requestTimeout = 30 * time.Second

// Handlers - всё, что нужно роутеру.
type Handlers struct {
	Auth     *AuthHandler
	Vehicles *VehicleHandler
	Stations *StationHandler
	Routes   *RoutesHandler
	History  *HistoryHandler
	Tokens   TokenParser // для middleware Authenticate
}

func NewRouter(h Handlers) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(requestTimeout))

	// публичные эндпоинты
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Post("/auth/register", h.Auth.Register)
	r.Post("/auth/login", h.Auth.Login)
	r.Get("/ev-models", h.Vehicles.ListModels)
	r.Get("/stations/nearby", h.Stations.Nearby)

	// эндпоинты, требующие JWT
	r.Group(func(r chi.Router) {
		r.Use(Authenticate(h.Tokens))

		r.Post("/vehicles", h.Vehicles.Create)
		r.Get("/vehicles", h.Vehicles.List)
		r.Get("/vehicles/{id}", h.Vehicles.Get)

		r.Post("/routes", h.Routes.PlanRoute)
		r.Get("/routes/history", h.History.List)
	})

	return r
}
