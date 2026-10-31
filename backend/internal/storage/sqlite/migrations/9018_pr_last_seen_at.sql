-- +goose Up
-- last_seen_at is the honest "observer last refreshed this PR" cursor: bumped
-- on every successful observation, including no-op polls where no content
-- changed (those skip the normal pr write by design). NULL means never seen.
-- The observation stamps (observed_at / ci_observed_at) freeze until content
-- changes and must not be read as freshness cursors.
ALTER TABLE pr ADD COLUMN last_seen_at TIMESTAMP;

-- +goose Down
ALTER TABLE pr DROP COLUMN last_seen_at;
