// Package yandex — место под реальный провайдер маршрутов на Яндекс.Router API.
//
// Пока это заготовка: сам вызов API не реализован, потому что для него нужны ключ и документация формата ответа.
// Подключение остальной системы уже готово: достаточно реализовать BuildRoute и убрать ErrNotImplemented из New.
package yandex

import (
	"context"
	"errors"

	"github.com/IvanLobanok/evnavigation/internal/models/route"
)

var (
	ErrAPIKeyRequired = errors.New("yandex: api key is required")
	ErrNotImplemented = errors.New("yandex: route provider is not implemented yet")
)

type Config struct {
	APIKey string
}

type Provider struct {
	cfg Config
}

var _ route.RouteProvider = (*Provider)(nil)

// New пока всегда возвращает ErrNotImplemented: так приложение сразу при старте скажет, что
// ROUTE_PROVIDER=yandex выбран рано, а не будет отвечать 500 на каждый запрос маршрута.
func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" {
		return nil, ErrAPIKeyRequired
	}
	return nil, ErrNotImplemented
}

// BuildRoute должен вернуть сегменты маршрута между точками с длинами в метрах (контракт см. в route.RouteProvider).
func (p *Provider) BuildRoute(_ context.Context, _, _ route.RoutePoint) ([]route.RouteSegment, error) {
	return nil, ErrNotImplemented
}
