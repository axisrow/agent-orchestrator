-- name: GetGatewayEntry :one
SELECT * FROM gateway_entries WHERE scope = ? AND project_id = ?;

-- name: ListGatewayEntries :many
SELECT * FROM gateway_entries ORDER BY scope, project_id;

-- name: UpsertGatewayEntry :exec
INSERT INTO gateway_entries (scope, project_id, base_url, auth_token, model, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(scope, project_id) DO UPDATE SET
    base_url = excluded.base_url,
    auth_token = excluded.auth_token,
    model = excluded.model,
    updated_at = excluded.updated_at;

-- name: CountGatewayEntries :one
SELECT count(*) FROM gateway_entries;
