package sessionmanager

import "time"

// spawnTimings attributes a spawn's wall time to its stages so the daemon log
// shows where a slow `ao spawn` spent it. Each mark records the time since the
// previous mark; work after the last mark shows only in totalMs.
type spawnTimings struct {
	start, last time.Time
	attrs       []any
	stages      []any
}

func newSpawnTimings() *spawnTimings {
	now := time.Now()
	return &spawnTimings{start: now, last: now}
}

// set adds a key/value attribute to the timings line.
func (t *spawnTimings) set(key string, value any) {
	t.attrs = append(t.attrs, key, value)
}

// mark closes the stage that started at the previous mark.
func (t *spawnTimings) mark(stage string) {
	now := time.Now()
	t.stages = append(t.stages, stage+"Ms", now.Sub(t.last).Milliseconds())
	t.last = now
}

// logSpawnTimings writes one line per spawn, including a failed one, so the
// stage a failure reached is visible next to how long it took to get there.
func (m *Manager) logSpawnTimings(t *spawnTimings, err error) {
	if m.logger == nil {
		return
	}
	args := make([]any, 0, len(t.attrs)+len(t.stages)+4)
	args = append(args, t.attrs...)
	args = append(args, "totalMs", time.Since(t.start).Milliseconds())
	args = append(args, t.stages...)
	if err != nil {
		args = append(args, "error", err)
	}
	m.logger.Info("spawn timings", args...)
}
