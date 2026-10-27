package history

import (
	"context"
	"fmt"

	"github.com/IvanLobanok/evnavigation/internal/models/history"
)

const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// Store — хранилище истории запросов (реализует postgres.Repository).
type Store interface {
	CreateRouteRequest(ctx context.Context, rr history.RouteRequest) (int, error)
	ListRouteRequests(ctx context.Context, userID, limit int, beforeID int64) ([]history.RouteRequest, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// Record сохраняет запрос маршрута.
func (s *Service) Record(ctx context.Context, rr history.RouteRequest) error {
	if _, err := s.store.CreateRouteRequest(ctx, rr); err != nil {
		return fmt.Errorf("record route request: %w", err)
	}
	return nil
}

// List возвращает страницу истории пользователя от новых запросов к старым.
// limit приводится к диапазону [1, MaxLimit] (<= 0 даёт DefaultLimit).
// hasMore сообщает, есть ли за этой страницей более старые записи: для этого запрашивается на одну запись больше.
func (s *Service) List(ctx context.Context, userID, limit int, beforeID int64) (items []history.RouteRequest, hasMore bool, err error) {
	switch {
	case limit <= 0:
		limit = DefaultLimit
	case limit > MaxLimit:
		limit = MaxLimit
	}

	items, err = s.store.ListRouteRequests(ctx, userID, limit+1, beforeID)
	if err != nil {
		return nil, false, fmt.Errorf("list route requests: %w", err)
	}
	if len(items) > limit {
		return items[:limit], true, nil
	}
	return items, false, nil
}
