// Package limit ограничивает число одновременных вызовов провайдера маршрутов.
//
// Внешний API маршрутов имеет квоты и конечную пропускную способность. Без ограничения всплеск запросов к /routes
// превращается во всплеск обращений к нему (на один запрос приходится до десятков вызовов), и провайдер начинает
// отвечать ошибками. Здесь лишние вызовы ждут слот, пока не истечёт контекст запроса.
package limit

import (
	"context"

	"github.com/IvanLobanok/evnavigation/internal/models/route"
)

// Provider пропускает к next не больше maxConcurrent вызовов BuildRoute одновременно.
type Provider struct {
	next  route.RouteProvider
	slots chan struct{}
}

// New оборачивает next. maxConcurrent <= 0 не ограничивает: возвращается сам next.
func New(next route.RouteProvider, maxConcurrent int) route.RouteProvider {
	if maxConcurrent <= 0 {
		return next
	}
	return &Provider{next: next, slots: make(chan struct{}, maxConcurrent)}
}

func (p *Provider) BuildRoute(ctx context.Context, from, to route.RoutePoint) ([]route.RouteSegment, error) {
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-p.slots }()

	return p.next.BuildRoute(ctx, from, to)
}
