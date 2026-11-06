package session

import (
	"encoding/json"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DefaultTurnStallAfter is how long an active turn may show no signs of life
// before reads flag it as stalled. It sits far below the gateway's ~60-minute
// API-call ceiling (issue #281's live example: a worker turn hung 60m16s while
// the UI showed plain "Working") and far above any honest streaming pause.
const DefaultTurnStallAfter = 10 * time.Minute

// turnActivityFacts is the liveness slice of the native hook-fact blob the
// lifecycle manager stores on the session row (ClaudeActivityFacts /
// CodexActivityFacts). Only the timestamps matter here; unknown fields are
// ignored, and a damaged blob degrades to "no hook liveness" rather than
// failing the read.
type turnActivityFacts struct {
	ParentAt    int64 `json:"parentAt"`
	LastChildAt int64 `json:"lastChildAt"`
	SnapshotAt  int64 `json:"snapshotAt"`
}

// hookFactLiveness returns the newest native hook-fact timestamp on the
// record, or zero. Parent turn hooks (prompt, tool boundaries) rewrite the
// blob on every event, so a TUI turn working through tools keeps advancing it
// even though the durable activity state stays "active".
func hookFactLiveness(rec domain.SessionRecord) time.Time {
	var blob string
	switch rec.Harness {
	case domain.HarnessClaudeCode:
		blob = rec.Metadata.ClaudeActivityFacts
	case domain.HarnessCodex:
		blob = rec.Metadata.CodexActivityFacts
	default:
		return time.Time{}
	}
	if blob == "" {
		return time.Time{}
	}
	var facts turnActivityFacts
	if err := json.Unmarshal([]byte(blob), &facts); err != nil {
		return time.Time{}
	}
	var newest time.Time
	for _, at := range []int64{facts.ParentAt, facts.LastChildAt, facts.SnapshotAt} {
		if at <= 0 {
			continue
		}
		if t := time.Unix(0, at); t.After(newest) {
			newest = t
		}
	}
	return newest
}

// deriveTurnStall reports whether the session's current turn has gone silent:
// the durable state is active but no sign of life — a conversation write
// (Chat streaming deltas and tool events), a native hook fact (TUI tool
// boundaries), or the activity transition that started the turn — has been
// observed for threshold. The second return is the silence duration in
// milliseconds, zero unless stalled. Read-time only: the activity state is
// never rewritten and nothing is killed on the strength of this.
func deriveTurnStall(rec domain.SessionRecord, lastConversationWrite, now time.Time, threshold time.Duration) (bool, int64) {
	if rec.Activity.State != domain.ActivityActive || rec.Activity.LastActivityAt.IsZero() {
		return false, 0
	}
	if threshold <= 0 {
		threshold = DefaultTurnStallAfter
	}
	newest := rec.Activity.LastActivityAt
	if lastConversationWrite.After(newest) {
		newest = lastConversationWrite
	}
	if hook := hookFactLiveness(rec); hook.After(newest) {
		newest = hook
	}
	silent := now.Sub(newest)
	if silent < threshold {
		return false, 0
	}
	return true, silent.Milliseconds()
}
