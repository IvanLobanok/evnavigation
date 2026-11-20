ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);
DROP INDEX idx_route_requests_user_vehicle_id;
DROP INDEX idx_user_vehicles_user_id;
