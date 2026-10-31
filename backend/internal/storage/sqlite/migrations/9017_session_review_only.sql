-- +goose Up
-- review_only marks sessions spawned to review a PR without code-mutation
-- rights. Spawn-fixed like session_permissions: no UPDATE path can change it.
ALTER TABLE sessions ADD COLUMN review_only BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
