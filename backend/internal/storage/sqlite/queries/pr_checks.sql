-- name: UpsertPRCheck :exec
INSERT INTO pr_checks (pr_url, name, commit_hash, status, url, log_tail, created_at, conclusion, details)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (pr_url, name, commit_hash) DO UPDATE SET
    status = excluded.status,
    url = excluded.url,
    log_tail = excluded.log_tail,
    conclusion = excluded.conclusion,
    details = excluded.details;

-- name: ListChecksByPR :many
SELECT pr_url, name, commit_hash, status, url, log_tail, created_at, conclusion, details
FROM pr_checks WHERE pr_url = ? ORDER BY name, created_at;

-- Per-sha replace: the stored checks for one head are a snapshot of the
-- provider's live rollup, not an accumulator. After an observation upserts the
-- current generation, this drops the head's rows the snapshot did not confirm
-- (renamed checks, re-expanded matrices, phantom template names), so the card
-- cannot mix check generations of the same commit.
-- name: DeletePRChecksNotInGeneration :exec
DELETE FROM pr_checks
WHERE pr_url = ? AND commit_hash = ?
  AND name NOT IN (sqlc.slice('names'));
