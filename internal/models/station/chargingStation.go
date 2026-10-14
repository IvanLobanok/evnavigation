package station

import (
	"time"
)

type ChargingStation struct {
	ID             int64
	Name           string
	Latitude       float64
	Longitude      float64
	PowerKW        float64
	ConnectorTypes []string
	IsOperational  bool
	CreatedAt      time.Time
}
