-- PostGIS расширение
CREATE EXTENSION IF NOT EXISTS postgis;

-- 1. Пользователи
CREATE TABLE users (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,     
    name TEXT NOT NULL,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 2. Справочник моделей электромобилей
CREATE TABLE ev_models (
    id SERIAL PRIMARY KEY,
    brand TEXT NOT NULL,
    model TEXT NOT NULL,
    battery_capacity_kwh NUMERIC(6,2) NOT NULL,
    avg_consumption_kwh_100km NUMERIC(5,2) NOT NULL,
    connector_type TEXT NOT NULL,
    max_charging_power_kw NUMERIC(6,2) NOT NULL,
    UNIQUE (brand, model)
);

-- 3. Машины пользователей
CREATE TABLE user_vehicles (
    id SERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ev_model_id INT NOT NULL REFERENCES ev_models(id),
    nickname TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 4. Зарядные станции с PostGIS геометрией
CREATE TABLE charging_stations (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    location GEOGRAPHY(POINT, 4326) NOT NULL,
    connector_types TEXT[] NOT NULL,
    power_kw NUMERIC(6,2) NOT NULL,
    is_operational BOOLEAN NOT NULL DEFAULT true,
    source TEXT NOT NULL DEFAULT 'manual'
);

-- Индекс для быстрого геопоиска (GIST)
CREATE INDEX idx_charging_stations_location ON charging_stations USING GIST (location);

-- 5. История запросов маршрутов
CREATE TABLE route_requests (
    id SERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_vehicle_id INT NOT NULL REFERENCES user_vehicles(id) ON DELETE CASCADE,
    from_lat NUMERIC(9,6) NOT NULL,
    from_lon NUMERIC(9,6) NOT NULL,
    to_lat NUMERIC(9,6) NOT NULL,
    to_lon NUMERIC(9,6) NOT NULL,
    initial_charge_percent NUMERIC(5,2) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);