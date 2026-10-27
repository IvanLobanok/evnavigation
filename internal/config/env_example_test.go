package config

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/service/routing"
)

// .env.example — это документация конфигурации. Эти тесты не дают ей разойтись с кодом:
// файл должен загружаться, давать значения по умолчанию, и в нём должна быть каждая переменная, которую читает Load.

const envExamplePath = "../../.env.example"

func readEnvExample(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(envExamplePath)
	if err != nil {
		t.Fatalf("open %s: %v", envExamplePath, err)
	}
	defer func() { _ = f.Close() }()

	vars := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("line %q is not KEY=VALUE", line)
		}
		if strings.ContainsAny(value, " \t\"'") {
			t.Errorf("%s: the file is loaded with `source`, so the value must have no spaces or quotes: %q", key, value)
		}
		vars[key] = value
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return vars
}

func TestEnvExample_LoadsAndMatchesDefaults(t *testing.T) {
	for k, v := range readEnvExample(t) {
		t.Setenv(k, v)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf(".env.example must produce a valid configuration: %v", err)
	}

	wantRouting := routing.Config{
		CriticalChargePercent: 15, MinArrivalPercent: 5, TargetChargePercent: 80,
		SearchRadiusMeters: 5000, MaxStops: 5, MaxStationCandidates: 3,
		LookbackStepKm: 5, MaxLookbackKm: 30,
	}
	if cfg.Routing != wantRouting {
		t.Errorf("routing settings in .env.example differ from the defaults:\n got  %+v\n want %+v", cfg.Routing, wantRouting)
	}
	if cfg.JWTTTL != 24*time.Hour || cfg.RouteCacheTTL != 30*time.Minute {
		t.Errorf("durations: jwt=%v cache=%v", cfg.JWTTTL, cfg.RouteCacheTTL)
	}
	if cfg.RouteProvider != "mock" || cfg.RedisURL != "" || cfg.AppEnv != "development" || cfg.ServerPort != "8080" {
		t.Errorf("unexpected values: %+v", cfg)
	}
}

func TestEnvExample_EqualsUnsetEnvironment(t *testing.T) {
	// пустое окружение и .env.example дают одну и ту же конфигурацию (кроме порта БД)
	t.Setenv("DB_PORT", "") // не даём окружению разработчика влиять на сравнение
	defaults, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	for k, v := range readEnvExample(t) {
		t.Setenv(k, v)
	}
	t.Setenv("DB_PORT", "5432") // единственное осознанное отличие: порт БД для docker-compose
	example, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if *defaults != *example {
		t.Errorf("configurations differ:\n defaults %+v\n example  %+v", *defaults, *example)
	}
}

func TestEnvExample_DocumentsEveryVariableTheCodeReads(t *testing.T) {
	src, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatal(err)
	}
	// getEnv("X", ...), r.float("X", ...), r.int("X", ...), r.duration("X", ...)
	re := regexp.MustCompile(`(?:getEnv|\.float|\.int|\.duration)\("([A-Z][A-Z0-9_]*)"`)
	matches := re.FindAllStringSubmatch(string(src), -1)
	if len(matches) < 20 {
		t.Fatalf("expected to find the environment variables in config.go, found only %d (did the code style change?)", len(matches))
	}

	documented := readEnvExample(t)
	for _, m := range matches {
		if _, ok := documented[m[1]]; !ok {
			t.Errorf("%s is read by config.go but is missing from .env.example", m[1])
		}
	}
}

func TestEnvExample_HasNoUnknownVariables(t *testing.T) {
	src, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatal(err)
	}
	for key := range readEnvExample(t) {
		if !strings.Contains(string(src), `"`+key+`"`) {
			t.Errorf("%s is in .env.example but config.go never reads it (a typo or a stale entry)", key)
		}
	}
}
