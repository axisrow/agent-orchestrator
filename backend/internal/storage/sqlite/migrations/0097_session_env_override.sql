-- +goose Up
-- +goose StatementBegin

-- session_env is the durable, additive per-session environment override. It is
-- deliberately separate from project/role config: on each launch it wins over
-- those defaults, while AO-owned runtime variables still win last.
ALTER TABLE sessions ADD COLUMN session_env TEXT NOT NULL DEFAULT '{}';

-- +goose StatementEnd
