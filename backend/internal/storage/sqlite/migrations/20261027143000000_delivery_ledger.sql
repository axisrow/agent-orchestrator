-- Единый delivery-леджер (fork, фаза 1): reports становится deliveries — одна
-- таблица владеет durable-циклом доставки (pending/claimed/acknowledged/stale)
-- для всего, что daemon доставляет получателю. kind готовит приём будущих
-- каналов (transition/queued_turn/startup_cue — фазы 2+); сегодня пишет только
-- report. recipient_session_id — денормализация для каналов с фиксированным
-- получателем; отчёты, как и прежде, резолвят активного оркестратора проекта.
-- Rebuild вместо ALTER: SQLite не меняет CHECK/FK у живой таблицы. Дети
-- (report_outputs) пересоздаются до DROP родителя — иначе каскад снёс бы их.
-- +goose Up

CREATE TABLE deliveries (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL DEFAULT 'report' CHECK (kind IN ('report', 'transition', 'queued_turn', 'startup_cue')),
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    recipient_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
    state TEXT NOT NULL DEFAULT '' CHECK (state IN ('', 'checkpoint', 'needs_input', 'stuck', 'done')),
    note TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 1000),
    message TEXT NOT NULL DEFAULT '' CHECK (length(message) <= 1000),
    created_at TIMESTAMP NOT NULL,
    delivery_state TEXT NOT NULL DEFAULT 'pending' CHECK (delivery_state IN ('pending', 'claimed', 'acknowledged', 'stale')),
    available_at TIMESTAMP NOT NULL,
    settlement_deadline TIMESTAMP,
    repeat_count INTEGER NOT NULL DEFAULT 1 CHECK (repeat_count >= 1),
    claim_token TEXT NOT NULL DEFAULT '',
    claimed_at TIMESTAMP,
    delivery_attempts INTEGER NOT NULL DEFAULT 0 CHECK (delivery_attempts >= 0),
    acknowledged_at TIMESTAMP,
    last_error TEXT NOT NULL DEFAULT '',
    emitted_at TIMESTAMP,
    delivery_batch_id TEXT NOT NULL DEFAULT '',
    CHECK (message = '' OR (state = '' AND note = '')),
    CHECK ((state = '' AND note = '') OR (state <> '' AND length(trim(note)) > 0)),
    CHECK ((state = 'done' AND settlement_deadline IS NOT NULL) OR (state <> 'done' AND settlement_deadline IS NULL)),
    CHECK (
        (delivery_state = 'pending' AND claim_token = '' AND claimed_at IS NULL AND acknowledged_at IS NULL)
        OR (delivery_state = 'claimed' AND claim_token <> '' AND claimed_at IS NOT NULL AND acknowledged_at IS NULL)
        OR (delivery_state = 'acknowledged' AND claim_token <> '' AND claimed_at IS NOT NULL AND acknowledged_at IS NOT NULL)
        OR (delivery_state = 'stale' AND claim_token = '' AND claimed_at IS NULL AND acknowledged_at IS NULL)
    )
);

-- Old databases carry FK orphans: reports whose source session was deleted
-- while the row survived (all acknowledged, none pending — verified on live
-- data). The ledger's restored foreign keys reject them; they cannot be
-- delivered again and nothing re-owns them, so the rebuild leaves them (and
-- only their outputs) behind deliberately.
INSERT INTO deliveries (
    id, kind, session_id, project_id, state, note, message, created_at,
    delivery_state, available_at, settlement_deadline, repeat_count,
    claim_token, claimed_at, delivery_attempts, acknowledged_at, last_error,
    emitted_at, delivery_batch_id
)
SELECT
    id, 'report', session_id, project_id, state, note, message, created_at,
    delivery_state, available_at, settlement_deadline, repeat_count,
    claim_token, claimed_at, delivery_attempts, acknowledged_at, last_error,
    emitted_at, delivery_batch_id
FROM reports
WHERE session_id IN (SELECT id FROM sessions)
  AND project_id IN (SELECT id FROM projects);

CREATE TABLE report_outputs_migrated (
    report_id TEXT NOT NULL REFERENCES deliveries(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    kind TEXT NOT NULL CHECK (kind IN ('artifact', 'pr_created', 'pr_reviewed')),
    reference TEXT NOT NULL CHECK (length(trim(reference)) > 0),
    label TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (report_id, position)
);

INSERT INTO report_outputs_migrated (report_id, position, kind, reference, label)
SELECT report_id, position, kind, reference, label FROM report_outputs
WHERE report_id IN (SELECT id FROM deliveries);

DROP TABLE report_outputs;
DROP TABLE reports;

ALTER TABLE report_outputs_migrated RENAME TO report_outputs;

CREATE INDEX idx_deliveries_delivery
    ON deliveries(delivery_state, available_at, created_at, id);
CREATE INDEX idx_deliveries_session
    ON deliveries(session_id, created_at, id);
CREATE INDEX idx_deliveries_project_kind
    ON deliveries(project_id, kind, delivery_state, available_at);

-- CDC-триггер 0169 читал reports; пересоздаётся на леджер.
DROP TRIGGER IF EXISTS report_outputs_pr_created_cdc;
-- +goose StatementBegin
CREATE TRIGGER report_outputs_pr_created_cdc
AFTER INSERT ON report_outputs
WHEN NEW.kind = 'pr_created'
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT r.project_id, r.session_id, 'session_updated', json_object('id', r.session_id), r.created_at
    FROM deliveries r WHERE r.id = NEW.report_id;
END;
-- +goose StatementEnd

-- +goose Down

CREATE TABLE reports (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT '' CHECK (state IN ('', 'checkpoint', 'needs_input', 'stuck', 'done')),
    note TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 1000),
    message TEXT NOT NULL DEFAULT '' CHECK (length(message) <= 1000),
    created_at TIMESTAMP NOT NULL,
    delivery_state TEXT NOT NULL DEFAULT 'pending' CHECK (delivery_state IN ('pending', 'claimed', 'acknowledged')),
    available_at TIMESTAMP NOT NULL,
    settlement_deadline TIMESTAMP,
    repeat_count INTEGER NOT NULL DEFAULT 1 CHECK (repeat_count >= 1),
    claim_token TEXT NOT NULL DEFAULT '',
    claimed_at TIMESTAMP,
    delivery_attempts INTEGER NOT NULL DEFAULT 0 CHECK (delivery_attempts >= 0),
    acknowledged_at TIMESTAMP,
    last_error TEXT NOT NULL DEFAULT '',
    emitted_at TIMESTAMP,
    delivery_batch_id TEXT NOT NULL DEFAULT '',
    CHECK (message = '' OR (state = '' AND note = '')),
    CHECK ((state = '' AND note = '') OR (state <> '' AND length(trim(note)) > 0)),
    CHECK ((state = 'done' AND settlement_deadline IS NOT NULL) OR (state <> 'done' AND settlement_deadline IS NULL)),
    CHECK (
        (delivery_state = 'pending' AND claim_token = '' AND claimed_at IS NULL AND acknowledged_at IS NULL)
        OR (delivery_state = 'claimed' AND claim_token <> '' AND claimed_at IS NOT NULL AND acknowledged_at IS NULL)
        OR (delivery_state = 'acknowledged' AND claim_token <> '' AND claimed_at IS NOT NULL AND acknowledged_at IS NOT NULL)
    )
);

-- stale не существует в старой схеме: такие строки возвращаются в pending.
INSERT INTO reports (
    id, session_id, project_id, state, note, message, created_at,
    delivery_state, available_at, settlement_deadline, repeat_count,
    claim_token, claimed_at, delivery_attempts, acknowledged_at, last_error,
    emitted_at, delivery_batch_id
)
SELECT
    id, session_id, project_id, state, note, message, created_at,
    CASE delivery_state WHEN 'stale' THEN 'pending' ELSE delivery_state END,
    available_at, settlement_deadline, repeat_count,
    claim_token, claimed_at, delivery_attempts, acknowledged_at, last_error,
    emitted_at, delivery_batch_id
FROM deliveries;

CREATE TABLE report_outputs_migrated (
    report_id TEXT NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    kind TEXT NOT NULL CHECK (kind IN ('artifact', 'pr_created', 'pr_reviewed')),
    reference TEXT NOT NULL CHECK (length(trim(reference)) > 0),
    label TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (report_id, position)
);

INSERT INTO report_outputs_migrated (report_id, position, kind, reference, label)
SELECT report_id, position, kind, reference, label FROM report_outputs;

DROP TABLE report_outputs;
DROP TABLE deliveries;

ALTER TABLE report_outputs_migrated RENAME TO report_outputs;

CREATE INDEX idx_reports_delivery
    ON reports(delivery_state, available_at, created_at, id);
CREATE INDEX idx_reports_session
    ON reports(session_id, created_at, id);
CREATE INDEX idx_reports_project_delivery
    ON reports(project_id, delivery_state, created_at, id);

DROP TRIGGER IF EXISTS report_outputs_pr_created_cdc;
-- +goose StatementBegin
CREATE TRIGGER report_outputs_pr_created_cdc
AFTER INSERT ON report_outputs
WHEN NEW.kind = 'pr_created'
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT r.project_id, r.session_id, 'session_updated', json_object('id', r.session_id), r.created_at
    FROM reports r WHERE r.id = NEW.report_id;
END;
-- +goose StatementEnd
