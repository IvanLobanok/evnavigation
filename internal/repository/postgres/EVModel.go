package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/IvanLobanok/evnavigation/internal/models/evmodel"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) GetAllEVModels(ctx context.Context) ([]evmodel.EVModel, error) {
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

	var models []evmodel.EVModel

	for rows.Next() {
		var m evmodel.EVModel
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

func (r *Repository) GetEVModelByID(ctx context.Context, id int) (*evmodel.EVModel, error) {
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
		WHERE id = $1`

	var model evmodel.EVModel
	err := r.db.QueryRow(ctx, query, id).Scan(&model.ID, &model.Brand, &model.Model, &model.BatteryCapacityKwh, &model.AvgConsumptionKwh100km, &model.ConnectorType, &model.MaxChargingPowerKw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, evmodel.ErrNotFound
		}
		return nil, fmt.Errorf("failed to query EVModel: %w", err)
	}
	return &model, nil
}
