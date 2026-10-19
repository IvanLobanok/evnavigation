package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/config"
	"github.com/IvanLobanok/evnavigation/internal/handler"
	"github.com/IvanLobanok/evnavigation/internal/migrate"
	"github.com/IvanLobanok/evnavigation/internal/provider/mock"
	"github.com/IvanLobanok/evnavigation/internal/repository/postgres"
	"github.com/IvanLobanok/evnavigation/internal/service/auth"
	"github.com/IvanLobanok/evnavigation/internal/service/routing"
	stationsvc "github.com/IvanLobanok/evnavigation/internal/service/station"
	"github.com/IvanLobanok/evnavigation/internal/service/vehicle"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dbPool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to create database pool: %v", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		log.Fatalf("failed to ping database: %v", err)
	}
	log.Println("successfully connected to PostgreSQL")

	migratorURL := strings.Replace(cfg.DatabaseURL, "postgres://", "pgx5://", 1)

	if err := migrate.RunMigrations(migratorURL, "migrations"); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
	log.Println("migrations applied successfully")

	repo := postgres.NewRepository(dbPool)
	stationService := stationsvc.NewService(repo)
	vehicleService := vehicle.NewService(repo, repo)

	authService, err := auth.NewService(repo, auth.Config{
		Secret: []byte(cfg.JWTSecret),
		TTL:    cfg.JWTTTL,
	})
	if err != nil {
		log.Fatalf("failed to create auth service: %v", err)
	}

	// пока MockProvider; когда появится ключ Яндекса, заменится на YandexProvider
	routingService, err := routing.NewService(&mock.MockProvider{}, stationService, cfg.Routing)
	if err != nil {
		log.Fatalf("failed to create routing service: %v", err)
	}

	// сервисы -> хендлеры -> роутер
	logger := slog.Default()
	router := handler.NewRouter(handler.Handlers{
		Auth:     handler.NewAuthHandler(authService, logger),
		Vehicles: handler.NewVehicleHandler(vehicleService, logger),
		Routes:   handler.NewRoutesHandler(routingService, vehicleService, logger),
		Tokens:   authService,
	})

	srv := &http.Server{
		Addr:              ":" + cfg.ServerPort,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      35 * time.Second, // больше, чем таймаут запроса в роутере (30 с)
		IdleTimeout:       60 * time.Second,
	}

	// graceful shutdown: по SIGINT/SIGTERM даём текущим запросам дозавершиться
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("evnavigation backend listening on %s", srv.Addr)
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		log.Fatalf("server failed: %v", err)
	case <-sigCtx.Done():
		log.Println("shutting down...")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}
