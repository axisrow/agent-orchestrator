-- Fork issue #170: an empty check rollup is a terminal "no checks" verdict,
-- not a loading state. The provider now reports ci_state 'none'; widen the
-- pr.ci_state CHECK to accept it. SQLite cannot widen a CHECK in place;
-- rebuild like 0052 did: NO TRANSACTION is required to toggle foreign_keys,
-- while the explicit BEGIN IMMEDIATE below keeps the whole rebuild atomic.
-- Foreign keys must stay off for the swap because pr_checks, pr_comment,
-- pr_review_thread, pr_review, and pr_provider_identity all reference pr ON
-- DELETE CASCADE, and dropping pr under enforced foreign keys would wipe
-- their rows.
-- +goose Up
-- +goose NO TRANSACTION
-- +goose StatementBegin
PRAGMA foreign_keys=OFF;
BEGIN IMMEDIATE;

CREATE TABLE pr_next (
    url             TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    number          INTEGER NOT NULL DEFAULT 0,
    pr_state        TEXT NOT NULL DEFAULT 'open'
        CHECK (pr_state IN ('draft', 'open', 'merged', 'closed')),
    review_decision TEXT NOT NULL DEFAULT 'none'
        CHECK (review_decision IN ('none', 'approved', 'changes_requested', 'review_required')),
    ci_state        TEXT NOT NULL DEFAULT 'unknown'
        CHECK (ci_state IN ('unknown', 'pending', 'passing', 'failing', 'none')),
    mergeability    TEXT NOT NULL DEFAULT 'unknown'
        CHECK (mergeability IN ('unknown', 'mergeable', 'conflicting', 'blocked', 'unstable')),
    updated_at      TIMESTAMP NOT NULL,
    provider TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '', repo TEXT NOT NULL DEFAULT '', source_branch TEXT NOT NULL DEFAULT '', target_branch TEXT NOT NULL DEFAULT '', head_sha TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '', additions INTEGER NOT NULL DEFAULT 0, deletions INTEGER NOT NULL DEFAULT 0, changed_files INTEGER NOT NULL DEFAULT 0, author TEXT NOT NULL DEFAULT '', base_sha TEXT NOT NULL DEFAULT '', merge_commit_sha TEXT NOT NULL DEFAULT '', is_draft INTEGER NOT NULL DEFAULT 0, is_merged INTEGER NOT NULL DEFAULT 0, is_closed INTEGER NOT NULL DEFAULT 0, provider_state TEXT NOT NULL DEFAULT '', provider_mergeable TEXT NOT NULL DEFAULT '', provider_merge_state_status TEXT NOT NULL DEFAULT '', html_url TEXT NOT NULL DEFAULT '', created_at_provider TIMESTAMP, updated_at_provider TIMESTAMP, merged_at_provider TIMESTAMP, closed_at_provider TIMESTAMP, metadata_hash TEXT NOT NULL DEFAULT '', ci_hash TEXT NOT NULL DEFAULT '', review_hash TEXT NOT NULL DEFAULT '', observed_at TIMESTAMP, ci_observed_at TIMESTAMP, review_observed_at TIMESTAMP, last_nudge_signature TEXT NOT NULL DEFAULT '', state_changed_at TIMESTAMP, auto_inject_ci BOOLEAN NOT NULL DEFAULT TRUE, provider_id TEXT NOT NULL DEFAULT '', author_avatar_url TEXT NOT NULL DEFAULT '', review_partial BOOLEAN NOT NULL DEFAULT TRUE, last_seen_at TIMESTAMP, ci_pending_since TIMESTAMP
);

INSERT INTO pr_next (
    url, session_id, number, pr_state, review_decision, ci_state, mergeability, updated_at,
    provider, host, repo, source_branch, target_branch, head_sha, title, additions, deletions,
    changed_files, author, base_sha, merge_commit_sha, is_draft, is_merged, is_closed,
    provider_state, provider_mergeable, provider_merge_state_status, html_url,
    created_at_provider, updated_at_provider, merged_at_provider, closed_at_provider,
    metadata_hash, ci_hash, review_hash, observed_at, ci_observed_at, review_observed_at,
    last_nudge_signature, state_changed_at, auto_inject_ci, provider_id, author_avatar_url,
    review_partial, last_seen_at, ci_pending_since
)
SELECT
    url, session_id, number, pr_state, review_decision, ci_state, mergeability, updated_at,
    provider, host, repo, source_branch, target_branch, head_sha, title, additions, deletions,
    changed_files, author, base_sha, merge_commit_sha, is_draft, is_merged, is_closed,
    provider_state, provider_mergeable, provider_merge_state_status, html_url,
    created_at_provider, updated_at_provider, merged_at_provider, closed_at_provider,
    metadata_hash, ci_hash, review_hash, observed_at, ci_observed_at, review_observed_at,
    last_nudge_signature, state_changed_at, auto_inject_ci, provider_id, author_avatar_url,
    review_partial, last_seen_at, ci_pending_since
FROM pr;

-- Triggers on the CHILD tables compile SELECTs against main.pr; dropping pr
-- under them breaks their next statement. Carry them across the swap.
DROP TRIGGER IF EXISTS pr_checks_cdc_insert;
DROP TRIGGER IF EXISTS pr_checks_cdc_update;
DROP TRIGGER IF EXISTS pr_review_threads_cdc_insert;
DROP TRIGGER IF EXISTS pr_review_threads_cdc_update;

DROP TABLE pr;
ALTER TABLE pr_next RENAME TO pr;

CREATE INDEX idx_pr_session ON pr (session_id);
CREATE UNIQUE INDEX idx_pr_provider_identity
    ON pr(provider, host, provider_id)
    WHERE provider_id != '';

CREATE TRIGGER pr_checks_cdc_insert
AFTER INSERT ON pr_checks
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_check_recorded',
        json_object('pr', NEW.pr_url, 'name', NEW.name, 'commit', NEW.commit_hash, 'status', NEW.status),
        NEW.created_at);
END;

CREATE TRIGGER pr_checks_cdc_update
AFTER UPDATE ON pr_checks
WHEN OLD.status <> NEW.status
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_check_recorded',
        json_object('pr', NEW.pr_url, 'name', NEW.name, 'commit', NEW.commit_hash, 'status', NEW.status),
        datetime('now'));
END;

CREATE TRIGGER pr_review_threads_cdc_insert
AFTER INSERT ON pr_review_threads
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_review_thread_added',
        json_object(
            'pr', NEW.pr_url,
            'thread', NEW.thread_id,
            'path', NEW.path,
            'line', NEW.line,
            'resolved', json(CASE WHEN NEW.resolved THEN 'true' ELSE 'false' END),
            'isBot', json(CASE WHEN NEW.is_bot THEN 'true' ELSE 'false' END)
        ),
        NEW.updated_at);
END;

CREATE TRIGGER pr_review_threads_cdc_update
AFTER UPDATE ON pr_review_threads
WHEN OLD.resolved <> NEW.resolved
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_review_thread_resolved',
        json_object(
            'pr', NEW.pr_url,
            'thread', NEW.thread_id,
            'path', NEW.path,
            'line', NEW.line,
            'resolved', json(CASE WHEN NEW.resolved THEN 'true' ELSE 'false' END)
        ),
        NEW.updated_at);
END;

CREATE TRIGGER pr_cdc_insert
AFTER INSERT ON pr
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_created',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability),
        NEW.updated_at);
END;

CREATE TRIGGER pr_session_cdc_update
AFTER UPDATE ON pr
WHEN OLD.session_id <> NEW.session_id
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id,
        'pr_session_changed',
        json_object(
            'url', NEW.url,
            'fromSession', OLD.session_id,
            'toSession', NEW.session_id),
        NEW.updated_at);
END;

CREATE TRIGGER pr_cdc_update
AFTER UPDATE ON pr
WHEN OLD.pr_state <> NEW.pr_state
    OR OLD.ci_state <> NEW.ci_state
    OR OLD.review_decision <> NEW.review_decision
    OR OLD.mergeability <> NEW.mergeability
    OR OLD.auto_inject_ci <> NEW.auto_inject_ci
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_updated',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability,
                    'autoInjectCI', json(CASE WHEN NEW.auto_inject_ci THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;

COMMIT;
PRAGMA foreign_keys=ON;

-- +goose StatementEnd

-- +goose Down
-- +goose NO TRANSACTION
-- +goose StatementBegin
PRAGMA foreign_keys=OFF;
BEGIN IMMEDIATE;

DELETE FROM pr WHERE ci_state = 'none';

CREATE TABLE pr_previous (
    url             TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    number          INTEGER NOT NULL DEFAULT 0,
    pr_state        TEXT NOT NULL DEFAULT 'open'
        CHECK (pr_state IN ('draft', 'open', 'merged', 'closed')),
    review_decision TEXT NOT NULL DEFAULT 'none'
        CHECK (review_decision IN ('none', 'approved', 'changes_requested', 'review_required')),
    ci_state        TEXT NOT NULL DEFAULT 'unknown'
        CHECK (ci_state IN ('unknown', 'pending', 'passing', 'failing')),
    mergeability    TEXT NOT NULL DEFAULT 'unknown'
        CHECK (mergeability IN ('unknown', 'mergeable', 'conflicting', 'blocked', 'unstable')),
    updated_at      TIMESTAMP NOT NULL,
    provider TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '', repo TEXT NOT NULL DEFAULT '', source_branch TEXT NOT NULL DEFAULT '', target_branch TEXT NOT NULL DEFAULT '', head_sha TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '', additions INTEGER NOT NULL DEFAULT 0, deletions INTEGER NOT NULL DEFAULT 0, changed_files INTEGER NOT NULL DEFAULT 0, author TEXT NOT NULL DEFAULT '', base_sha TEXT NOT NULL DEFAULT '', merge_commit_sha TEXT NOT NULL DEFAULT '', is_draft INTEGER NOT NULL DEFAULT 0, is_merged INTEGER NOT NULL DEFAULT 0, is_closed INTEGER NOT NULL DEFAULT 0, provider_state TEXT NOT NULL DEFAULT '', provider_mergeable TEXT NOT NULL DEFAULT '', provider_merge_state_status TEXT NOT NULL DEFAULT '', html_url TEXT NOT NULL DEFAULT '', created_at_provider TIMESTAMP, updated_at_provider TIMESTAMP, merged_at_provider TIMESTAMP, closed_at_provider TIMESTAMP, metadata_hash TEXT NOT NULL DEFAULT '', ci_hash TEXT NOT NULL DEFAULT '', review_hash TEXT NOT NULL DEFAULT '', observed_at TIMESTAMP, ci_observed_at TIMESTAMP, review_observed_at TIMESTAMP, last_nudge_signature TEXT NOT NULL DEFAULT '', state_changed_at TIMESTAMP, auto_inject_ci BOOLEAN NOT NULL DEFAULT TRUE, provider_id TEXT NOT NULL DEFAULT '', author_avatar_url TEXT NOT NULL DEFAULT '', review_partial BOOLEAN NOT NULL DEFAULT TRUE, last_seen_at TIMESTAMP, ci_pending_since TIMESTAMP
);

INSERT INTO pr_previous SELECT * FROM pr;

DROP TRIGGER IF EXISTS pr_checks_cdc_insert;
DROP TRIGGER IF EXISTS pr_checks_cdc_update;
DROP TRIGGER IF EXISTS pr_review_threads_cdc_insert;
DROP TRIGGER IF EXISTS pr_review_threads_cdc_update;

DROP TABLE pr;
ALTER TABLE pr_previous RENAME TO pr;

CREATE INDEX idx_pr_session ON pr (session_id);
CREATE UNIQUE INDEX idx_pr_provider_identity
    ON pr(provider, host, provider_id)
    WHERE provider_id != '';

CREATE TRIGGER pr_checks_cdc_insert
AFTER INSERT ON pr_checks
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_check_recorded',
        json_object('pr', NEW.pr_url, 'name', NEW.name, 'commit', NEW.commit_hash, 'status', NEW.status),
        NEW.created_at);
END;

CREATE TRIGGER pr_checks_cdc_update
AFTER UPDATE ON pr_checks
WHEN OLD.status <> NEW.status
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_check_recorded',
        json_object('pr', NEW.pr_url, 'name', NEW.name, 'commit', NEW.commit_hash, 'status', NEW.status),
        datetime('now'));
END;

CREATE TRIGGER pr_review_threads_cdc_insert
AFTER INSERT ON pr_review_threads
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_review_thread_added',
        json_object(
            'pr', NEW.pr_url,
            'thread', NEW.thread_id,
            'path', NEW.path,
            'line', NEW.line,
            'resolved', json(CASE WHEN NEW.resolved THEN 'true' ELSE 'false' END),
            'isBot', json(CASE WHEN NEW.is_bot THEN 'true' ELSE 'false' END)
        ),
        NEW.updated_at);
END;

CREATE TRIGGER pr_review_threads_cdc_update
AFTER UPDATE ON pr_review_threads
WHEN OLD.resolved <> NEW.resolved
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_review_thread_resolved',
        json_object(
            'pr', NEW.pr_url,
            'thread', NEW.thread_id,
            'path', NEW.path,
            'line', NEW.line,
            'resolved', json(CASE WHEN NEW.resolved THEN 'true' ELSE 'false' END)
        ),
        NEW.updated_at);
END;

CREATE TRIGGER pr_cdc_insert
AFTER INSERT ON pr
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_created',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability),
        NEW.updated_at);
END;

CREATE TRIGGER pr_session_cdc_update
AFTER UPDATE ON pr
WHEN OLD.session_id <> NEW.session_id
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id,
        'pr_session_changed',
        json_object(
            'url', NEW.url,
            'fromSession', OLD.session_id,
            'toSession', NEW.session_id),
        NEW.updated_at);
END;

CREATE TRIGGER pr_cdc_update
AFTER UPDATE ON pr
WHEN OLD.pr_state <> NEW.pr_state
    OR OLD.ci_state <> NEW.ci_state
    OR OLD.review_decision <> NEW.review_decision
    OR OLD.mergeability <> NEW.mergeability
    OR OLD.auto_inject_ci <> NEW.auto_inject_ci
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_updated',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability,
                    'autoInjectCI', json(CASE WHEN NEW.auto_inject_ci THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;

COMMIT;
PRAGMA foreign_keys=ON;
-- +goose StatementEnd
