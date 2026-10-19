package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/IvanLobanok/evnavigation/internal/models/evmodel"
	"github.com/IvanLobanok/evnavigation/internal/models/user"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// vehicleSelect - общий хвост запроса: машина + её модель.
const vehicleColumns = `
	v.id, v.user_id, v.ev_model_id, v.nickname, v.created_at,
	m.id, m.brand, m.model, m.battery_capacity_kwh, m.avg_consumption_kwh_100km,
	m.connector_type, m.max_charging_power_kw`

// CreateVehicle привязывает модель к пользователю одним запросом (INSERT + JOIN).
// Несуществующая модель даёт evmodel.ErrNotFound, несуществующий пользователь - user.ErrNotFound.
func (r *Repository) CreateVehicle(ctx context.Context, userID, evModelID int, nickname *string) (*user.VehicleDetails, error) {
	query := `
		WITH v AS (
			INSERT INTO user_vehicles (user_id, ev_model_id, nickname)
			VALUES ($1, $2, $3)
			RETURNING id, user_id, ev_model_id, nickname, created_at
		)
		SELECT ` + vehicleColumns + `
		FROM v JOIN ev_models m ON m.id = v.ev_model_id`

	d, err := scanVehicle(r.db.QueryRow(ctx, query, userID, evModelID, nickname))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.ForeignKeyViolation {
			switch pgErr.ConstraintName {
			case "user_vehicles_ev_model_id_fkey":
				return nil, evmodel.ErrNotFound
			case "user_vehicles_user_id_fkey":
				return nil, user.ErrNotFound
			}
		}
		return nil, fmt.Errorf("create vehicle: %w", err)
	}
	return d, nil
}

// GetVehicle возвращает машину, только если она принадлежит userID.
// Чужая и несуществующая машины неразличимы: обе дают user.ErrVehicleNotFound.
func (r *Repository) GetVehicle(ctx context.Context, userID, vehicleID int) (*user.VehicleDetails, error) {
	query := `
		SELECT ` + vehicleColumns + `
		FROM user_vehicles v JOIN ev_models m ON m.id = v.ev_model_id
		WHERE v.id = $1 AND v.user_id = $2`

	d, err := scanVehicle(r.db.QueryRow(ctx, query, vehicleID, userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, user.ErrVehicleNotFound
		}
		return nil, fmt.Errorf("get vehicle: %w", err)
	}
	return d, nil
}

func (r *Repository) ListVehicles(ctx context.Context, userID int) ([]user.VehicleDetails, error) {
	query := `
		SELECT ` + vehicleColumns + `
		FROM user_vehicles v JOIN ev_models m ON m.id = v.ev_model_id
		WHERE v.user_id = $1
		ORDER BY v.id`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	defer rows.Close()

	var out []user.VehicleDetails
	for rows.Next() {
		d, err := scanVehicle(rows)
		if err != nil {
			return nil, fmt.Errorf("scan vehicle: %w", err)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return out, nil
}

// scanVehicle подходит и для pgx.Row (QueryRow), и для pgx.Rows (Query).
func scanVehicle(row interface{ Scan(dest ...any) error }) (*user.VehicleDetails, error) {
	var d user.VehicleDetails
	err := row.Scan(
		&d.ID, &d.UserID, &d.EVModelID, &d.Nickname, &d.CreatedAt,
		&d.EVModel.ID, &d.EVModel.Brand, &d.EVModel.Model, &d.EVModel.BatteryCapacityKwh,
		&d.EVModel.AvgConsumptionKwh100km, &d.EVModel.ConnectorType, &d.EVModel.MaxChargingPowerKw,
	)
	if err != nil {
		return nil, err
	}
	return &d, nil
}
