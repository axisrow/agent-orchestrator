-- name: GetSessionEnvOverrides :one
SELECT env_overrides FROM sessions WHERE id = ?;

-- name: SetSessionEnvOverrides :exec
UPDATE sessions SET env_overrides = ? WHERE id = ?;
