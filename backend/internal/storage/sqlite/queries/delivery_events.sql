-- name: InsertDeliveryEvent :exec
INSERT INTO delivery_events (delivery_id, session_id, project_id, from_state, to_state, cause, reason, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListDeliveryEventsByDelivery :many
SELECT * FROM delivery_events WHERE delivery_id = ? ORDER BY id;

-- name: ListDeliveryEventsSince :many
SELECT * FROM delivery_events WHERE created_at >= ? ORDER BY id;
