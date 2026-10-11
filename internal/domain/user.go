package domain

import (
	"time"
)

type User struct {
	ID           int       `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type UserVehicle struct {
	ID        int       `json:"id"`
	UserID    int       `json:"user_id"`
	EVModelID int       `json:"ev_model_id"`
	Nickname  *string   `json:"nickname"`
	CreatedAt time.Time `json:"created_at"`
}
