package session

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func stallRecord(state domain.ActivityState, turnStartedAt time.Time) domain.SessionRecord {
	return domain.SessionRecord{
		Harness:  domain.HarnessClaudeCode,
		Mode:     domain.SessionModeChat,
		Activity: domain.Activity{State: state, LastActivityAt: turnStartedAt},
	}
}

// A chat turn parked on a hung API call has no conversation writes after the
// turn started; past the threshold the read flags it and reports the silence.
func TestDeriveTurnStallFlagsSilentActiveTurnPastThreshold(t *testing.T) {
	started := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	now := started.Add(10 * time.Minute)
	stalled, silentMs, _ := deriveTurnStall(stallRecord(domain.ActivityActive, started), time.Time{}, now, 10*time.Minute)
	if !stalled || silentMs != int64(10*time.Minute/time.Millisecond) {
		t.Fatalf("stalled=%v silentMs=%d, want true at exactly the threshold", stalled, silentMs)
	}
}

// An honest streaming pause inside the threshold never flags.
func TestDeriveTurnStallKeepsShortStreamingPauseLive(t *testing.T) {
	started := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	now := started.Add(9 * time.Minute)
	stalled, silentMs, _ := deriveTurnStall(stallRecord(domain.ActivityActive, started), time.Time{}, now, 10*time.Minute)
	if stalled || silentMs != 0 {
		t.Fatalf("stalled=%v silentMs=%d, want false below the threshold", stalled, silentMs)
	}
}

// A streaming delta (or tool event) that lands after the turn started resets
// the silence clock to that write.
func TestDeriveTurnStallResetsOnConversationWrite(t *testing.T) {
	started := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	lastWrite := started.Add(9 * time.Minute)
	now := started.Add(15 * time.Minute)
	if stalled, _, _ := deriveTurnStall(stallRecord(domain.ActivityActive, started), lastWrite, now, 10*time.Minute); stalled {
		t.Fatal("a turn with a fresh conversation write must not stall")
	}
	stalled, silentMs, _ := deriveTurnStall(stallRecord(domain.ActivityActive, started), lastWrite, lastWrite.Add(11*time.Minute), 10*time.Minute)
	if !stalled {
		t.Fatal("silence measured from the last write must eventually stall")
	}
	if silentMs != int64(11*time.Minute/time.Millisecond) {
		t.Fatalf("silentMs=%d, want 660000", silentMs)
	}
}

// A completed turn cleans up after itself: nothing is flagged once the state
// leaves active.
func TestDeriveTurnStallIgnoresNonActiveTurns(t *testing.T) {
	started := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	now := started.Add(time.Hour)
	for _, state := range []domain.ActivityState{domain.ActivityIdle, domain.ActivityWaitingInput, domain.ActivityBlocked, domain.ActivityExited, ""} {
		if stalled, silentMs, _ := deriveTurnStall(stallRecord(state, started), time.Time{}, now, 10*time.Minute); stalled || silentMs != 0 {
			t.Fatalf("state %s: stalled=%v silentMs=%d, want false", state, stalled, silentMs)
		}
	}
}

// A zero turn-start timestamp (no activity observed yet) never stalls.
func TestDeriveTurnStallIgnoresZeroTurnStart(t *testing.T) {
	rec := stallRecord(domain.ActivityActive, time.Time{})
	rec.Activity.LastActivityAt = time.Time{}
	if stalled, _, _ := deriveTurnStall(rec, time.Time{}, time.Now(), 10*time.Minute); stalled {
		t.Fatal("a turn with no observed start must not stall")
	}
}

// TUI turns leave no conversation rows; the native hook-fact blob is their
// sign of life. Tools keep it advancing; a silent turn freezes it at the
// prompt.
func TestDeriveTurnStallUsesHookFactsForTUI(t *testing.T) {
	started := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	rec := stallRecord(domain.ActivityActive, started)
	rec.Mode = domain.SessionModeTUI

	// No hook blob at all: the turn-start timestamp alone clocks the silence.
	if stalled, _, _ := deriveTurnStall(rec, time.Time{}, started.Add(11*time.Minute), 10*time.Minute); !stalled {
		t.Fatal("a TUI turn with no hook traffic must stall past the threshold")
	}

	// A tool boundary 9 minutes in keeps the turn live 11 minutes after start.
	rec.Metadata.ClaudeActivityFacts = mustJSON(t, turnActivityFacts{ParentAt: started.Add(9 * time.Minute).UnixNano()})
	if stalled, _, _ := deriveTurnStall(rec, time.Time{}, started.Add(11*time.Minute), 10*time.Minute); stalled {
		t.Fatal("a recent hook fact must keep the turn live")
	}
	stalled, _, _ := deriveTurnStall(rec, time.Time{}, started.Add(20*time.Minute), 10*time.Minute)
	if !stalled {
		t.Fatal("silence measured from the last hook fact must eventually stall")
	}

	// A damaged blob degrades to no hook liveness instead of failing the read.
	rec.Metadata.ClaudeActivityFacts = "{not json"
	if stalled, _, _ := deriveTurnStall(rec, time.Time{}, started.Add(11*time.Minute), 10*time.Minute); !stalled {
		t.Fatal("a damaged hook blob must not keep the turn alive")
	}
}

// Codex hook facts live in their own column and count the same way.
func TestDeriveTurnStallUsesCodexHookFacts(t *testing.T) {
	started := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	rec := stallRecord(domain.ActivityActive, started)
	rec.Mode = domain.SessionModeTUI
	rec.Harness = domain.HarnessCodex
	rec.Metadata.CodexActivityFacts = mustJSON(t, turnActivityFacts{ParentAt: started.Add(9 * time.Minute).UnixNano()})
	if stalled, _, _ := deriveTurnStall(rec, time.Time{}, started.Add(11*time.Minute), 10*time.Minute); stalled {
		t.Fatal("a recent Codex hook fact must keep the turn live")
	}
}

// Issue #344: a tool call whose pre-tool-use hook fired but whose post has not
// arrived is a live turn blocked on real work, however long it runs — no
// "no signs of life", and the read names the call with its elapsed time. The
// same silence with no in-flight call stays a real stall.
func TestDeriveTurnStallKeepsInFlightToolCallLive(t *testing.T) {
	started := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	now := started.Add(43 * time.Minute)
	rec := stallRecord(domain.ActivityActive, started)
	rec.Mode = domain.SessionModeTUI
	rec.Metadata.ClaudeActivityFacts = mustJSON(t, turnActivityFacts{
		ParentAt: started.Add(time.Minute).UnixNano(),
		InflightTools: map[string]turnToolFlight{
			"toolu_01": {Name: "Bash", At: started.Add(time.Minute).UnixNano()},
		},
	})

	stalled, silentMs, inflight := deriveTurnStall(rec, time.Time{}, now, 10*time.Minute)
	if stalled || silentMs != 0 {
		t.Fatalf("stalled=%v silentMs=%d, want a live turn while the call runs", stalled, silentMs)
	}
	if inflight == nil || inflight.Name != "Bash" || inflight.Running != 42*time.Minute {
		t.Fatalf("inflight=%+v, want Bash running 42m", inflight)
	}

	// The same silence with no in-flight call is genuine idleness.
	rec.Metadata.ClaudeActivityFacts = mustJSON(t, turnActivityFacts{ParentAt: started.Add(time.Minute).UnixNano()})
	stalled, silentMs, inflight = deriveTurnStall(rec, time.Time{}, now, 10*time.Minute)
	if !stalled || inflight != nil || silentMs != int64(42*time.Minute/time.Millisecond) {
		t.Fatalf("stalled=%v silentMs=%d inflight=%v, want a stall once the call is gone", stalled, silentMs, inflight)
	}
}

// Codex sessions read their in-flight map from the Codex column the same way.
func TestDeriveTurnStallKeepsCodexInFlightToolCallLive(t *testing.T) {
	started := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	rec := stallRecord(domain.ActivityActive, started)
	rec.Mode = domain.SessionModeTUI
	rec.Harness = domain.HarnessCodex
	rec.Metadata.CodexActivityFacts = mustJSON(t, turnActivityFacts{
		InflightTools: map[string]turnToolFlight{
			"call_1": {Name: "shell", At: started.UnixNano()},
		},
	})
	stalled, _, inflight := deriveTurnStall(rec, time.Time{}, started.Add(time.Hour), 10*time.Minute)
	if stalled || inflight == nil || inflight.Name != "shell" {
		t.Fatalf("stalled=%v inflight=%+v, want a live Codex turn naming the call", stalled, inflight)
	}
}

// A damaged blob must not fabricate an in-flight call.
func TestInflightToolCallDegradesOnDamagedBlob(t *testing.T) {
	rec := stallRecord(domain.ActivityActive, time.Now())
	rec.Metadata.ClaudeActivityFacts = "{not json"
	if _, _, live := inflightToolCall(rec, time.Now()); live {
		t.Fatal("a damaged hook blob must not report an in-flight call")
	}
}

// The strongest of the three hook timestamps wins: child activity proves life
// even when the parent timestamp is older.
func TestHookFactLivenessTakesNewestTimestamp(t *testing.T) {
	base := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	rec := domain.SessionRecord{Harness: domain.HarnessClaudeCode}
	rec.Metadata.ClaudeActivityFacts = mustJSON(t, turnActivityFacts{
		ParentAt:    base.UnixNano(),
		LastChildAt: base.Add(5 * time.Minute).UnixNano(),
		SnapshotAt:  base.Add(2 * time.Minute).UnixNano(),
	})
	if got := hookFactLiveness(rec); !got.Equal(base.Add(5 * time.Minute)) {
		t.Fatalf("hookFactLiveness = %v, want the newest child timestamp", got)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	blob, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal facts: %v", err)
	}
	return string(blob)
}

// The shipped default stays well inside the two bounds issue #281 names:
// far below the gateway's ~60-minute hang, far above an honest streaming
// pause.
func TestDefaultTurnStallAfterBounds(t *testing.T) {
	if DefaultTurnStallAfter < 5*time.Minute || DefaultTurnStallAfter > 15*time.Minute {
		t.Fatalf("DefaultTurnStallAfter = %s, want ~10 minutes", DefaultTurnStallAfter)
	}
}
