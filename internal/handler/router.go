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

	// Limits - ограничения частоты запросов; нулевое значение выключает их.
	Limits RateLimits
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

	authLimit := limitByIP(h.Limits.Auth, h.Limits.TrustProxyHeaders)
	publicLimit := limitByIP(h.Limits.Public, h.Limits.TrustProxyHeaders)
	r.With(authLimit).Post("/auth/register", h.Auth.Register)
	r.With(authLimit).Post("/auth/login", h.Auth.Login)
	r.With(publicLimit).Get("/ev-models", h.Vehicles.ListModels)
	r.With(publicLimit).Get("/stations/nearby", h.Stations.Nearby)

	// эндпоинты, требующие JWT
	r.Group(func(r chi.Router) {
		r.Use(Authenticate(h.Tokens))

		r.Post("/vehicles", h.Vehicles.Create)
		r.Get("/vehicles", h.Vehicles.List)
		r.Get("/vehicles/{id}", h.Vehicles.Get)

		r.With(limitByUser(h.Limits.Routes)).Post("/routes", h.Routes.PlanRoute)
		r.Get("/routes/history", h.History.List)
	})

	return r
}
