package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/IvanLobanok/evnavigation/internal/service/routing"
)

type Config struct {
	AppEnv      string
	ServerPort  string
	DatabaseURL string
	JWTSecret   string

	Routing routing.Config
}

type envReader struct {
	errs []error
}

func Load() (*Config, error) {
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "evnav_dev_pass")
	dbName := getEnv("DB_NAME", "evnavigation")
	dbSSLMode := getEnv("DB_SSLMODE", "disable")

	databaseURL := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s",
		dbUser, dbPassword, dbHost, dbPort, dbName, dbSSLMode,
	)

	var r envReader

	cfg := &Config{
		AppEnv:      getEnv("APP_ENV", "development"),
		ServerPort:  getEnv("SERVER_PORT", "8080"),
		DatabaseURL: databaseURL,
		JWTSecret:   getEnv("JWT_SECRET", "dev_secret_change_me"),
		Routing: routing.Config{
			CriticalChargePercent: r.float("CRITICAL_CHARGE_PERCENT", 15),
			MinArrivalPercent:     r.float("MIN_ARRIVAL_PERCENT", 5),
			TargetChargePercent:   r.float("TARGET_CHARGE_PERCENT", 80),
			SearchRadiusMeters:    r.float("STATION_SEARCH_RADIUS_METERS", 5000),
			MaxStops:              r.int("MAX_CHARGING_STOPS", 5),
			MaxStationCandidates:  r.int("MAX_STATION_CANDIDATES", 3),
			LookbackStepKm:        r.float("LOOKBACK_STEP_KM", 5),
			MaxLookbackKm:         r.float("MAX_LOOKBACK_KM", 30),
		},
	}
	if err := r.err(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

func (r *envReader) float(key string, fallback float64) float64 {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}

	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s=%q: not a number", key, raw))
		return fallback
	}
	return v
}

func (r *envReader) int(key string, fallback int) int {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}

	v, err := strconv.Atoi(raw)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s=%q: not an integer", key, raw))
		return fallback
	}
	return v
}

func (r *envReader) err() error {
	return errors.Join(r.errs...)
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
