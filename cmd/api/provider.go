package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/config"
	"github.com/IvanLobanok/evnavigation/internal/models/route"
	"github.com/IvanLobanok/evnavigation/internal/provider/cache"
	"github.com/IvanLobanok/evnavigation/internal/provider/mock"
	"github.com/IvanLobanok/evnavigation/internal/provider/yandex"
)

// newRouteProvider выбирает провайдера маршрутов по конфигурации и при заданном REDIS_URL оборачивает его кэшем.
// Второе возвращаемое значение освобождает ресурсы (соединение с Redis); его нужно вызвать при остановке.
func newRouteProvider(ctx context.Context, cfg *config.Config, logger *slog.Logger) (route.RouteProvider, func(), error) {
	var base route.RouteProvider
	switch cfg.RouteProvider {
	case "mock":
		base = &mock.MockProvider{}
	case "yandex":
		p, err := yandex.New(yandex.Config{APIKey: cfg.YandexAPIKey})
		if err != nil {
			return nil, nil, fmt.Errorf("yandex provider: %w", err)
		}
		base = p
	default:
		return nil, nil, fmt.Errorf("unknown route provider %q", cfg.RouteProvider)
	}

	if cfg.RedisURL == "" {
		logger.Info("route cache is disabled (REDIS_URL is empty)", "provider", cfg.RouteProvider)
		return base, func() {}, nil
	}

	// неверный URL — ошибка конфигурации, и о ней нужно узнать сразу
	client, err := cache.NewRedisClient(cfg.RedisURL)
	if err != nil {
		return nil, nil, fmt.Errorf("redis: %w", err)
	}

	// А вот недоступный Redis при старте — не повод не запускаться: кэш лишь ускоряет работу.
	// Клиент переподключается сам, а Provider при сбоях кэша идёт напрямую к провайдеру маршрутов.
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		logger.Warn("redis is not reachable, the route cache will start working once it is", "error", err)
	}

	cached, err := cache.New(base, cache.NewRedisStore(client), cfg.RouteCacheTTL, cache.WithLogger(logger))
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("route cache: %w", err)
	}
	logger.Info("route cache is enabled", "provider", cfg.RouteProvider, "ttl", cfg.RouteCacheTTL)

	return cached, func() { _ = client.Close() }, nil
}
