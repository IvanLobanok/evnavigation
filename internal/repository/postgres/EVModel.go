package postgres

import (
	"context"
	"evnavigation/internal/domain"
	"fmt"
)

func (r *Repository) GetEVModels(ctx context.Context) ([]domain.EVModel, error) {
	query :=
		`SELECT 
				id, 
				brand, 
				model,
				battery_capacity_kwh, 
				avg_consumption_kwh_100km, 
				connector_type, 
				max_charging_power_kw 
		FROM ev_models 
		ORDER BY brand, model;`

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query ev models: %w", err)
	}
	defer rows.Close()

	var models []domain.EVModel

	for rows.Next() {
		var m domain.EVModel
		if err := rows.Scan(
			&m.ID,
			&m.Brand,
			&m.Model,
			&m.BatteryCapacityKwh,
			&m.AvgConsumptionKwh100km,
			&m.ConnectorType,
			&m.MaxChargingPowerKw,
		); err != nil {
			return nil, fmt.Errorf("failed to scan ev model: %w", err)
		}
		models = append(models, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return models, nil
}
