-- +goose Up
-- Per-session env overrides (e.g. a hot LLM provider switch via
-- ANTHROPIC_BASE_URL/ANTHROPIC_AUTH_TOKEN). Applied on every launch on top of
-- the project env; session values win. Values are secrets, so the column is
-- read and written through dedicated queries only and is never part of the
-- served session record.
ALTER TABLE sessions ADD COLUMN env_overrides TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN env_overrides;
