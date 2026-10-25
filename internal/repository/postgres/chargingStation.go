package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/IvanLobanok/evnavigation/internal/models/station"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) FindNearbyStations(ctx context.Context, lat, lon float64, radiusMeters float64) ([]station.ChargingStation, error) {
	query := `
    SELECT
			id,
			name,
        	ST_Y(location::geometry) as latitude,
        	ST_X(location::geometry) as longitude,
        	power_kw, connector_types, is_operational
    FROM charging_stations
    WHERE ST_DWithin(location, ST_SetSRID(ST_MakePoint($1,$2),4326)::geography, $3)
      AND is_operational = true
    ORDER BY ST_Distance(location, ST_SetSRID(ST_MakePoint($1,$2),4326)::geography);`

	rows, err := r.db.Query(ctx, query, lon, lat, radiusMeters)
	if err != nil {
		return nil, fmt.Errorf("failed to query nearby stations: %w", err)
	}
	defer rows.Close()

	var stations []station.ChargingStation

	for rows.Next() {
		s, err := r.scanStation(rows)
		if err != nil {
			return nil, err
		}
		stations = append(stations, *s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return stations, nil
}

func (r *Repository) scanStation(row pgx.Row) (*station.ChargingStation, error) {
	var s station.ChargingStation
	err := row.Scan(&s.ID, &s.Name, &s.Latitude, &s.Longitude, &s.PowerKW, &s.ConnectorTypes, &s.IsOperational)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, station.ErrNotFound
		}
		return nil, fmt.Errorf("get station: %w", err)
	}
	return &s, nil
}
