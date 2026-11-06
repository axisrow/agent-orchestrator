-- +goose Up
-- ListPRFactsBySession(s) reads each session's latest completed chat turn
-- (MAX(completed_at) filtered to settled, non-rolled-back turns) once per PR
-- row to decide whether an external changes request was already answered.
-- Without an index that filter walks the whole turns archive per read; the
-- partial index keeps one entry per completed turn. Building it scans the
-- whole table once, at the first start after upgrade.
CREATE INDEX idx_conversation_turns_session_completed
    ON conversation_turns(handled_by_session_id, completed_at)
    WHERE state = 'completed' AND rolled_back_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_conversation_turns_session_completed;
