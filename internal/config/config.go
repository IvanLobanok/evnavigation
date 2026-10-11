package config

import (
	"fmt"
	"os"
)

type Config struct {
	AppEnv                string
	ServerPort            string
	DatabaseURL           string
	JWTSecret             string
	CriticalChargePercent string
}

func Load() *Config {
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

	return &Config{
		AppEnv:                getEnv("APP_ENV", "development"),
		ServerPort:            getEnv("SERVER_PORT", "8080"),
		DatabaseURL:           databaseURL,
		JWTSecret:             getEnv("JWT_SECRET", "dev_secret_change_me"),
		CriticalChargePercent: getEnv("CRITICAL_CHARGE_PERCENT", "15"),
	}
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
