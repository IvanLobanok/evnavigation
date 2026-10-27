package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/IvanLobanok/evnavigation/internal/models/evmodel"
	"github.com/jackc/pgx/v5"
)

const evmodelColumns = ` id, brand,model,battery_capacity_kwh, avg_consumption_kwh_100km, connector_type, max_charging_power_kw `

func (r *Repository) GetAllEVModels(ctx context.Context) ([]evmodel.EVModel, error) {
	query := `SELECT` + evmodelColumns + `FROM ev_models ORDER BY brand, model;`

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query ev models: %w", err)
	}
	defer rows.Close()

	var models []evmodel.EVModel

	for rows.Next() {
		m, err := r.scanEVModel(rows)
		if err != nil {
			return nil, err
		}
		models = append(models, *m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	if models == nil {
		models = []evmodel.EVModel{}
	}

	return models, nil
}

func (r *Repository) GetEVModelByID(ctx context.Context, id int) (*evmodel.EVModel, error) {
	query := `SELECT` + evmodelColumns + `FROM ev_models WHERE id = $1`
	return r.scanEVModel(r.db.QueryRow(ctx, query, id))
}

func (r *Repository) scanEVModel(row pgx.Row) (*evmodel.EVModel, error) {
	var m evmodel.EVModel
	err := row.Scan(&m.ID, &m.Brand, &m.Model, &m.BatteryCapacityKwh, &m.AvgConsumptionKwh100km, &m.ConnectorType, &m.MaxChargingPowerKw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, evmodel.ErrNotFound
		}
		return nil, fmt.Errorf("get EVModel: %w", err)
	}
	return &m, nil
}
