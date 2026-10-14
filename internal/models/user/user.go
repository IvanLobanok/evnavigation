package user

import (
	"time"
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
