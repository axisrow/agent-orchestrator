-- The process-footprint feature (status bar, orphan kill, `ao ps`) returns as a
-- runtime-toggleable surface: the Settings toggle flips it without a rebuild,
-- and the daemon re-reads the preference per request. On by default — restoring
-- the feature restores its previous behavior; turning it off is the deliberate
-- act.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_settings
    ADD COLUMN process_inventory INTEGER NOT NULL DEFAULT 1
        CHECK (process_inventory IN (0, 1));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_settings DROP COLUMN process_inventory;
-- +goose StatementEnd
