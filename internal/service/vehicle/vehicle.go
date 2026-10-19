package vehicle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/IvanLobanok/evnavigation/internal/models/evmodel"
	"github.com/IvanLobanok/evnavigation/internal/models/user"
)

const MaxNicknameLen = 50

var ErrInvalidNickname = fmt.Errorf("nickname must be at most %d characters", MaxNicknameLen)

// Store - хранилище машин пользователей (реализует postgres.Repository).
type Store interface {
	CreateVehicle(ctx context.Context, userID, evModelID int, nickname *string) (*user.VehicleDetails, error)
	GetVehicle(ctx context.Context, userID, vehicleID int) (*user.VehicleDetails, error)
	ListVehicles(ctx context.Context, userID int) ([]user.VehicleDetails, error)
}

// ModelCatalog - справочник моделей электромобилей (реализует postgres.Repository).
type ModelCatalog interface {
	GetAllEVModels(ctx context.Context) ([]evmodel.EVModel, error)
}

type Service struct {
	store  Store
	models ModelCatalog
}

func NewService(store Store, models ModelCatalog) *Service {
	return &Service{store: store, models: models}
}

func (s *Service) ListModels(ctx context.Context) ([]evmodel.EVModel, error) {
	return s.models.GetAllEVModels(ctx)
}

// AddVehicle привязывает модель к пользователю. Пустой nickname сохраняется как NULL.
// Несуществующая модель даёт evmodel.ErrNotFound.
func (s *Service) AddVehicle(ctx context.Context, userID, evModelID int, nickname string) (*user.VehicleDetails, error) {
	nick := strings.TrimSpace(nickname)
	if utf8.RuneCountInString(nick) > MaxNicknameLen {
		return nil, ErrInvalidNickname
	}
	var nickPtr *string
	if nick != "" {
		nickPtr = &nick
	}

	d, err := s.store.CreateVehicle(ctx, userID, evModelID, nickPtr)
	if err != nil {
		if errors.Is(err, evmodel.ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("add vehicle: %w", err)
	}
	return d, nil
}

// GetVehicle возвращает машину только владельцу; чужая машина даёт user.ErrVehicleNotFound.
func (s *Service) GetVehicle(ctx context.Context, userID, vehicleID int) (*user.VehicleDetails, error) {
	return s.store.GetVehicle(ctx, userID, vehicleID)
}

func (s *Service) ListVehicles(ctx context.Context, userID int) ([]user.VehicleDetails, error) {
	return s.store.ListVehicles(ctx, userID)
}
