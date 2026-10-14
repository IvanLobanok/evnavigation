package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/config"
	"github.com/IvanLobanok/evnavigation/internal/migrate"
	"github.com/IvanLobanok/evnavigation/internal/repository/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg := config.Load()

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
	log.Println("successfully connected to PostgreSQL (PostGIS)")

	migratorURL := strings.Replace(cfg.DatabaseURL, "postgres://", "pgx5://", 1)

	if err := migrate.RunMigrations(migratorURL, "migrations"); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
	log.Println("migrations applied successfully")

	repo := postgres.NewRepository(dbPool)
	_ = repo

	log.Printf("evnavigation backend initialized, server port configured: %s", cfg.ServerPort)

}
