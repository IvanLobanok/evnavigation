package domain

import (
	"time"
)

type ChargingStation struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Latitude       float64   `json:"latitude"`
	Longitude      float64   `json:"longitude"`
	PowerKW        float64   `json:"power_kw"`
	ConnectorTypes []string  `json:"connector_type"`
	IsOperational  bool      `json:"is_operational"`
	CreatedAt      time.Time `json:"created_at"`
}
