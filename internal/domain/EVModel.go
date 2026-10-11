package domain

type EVModel struct {
	ID                     int     `json:"id"`
	Brand                  string  `json:"brand"`
	Model                  string  `json:"model"`
	BatteryCapacityKwh     float64 `json:"battery_capacity_kwh"`
	AvgConsumptionKwh100km float64 `json:"avg_consumption_kwh_100km"`
	ConnectorType          string  `json:"connector_type"`
	MaxChargingPowerKw     float64 `json:"max_charging_power_kw"`
}
