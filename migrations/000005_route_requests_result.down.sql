DROP INDEX IF EXISTS idx_route_requests_user_id_id;

ALTER TABLE route_requests
    DROP COLUMN IF EXISTS total_distance_km,
    DROP COLUMN IF EXISTS stops_count,
    DROP COLUMN IF EXISTS status;
