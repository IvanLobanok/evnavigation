-- GET /vehicles и проверка владельца машины фильтруют по user_id: без индекса это полное сканирование user_vehicles.
CREATE INDEX idx_user_vehicles_user_id ON user_vehicles (user_id);

-- внешний ключ без индекса: каждое каскадное удаление машины сканировало бы всю историю маршрутов.
CREATE INDEX idx_route_requests_user_vehicle_id ON route_requests (user_vehicle_id);

-- users_email_lower_idx (миграция 000004) уже гарантирует уникальность без учёта регистра, а значит и точную:
-- отдельный UNIQUE (email) только удваивает работу при каждой записи и ничего не добавляет.
ALTER TABLE users DROP CONSTRAINT users_email_key;
