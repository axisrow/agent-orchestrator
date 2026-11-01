-- The daemon publishes each review pass to the provider and records the review
-- id (0016). The verdict event a publication asks for can be rejected — GitHub
-- forbids APPROVE and REQUEST_CHANGES from the PR author's own account — and
-- the publication then lands as COMMENT. This column records the review state
-- GitHub actually assigned (APPROVED / CHANGES_REQUESTED / COMMENTED), so
-- review_run reflects the provider's outcome instead of only the verdict AO
-- recorded (issue #188). Empty for runs published before this fix.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE review_run ADD COLUMN github_review_state TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE review_run DROP COLUMN github_review_state;
-- +goose StatementEnd
