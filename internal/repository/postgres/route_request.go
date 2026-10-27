package postgres

import (
	"context"
	"fmt"

	"github.com/IvanLobanok/evnavigation/internal/models/history"
)

// CreateRouteRequest сохраняет запрос маршрута и возвращает его id.
func (r *Repository) CreateRouteRequest(ctx context.Context, rr history.RouteRequest) (int, error) {
	query := `
		INSERT INTO route_requests (
			user_id, user_vehicle_id, from_lat, from_lon, to_lat, to_lon,
			initial_charge_percent, status, stops_count, total_distance_km
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id`

	var id int
	err := r.db.QueryRow(ctx, query,
		rr.UserID, rr.VehicleID, rr.From.Lat, rr.From.Lon, rr.To.Lat, rr.To.Lon,
		rr.InitialChargePercent, rr.Status, rr.StopsCount, rr.TotalDistanceKm,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create route request: %w", err)
	}
	return id, nil
}

// ListRouteRequests отдаёт запросы пользователя от новых к старым.
// beforeID > 0 — вернуть только записи со строго меньшим id (постраничность без OFFSET).
func (r *Repository) ListRouteRequests(ctx context.Context, userID, limit int, beforeID int64) ([]history.RouteRequest, error) {
	query := `
		SELECT id, user_id, user_vehicle_id, from_lat, from_lon, to_lat, to_lon,
		       initial_charge_percent, status, stops_count, total_distance_km, created_at
		FROM route_requests
		WHERE user_id = $1 AND ($2::bigint = 0 OR id < $2::bigint)
		ORDER BY id DESC
		LIMIT $3`

	rows, err := r.db.Query(ctx, query, userID, beforeID, limit)
	if err != nil {
		return nil, fmt.Errorf("list route requests: %w", err)
	}
	defer rows.Close()

	var out []history.RouteRequest
	for rows.Next() {
		var rr history.RouteRequest
		if err := rows.Scan(
			&rr.ID, &rr.UserID, &rr.VehicleID, &rr.From.Lat, &rr.From.Lon, &rr.To.Lat, &rr.To.Lon,
			&rr.InitialChargePercent, &rr.Status, &rr.StopsCount, &rr.TotalDistanceKm, &rr.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan route request: %w", err)
		}
		out = append(out, rr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return out, nil
}
