package evmodel

import "errors"

var ErrNotFound = errors.New("ev model not found")

type EVModel struct {
	ID                     int
	Brand                  string
	Model                  string
	BatteryCapacityKwh     float64
	AvgConsumptionKwh100km float64
	ConnectorType          string
	MaxChargingPowerKw     float64
}
