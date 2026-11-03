-- +goose Up
ALTER TABLE pr ADD COLUMN ci_pending_since TIMESTAMP;

-- +goose Down
ALTER TABLE pr DROP COLUMN ci_pending_since;
