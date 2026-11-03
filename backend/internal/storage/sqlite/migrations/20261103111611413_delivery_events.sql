-- Строгий учёт доставок (fork, #242 срез 1): append-only журнал переходов
-- delivery_events. Каждая строка — один переход леджера deliveries с причиной
-- (cause-класс + сырой reason): судьба любого отчёта восстанавливается из
-- журнала одним запросом, «просто пропало» — невозможное состояние.
--
-- Внешних ключей нет намеренно: deliveries-строки уходят каскадом вместе с
-- сессиями и проектами, журнал — аудит и обязан переживать строку леджера.
-- Очистка возможна только для архивных записей за пределами окна учёта и
-- выполняется явным обслуживанием, а не конвейером демона.
-- +goose Up

CREATE TABLE delivery_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    delivery_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    from_state TEXT NOT NULL DEFAULT '' CHECK (from_state IN ('', 'pending', 'claimed', 'acknowledged', 'stale')),
    to_state TEXT NOT NULL CHECK (to_state IN ('pending', 'claimed', 'acknowledged', 'stale')),
    cause TEXT NOT NULL CHECK (length(trim(cause)) > 0),
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX idx_delivery_events_delivery
    ON delivery_events(delivery_id, id);
CREATE INDEX idx_delivery_events_project_time
    ON delivery_events(project_id, created_at, id);

-- Append-only на уровне БД: журнал не редактируется и не стирается кодом.
-- +goose StatementBegin
CREATE TRIGGER delivery_events_append_only_update
BEFORE UPDATE ON delivery_events
BEGIN
    SELECT RAISE(ABORT, 'delivery_events is append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER delivery_events_append_only_delete
BEFORE DELETE ON delivery_events
BEGIN
    SELECT RAISE(ABORT, 'delivery_events is append-only');
END;
-- +goose StatementEnd

-- Гард аудита леджера: недоставленная работа не может исчезнуть молча —
-- строка deliveries в pending/claimed не удаляется, а получает терминальный
-- статус (событие в журнале). Терминальные строки (acknowledged/stale)
-- удаляются вместе с родительскими сессиями/проектами как раньше.
-- +goose StatementBegin
CREATE TRIGGER deliveries_no_delete_undelivered
BEFORE DELETE ON deliveries
WHEN OLD.delivery_state IN ('pending', 'claimed')
BEGIN
    SELECT RAISE(ABORT, 'undelivered deliveries must be terminalized, never deleted');
END;
-- +goose StatementEnd

-- Backfill: у каждой живой строки леджера есть хотя бы одно событие — снимок
-- её текущего состояния на момент внедрения журнала. Промежуточная история
-- старых строк неизвестна и честно помечена cause 'backfill'; новые строки
-- пишут события из store-переходов.
INSERT INTO delivery_events (delivery_id, session_id, project_id, from_state, to_state, cause, reason, created_at)
SELECT id, session_id, project_id, '', delivery_state, 'backfill', last_error, created_at
FROM deliveries;

-- +goose Down

DROP TRIGGER IF EXISTS deliveries_no_delete_undelivered;
DROP TRIGGER IF EXISTS delivery_events_append_only_delete;
DROP TRIGGER IF EXISTS delivery_events_append_only_update;

DROP TABLE delivery_events;
