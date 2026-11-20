package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/IvanLobanok/evnavigation/internal/models/station"
	"github.com/jackc/pgx/v5"
)

// FindNearbyStations возвращает работающие станции в радиусе radiusMeters от точки: ближайшие первыми,
// при равном расстоянии по id (порядок стабилен), не больше limit штук.
//
// Если connectors не пуст, остаются только станции хотя бы с одним из этих разъёмов (точное совпадение строк).
// Фильтр по разъёму и LIMIT выполняются в SQL, чтобы в плотной зоне в Go не приезжали тысячи станций,
// из которых нужны единицы. Поиск по-прежнему ограничен радиусом через ST_DWithin (индекс GiST): чистый kNN
// без ограничения радиусом при редком разъёме обходил бы весь индекс.
func (r *Repository) FindNearbyStations(ctx context.Context, lat, lon, radiusMeters float64, connectors []string, limit int) ([]station.ChargingStation, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("find nearby stations: limit must be positive, got %d", limit)
	}
	if connectors == nil {
		connectors = []string{} // pgx передал бы nil как NULL, а cardinality(NULL) не равно 0
	}

	const query = `
		SELECT
			id,
			name,
			ST_Y(location::geometry) AS latitude,
			ST_X(location::geometry) AS longitude,
			power_kw,
			connector_types,
			is_operational
		FROM charging_stations
		WHERE is_operational
		  AND ST_DWithin(location, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3)
		  AND (cardinality($4::text[]) = 0 OR connector_types && $4::text[])
		ORDER BY ST_Distance(location, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography), id
		LIMIT $5`

	rows, err := r.db.Query(ctx, query, lon, lat, radiusMeters, connectors, limit)
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
