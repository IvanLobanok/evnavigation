package postgres

import (
	"context"
	"fmt"

	"github.com/IvanLobanok/evnavigation/internal/models/station"
)

func (r *Repository) FindNearByStations(ctx context.Context, lat, lon float64, radiusMeters float64) ([]station.ChargingStation, error) {
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
		var s station.ChargingStation
		if err := rows.Scan(
			&s.ID,
			&s.Name,
			&s.Latitude,
			&s.Longitude,
			&s.PowerKW,
			&s.ConnectorTypes,
			&s.IsOperational,
		); err != nil {
			return nil, fmt.Errorf("failed to scan station: %w", err)
		}
		stations = append(stations, s)
	}

	return stations, nil
}
