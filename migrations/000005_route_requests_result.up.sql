-- В истории запросов маршрутов хранится не только вход, но и итог: чем закончился расчёт.
ALTER TABLE route_requests
    ADD COLUMN status            TEXT          NOT NULL DEFAULT 'ok', -- ok | no_station_nearby | station_unreachable | too_many_stops | error
    ADD COLUMN stops_count       INT           NOT NULL DEFAULT 0,
    ADD COLUMN total_distance_km NUMERIC(8, 2);                       -- NULL, если маршрут не построен

-- история пользователя читается страницами от новых к старым
CREATE INDEX idx_route_requests_user_id_id ON route_requests (user_id, id DESC);
