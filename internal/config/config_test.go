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
