package config

import (
	"strings"
	"testing"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/service/routing"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := routing.Config{
		CriticalChargePercent: 15,
		MinArrivalPercent:     5,
		TargetChargePercent:   80,
		SearchRadiusMeters:    5000,
		MaxStops:              5,
		MaxStationCandidates:  3,
		LookbackStepKm:        5,
		MaxLookbackKm:         30,
	}
	if cfg.Routing != want {
		t.Errorf("routing defaults:\n got  %+v\n want %+v", cfg.Routing, want)
	}
}

func TestLoad_Overrides(t *testing.T) {
	t.Setenv("CRITICAL_CHARGE_PERCENT", "20")
	t.Setenv("MAX_CHARGING_STOPS", "8")
	t.Setenv("MAX_LOOKBACK_KM", "12.5")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Routing.CriticalChargePercent != 20 {
		t.Errorf("CriticalChargePercent: got %v, want 20", cfg.Routing.CriticalChargePercent)
	}
	if cfg.Routing.MaxStops != 8 {
		t.Errorf("MaxStops: got %v, want 8", cfg.Routing.MaxStops)
	}
	if cfg.Routing.MaxLookbackKm != 12.5 {
		t.Errorf("MaxLookbackKm: got %v, want 12.5", cfg.Routing.MaxLookbackKm)
	}
}

func TestLoad_EmptyValueFallsBackToDefault(t *testing.T) {
	t.Setenv("TARGET_CHARGE_PERCENT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Routing.TargetChargePercent != 80 {
		t.Errorf("got %v, want default 80", cfg.Routing.TargetChargePercent)
	}
}

func TestLoad_InvalidValuesAreAllReported(t *testing.T) {
	t.Setenv("CRITICAL_CHARGE_PERCENT", "abc")
	t.Setenv("MAX_CHARGING_STOPS", "2.5") // дробное для int-поля

	cfg, err := Load()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if cfg != nil {
		t.Error("config must be nil on error")
	}
	for _, key := range []string{"CRITICAL_CHARGE_PERCENT", "MAX_CHARGING_STOPS"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error must mention %s, got: %v", key, err)
		}
	}
}

func TestLoad_JWTDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JWTTTL != 24*time.Hour {
		t.Errorf("JWTTTL: got %v, want 24h", cfg.JWTTTL)
	}
	if cfg.JWTSecret == "" {
		t.Error("dev default secret must be non-empty")
	}
}

func TestLoad_JWTTTL(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{"minutes", "30m", 30 * time.Minute, false},
		{"hours", "72h", 72 * time.Hour, false},
		{"empty falls back to default", "", 24 * time.Hour, false},
		{"plain number has no unit", "3600", 0, true},
		{"garbage", "tomorrow", 0, true},
		{"zero", "0s", 0, true},
		{"negative", "-1h", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JWT_TTL", tc.value)

			cfg, err := Load()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got ttl=%v", cfg.JWTTTL)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.JWTTTL != tc.want {
				t.Errorf("got %v, want %v", cfg.JWTTTL, tc.want)
			}
		})
	}
}

func TestLoad_ProductionRequiresStrongSecret(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		secret  string
		wantErr bool
	}{
		{"production with default secret", "production", "", true}, // JWT_SECRET не задан => dev-значение
		{"production with short secret", "production", "short-secret", true},
		{"production with 31 chars", "production", strings.Repeat("a", 31), true},
		{"production with 32 chars", "production", strings.Repeat("a", 32), false},
		{"development tolerates the dev secret", "development", "dev-secret", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("APP_ENV", tc.env)
			if tc.secret != "" {
				t.Setenv("JWT_SECRET", tc.secret)
			}

			_, err := Load()
			if (err != nil) != tc.wantErr {
				t.Fatalf("got err=%v, wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "JWT_SECRET") && !strings.Contains(err.Error(), "secret") {
				t.Errorf("error should explain the problem: %v", err)
			}
		})
	}
}

func TestLoad_ProviderAndCacheDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RouteProvider != "mock" {
		t.Errorf("RouteProvider: got %q, want mock", cfg.RouteProvider)
	}
	if cfg.RedisURL != "" {
		t.Errorf("the cache must be off by default, got REDIS_URL=%q", cfg.RedisURL)
	}
	if cfg.RouteCacheTTL != 30*time.Minute {
		t.Errorf("RouteCacheTTL: got %v, want 30m", cfg.RouteCacheTTL)
	}
}

func TestLoad_RouteProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		apiKey   string
		want     string
		wantErr  bool
	}{
		{"mock", "mock", "", "mock", false},
		{"empty value falls back to mock", "", "", "mock", false},
		{"yandex with a key", "yandex", "secret-key", "yandex", false},
		{"yandex without a key", "yandex", "", "", true},
		{"unknown provider", "google", "", "", true},
		{"case matters", "Mock", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ROUTE_PROVIDER", tc.provider)
			t.Setenv("YANDEX_API_KEY", tc.apiKey)

			cfg, err := Load()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got provider=%q", cfg.RouteProvider)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.RouteProvider != tc.want {
				t.Errorf("got %q, want %q", cfg.RouteProvider, tc.want)
			}
		})
	}
}

func TestLoad_RedisSettings(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://redis:6379/0")
	t.Setenv("ROUTE_CACHE_TTL", "5m")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RedisURL != "redis://redis:6379/0" || cfg.RouteCacheTTL != 5*time.Minute {
		t.Errorf("got url=%q ttl=%v", cfg.RedisURL, cfg.RouteCacheTTL)
	}
}

func TestLoad_RouteCacheTTLValidation(t *testing.T) {
	for _, v := range []string{"0s", "-5m", "soon", "300"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("ROUTE_CACHE_TTL", v)

			if _, err := Load(); err == nil {
				t.Errorf("ROUTE_CACHE_TTL=%q must be rejected", v)
			}
		})
	}
}

func TestLoad_EmptyVariablesAreTreatedAsUnset(t *testing.T) {
	// так выглядят `VAR=` в .env и `${VAR:-}` в docker-compose
	for _, k := range []string{"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SSLMODE", "SERVER_PORT", "APP_ENV", "JWT_SECRET", "ROUTE_PROVIDER"} {
		t.Setenv(k, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if want := "postgres://postgres:evnav_dev_pass@localhost:5432/evnavigation?sslmode=disable"; cfg.DatabaseURL != want {
		t.Errorf("DatabaseURL:\n got  %s\n want %s", cfg.DatabaseURL, want)
	}
	if cfg.ServerPort != "8080" || cfg.AppEnv != "development" || cfg.RouteProvider != "mock" || cfg.JWTSecret != "dev_secret_change_me" {
		t.Errorf("empty variables must fall back to the defaults: %+v", cfg)
	}
}

func TestLoad_DatabaseURLFromVariables(t *testing.T) {
	t.Setenv("DB_HOST", "db.internal")
	t.Setenv("DB_PORT", "5433")
	t.Setenv("DB_USER", "app")
	t.Setenv("DB_PASSWORD", "s3cret")
	t.Setenv("DB_NAME", "evnav")
	t.Setenv("DB_SSLMODE", "require")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if want := "postgres://app:s3cret@db.internal:5433/evnav?sslmode=require"; cfg.DatabaseURL != want {
		t.Errorf("got %s, want %s", cfg.DatabaseURL, want)
	}
}

func TestLoad_ProductionWithEmptySecretIsRejected(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("JWT_SECRET", "") // пустой секрет не должен молча превращаться в «достаточно длинный» дефолт

	if _, err := Load(); err == nil {
		t.Fatal("an empty secret in production must be rejected")
	}
}
