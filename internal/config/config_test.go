package config

import (
	"strings"
	"testing"

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
