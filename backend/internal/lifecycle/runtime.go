package lifecycle

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const defaultRecentActivityWindow = 60 * time.Second

func hasRecentActivity(a domain.Activity, now time.Time, window time.Duration) bool {
	// Sticky states (waiting_input/blocked) stay sticky for activity aging, but
	// not for death arbitration: stickiness must not turn the recent-activity
	// guard into an eternal veto over an unambiguous ProbeDead. An agent killed
	// while sitting at its prompt never emits another signal, so its last
	// waiting_input would otherwise block termination forever (issue #277).
	// The window still protects a live paused session from a single stale probe.
	switch {
	case a.State == domain.ActivityExited:
		return false
	case a.LastActivityAt.IsZero():
		return false
	default:
		return now.Sub(a.LastActivityAt) <= window
	}
}

func runtimeClearlyDead(f ports.RuntimeFacts, activity domain.Activity, now time.Time, window time.Duration) bool {
	observedAt := timeOr(f.ObservedAt, now)
	return f.Runtime == ports.ProbeDead && !hasRecentActivity(activity, observedAt, window)
}

func timeOr(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}
