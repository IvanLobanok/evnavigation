package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/config"
	"github.com/IvanLobanok/evnavigation/internal/models/route"
	"github.com/IvanLobanok/evnavigation/internal/provider/cache"
	"github.com/IvanLobanok/evnavigation/internal/provider/mock"
	"github.com/IvanLobanok/evnavigation/internal/provider/yandex"
)

func testConfig() *config.Config {
	return &config.Config{RouteProvider: "mock", RouteCacheTTL: time.Minute}
}

func TestNewRouteProvider_MockWithoutRedis(t *testing.T) {
	p, closeFn, err := newRouteProvider(context.Background(), testConfig(), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()

	if _, ok := p.(*mock.MockProvider); !ok {
		t.Errorf("without REDIS_URL the bare mock provider is expected, got %T", p)
	}
}

func TestNewRouteProvider_RedisWrapsTheProviderInACache(t *testing.T) {
	cfg := testConfig()
	cfg.RedisURL = "redis://127.0.0.1:1/0" // Redis недоступен: приложение всё равно должно запуститься

	p, closeFn, err := newRouteProvider(context.Background(), cfg, slog.Default())
	if err != nil {
		t.Fatalf("an unreachable Redis must not prevent startup: %v", err)
	}
	defer closeFn()

	if _, ok := p.(*cache.Provider); !ok {
		t.Fatalf("expected the caching decorator, got %T", p)
	}

	// и маршрут при этом строится: кэш просто молча пропускается
	segs, err := p.BuildRoute(context.Background(), route.RoutePoint{Lat: 53.9, Lon: 27.5}, route.RoutePoint{Lat: 53.8, Lon: 27.4})
	if err != nil || len(segs) == 0 {
		t.Errorf("routing must work without a reachable cache: n=%d err=%v", len(segs), err)
	}
}

func TestNewRouteProvider_Errors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config.Config)
		wantErr error // nil — достаточно любой ошибки
	}{
		{"unknown provider", func(c *config.Config) { c.RouteProvider = "google" }, nil},
		{"yandex is not implemented yet", func(c *config.Config) { c.RouteProvider = "yandex"; c.YandexAPIKey = "key" }, yandex.ErrNotImplemented},
		{"yandex without a key", func(c *config.Config) { c.RouteProvider = "yandex" }, yandex.ErrAPIKeyRequired},
		{"malformed redis url", func(c *config.Config) { c.RedisURL = "http://not-redis" }, nil},
		{"non-positive cache ttl", func(c *config.Config) { c.RedisURL = "redis://127.0.0.1:1/0"; c.RouteCacheTTL = 0 }, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			tc.mutate(cfg)

			p, closeFn, err := newRouteProvider(context.Background(), cfg, slog.Default())

			if err == nil {
				t.Fatal("expected an error")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("got %v, want %v", err, tc.wantErr)
			}
			if p != nil || closeFn != nil {
				t.Error("nothing must be returned on error")
			}
		})
	}
}
