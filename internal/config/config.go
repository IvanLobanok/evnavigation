package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/service/routing"
)

type Config struct {
	AppEnv      string
	ServerPort  string
	DatabaseURL string
	JWTSecret   string
	JWTTTL      time.Duration

	// провайдер маршрутов: "mock" (по умолчанию) или "yandex" (пока не реализован)
	RouteProvider string
	YandexAPIKey  string

	// кэш ответов провайдера; пустой RedisURL — кэш выключен
	RedisURL      string
	RouteCacheTTL time.Duration

	Routing routing.Config

	// --- защита от перегрузки ---
	DBMaxConns                  int // размер пула соединений с PostgreSQL; 0 - по умолчанию pgx (max(4, число CPU))
	RouteProviderMaxConcurrency int // одновременных вызовов провайдера маршрутов; 0 - без ограничения
	BcryptMaxConcurrency        int // одновременных bcrypt-операций; 0 - runtime.GOMAXPROCS

	// ограничения частоты запросов; 0 запросов в минуту выключает соответствующее ограничение
	RateLimitAuthPerMin   int
	RateLimitAuthBurst    int
	RateLimitPublicPerMin int
	RateLimitPublicBurst  int
	RateLimitRoutesPerMin int
	RateLimitRoutesBurst  int
	TrustProxyHeaders     bool // IP клиента брать из X-Forwarded-For (только за своим прокси)
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
		JWTTTL:      r.duration("JWT_TTL", 24*time.Hour),

		RouteProvider: getEnv("ROUTE_PROVIDER", "mock"),
		YandexAPIKey:  getEnv("YANDEX_API_KEY", ""),
		RedisURL:      getEnv("REDIS_URL", ""),
		RouteCacheTTL: r.duration("ROUTE_CACHE_TTL", 30*time.Minute),

		Routing: routing.Config{
			CriticalChargePercent: r.float("CRITICAL_CHARGE_PERCENT", 15),
			MinArrivalPercent:     r.float("MIN_ARRIVAL_PERCENT", 5),
			TargetChargePercent:   r.float("TARGET_CHARGE_PERCENT", 80),
			SearchRadiusMeters:    r.float("STATION_SEARCH_RADIUS_METERS", 5000),
			MaxStops:              r.int("MAX_CHARGING_STOPS", 5),
			MaxStationCandidates:  r.int("MAX_STATION_CANDIDATES", 3),
			LookbackStepKm:        r.float("LOOKBACK_STEP_KM", 5),
			MaxLookbackKm:         r.float("MAX_LOOKBACK_KM", 30),
			CandidateParallelism:  r.int("ROUTE_CANDIDATE_PARALLELISM", 1),
		},

		DBMaxConns:                  r.int("DB_MAX_CONNS", 0),
		RouteProviderMaxConcurrency: r.int("ROUTE_PROVIDER_MAX_CONCURRENCY", 32),
		BcryptMaxConcurrency:        r.int("BCRYPT_MAX_CONCURRENCY", 0),

		RateLimitAuthPerMin:   r.int("RATE_LIMIT_AUTH_PER_MIN", 30),
		RateLimitAuthBurst:    r.int("RATE_LIMIT_AUTH_BURST", 10),
		RateLimitPublicPerMin: r.int("RATE_LIMIT_PUBLIC_PER_MIN", 300),
		RateLimitPublicBurst:  r.int("RATE_LIMIT_PUBLIC_BURST", 60),
		RateLimitRoutesPerMin: r.int("RATE_LIMIT_ROUTES_PER_MIN", 60),
		RateLimitRoutesBurst:  r.int("RATE_LIMIT_ROUTES_BURST", 10),
		TrustProxyHeaders:     r.bool("TRUST_PROXY_HEADERS", false),
	}
	if err := r.err(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	if cfg.JWTTTL <= 0 {
		return nil, errors.New("invalid configuration: JWT_TTL must be positive")
	}
	if cfg.RouteProvider != "mock" && cfg.RouteProvider != "yandex" {
		return nil, fmt.Errorf("invalid configuration: ROUTE_PROVIDER=%q, expected mock or yandex", cfg.RouteProvider)
	}
	if cfg.RouteProvider == "yandex" && cfg.YandexAPIKey == "" {
		return nil, errors.New("invalid configuration: YANDEX_API_KEY is required when ROUTE_PROVIDER=yandex")
	}
	if cfg.RouteCacheTTL <= 0 {
		return nil, errors.New("invalid configuration: ROUTE_CACHE_TTL must be positive")
	}
	if err := cfg.validateLimits(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	if cfg.AppEnv == "production" && len(cfg.JWTSecret) < minProductionSecretLen {
		return nil, fmt.Errorf("invalid configuration: JWT_SECRET must be at least %d characters when APP_ENV=production", minProductionSecretLen)
	}

	return cfg, nil
}

const minProductionSecretLen = 32

// validateLimits: отрицательные значения бессмысленны, а включённому ограничению нужно ведро хотя бы на один запрос.
func (c *Config) validateLimits() error {
	var errs []error
	nonNegative := map[string]int{
		"DB_MAX_CONNS":                   c.DBMaxConns,
		"ROUTE_PROVIDER_MAX_CONCURRENCY": c.RouteProviderMaxConcurrency,
		"BCRYPT_MAX_CONCURRENCY":         c.BcryptMaxConcurrency,
		"ROUTE_CANDIDATE_PARALLELISM":    c.Routing.CandidateParallelism,
		"RATE_LIMIT_AUTH_PER_MIN":        c.RateLimitAuthPerMin,
		"RATE_LIMIT_PUBLIC_PER_MIN":      c.RateLimitPublicPerMin,
		"RATE_LIMIT_ROUTES_PER_MIN":      c.RateLimitRoutesPerMin,
	}
	for name, v := range nonNegative {
		if v < 0 {
			errs = append(errs, fmt.Errorf("%s must not be negative, got %d", name, v))
		}
	}
	for _, l := range []struct {
		perMin, burst int
		burstName     string
	}{
		{c.RateLimitAuthPerMin, c.RateLimitAuthBurst, "RATE_LIMIT_AUTH_BURST"},
		{c.RateLimitPublicPerMin, c.RateLimitPublicBurst, "RATE_LIMIT_PUBLIC_BURST"},
		{c.RateLimitRoutesPerMin, c.RateLimitRoutesBurst, "RATE_LIMIT_ROUTES_BURST"},
	} {
		if l.perMin > 0 && l.burst < 1 {
			errs = append(errs, fmt.Errorf("%s must be at least 1 while the limit is enabled, got %d", l.burstName, l.burst))
		}
	}
	return errors.Join(errs...)
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

func (r *envReader) bool(key string, fallback bool) bool {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s=%q: not a boolean (true or false)", key, raw))
		return fallback
	}
	return v
}

func (r *envReader) duration(key string, fallback time.Duration) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s=%q: not a duration (examples: 30m, 24h)", key, raw))
		return fallback
	}
	return v
}

func (r *envReader) err() error {
	return errors.Join(r.errs...)
}

// getEnv возвращает значение переменной окружения; пустое значение (VAR=) считается незаданным.
// Иначе, например, DB_PORT= дал бы DSN вида postgres://...@localhost:/db, который не разобрать.
func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
