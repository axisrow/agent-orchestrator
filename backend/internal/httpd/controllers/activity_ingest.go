package controllers

import (
	"context"
	"log/slog"
	"runtime/debug"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// activityQueueSize bounds the ordered ingest queue in front of the lifecycle
// reducer. The daemon's observed hook arrival rate is a few signals per second
// and the reducer applies one in tens of milliseconds, so the queue only fills
// when the store itself is wedged; at that point dropping the newest signal
// (the next hook re-reports the state) beats blocking every agent process
// behind a stalled write.
const activityQueueSize = 1024

// ActivityIngest decouples the activity HTTP boundary from the store. A hook
// request validates and queues its signal, then answers; one applier reduces
// signals in arrival order through the same recorder (the hook-process gate and
// lifecycle Manager). Without this, every POST runs the gate's session read and
// the reducer's fenced writes inline, so one slow store operation — a bulk
// writer holding the single SQLite writer, a WAL checkpoint, a busy reader
// pool — head-of-line blocks the hooks of every session behind one global
// lifecycle lock, for as long as the stall lasts.
type ActivityIngest struct {
	recorder ActivityRecorder
	signals  chan activityIngestItem
	log      *slog.Logger
}

type activityIngestItem struct {
	id     domain.SessionID
	signal ports.ActivitySignal
}

// NewActivityIngest wraps apply with the ordered ingest queue. Run must be
// started for queued signals to be applied.
func NewActivityIngest(apply ActivityRecorder, log *slog.Logger) *ActivityIngest {
	if log == nil {
		log = slog.Default()
	}
	return &ActivityIngest{
		recorder: apply,
		signals:  make(chan activityIngestItem, activityQueueSize),
		log:      log,
	}
}

// ApplyActivitySignal queues the signal and returns at once. A full queue
// surfaces the projection-busy contract to the caller (the handler answers 503
// with Retry-After) instead of silently dropping the fact.
func (q *ActivityIngest) ApplyActivitySignal(_ context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	select {
	case q.signals <- activityIngestItem{id: id, signal: s}:
		return nil
	default:
		q.log.Warn("activity ingest queue full; dropping signal", "session", id, "event", s.Event)
		return ports.ErrActivityProjectionContention
	}
}

// Run applies queued signals in arrival order until ctx is done. Applies run
// detached from the hook client's connection lifetime — a client that gives up
// must not abort a half-committed projection (#259) — under the same generous
// activityHookBudget the inline projection already lived with, so the window a
// stalled store has to commit the projection is unchanged. A panic on the
// apply path (gate, reducer, driver) fails that one signal and is logged, the
// way a panic inside the HTTP request used to kill only the connection.
func (q *ActivityIngest) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-q.signals:
			q.apply(item)
		}
	}
}

func (q *ActivityIngest) apply(item activityIngestItem) {
	defer func() {
		if p := recover(); p != nil {
			q.log.Warn("activity signal apply panicked",
				"session", item.id, "event", item.signal.Event,
				"panic", p, "stack", string(debug.Stack()))
		}
	}()
	applyCtx, cancel := context.WithTimeout(context.Background(), activityHookBudget)
	defer cancel()
	if err := q.recorder.ApplyActivitySignal(applyCtx, item.id, item.signal); err != nil {
		q.log.Warn("activity signal apply failed",
			"session", item.id, "event", item.signal.Event, "err", err)
	}
}
