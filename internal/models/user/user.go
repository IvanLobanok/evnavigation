package user

import (
	"errors"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/evmodel"
)

var (
	ErrNotFound        = errors.New("user not found")
	ErrEmailTaken      = errors.New("email is already registered")
	ErrVehicleNotFound = errors.New("vehicle not found")
)

type User struct {
	ID           int
	Name         string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

type UserVehicle struct {
	ID        int
	UserID    int
	EVModelID int
	Nickname  *string
	CreatedAt time.Time
}

// VehicleDetails - машина пользователя вместе с характеристиками её модели.
type VehicleDetails struct {
	UserVehicle
	EVModel evmodel.EVModel
}
