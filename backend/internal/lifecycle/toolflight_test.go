package lifecycle

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// sig builds an event-tagged activity signal the way the ingress does.
func sig(state domain.ActivityState, event, toolName, toolUseID string) ports.ActivitySignal {
	return ports.ActivitySignal{Valid: true, State: state, Event: event, ToolName: toolName, ToolUseID: toolUseID}
}

// seedSignaled seeds a session that already produced signals (FirstSignalAt
// stamped), so receipt stamping does not interfere with precedence assertions.
func seedSignaled(st *fakeStore, id domain.SessionID, state domain.ActivityState) {
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer",
		Activity:      domain.Activity{State: state, LastActivityAt: time.Now()},
		FirstSignalAt: time.Now(),
	}
}

// mustApply applies a signal and fails the test on error.
func mustApply(t *testing.T, m *Manager, id domain.SessionID, s ports.ActivitySignal) {
	t.Helper()
	if err := m.ApplyActivitySignal(ctx, id, s); err != nil {
		t.Fatal(err)
	}
}

func stateOf(st *fakeStore, id domain.SessionID) domain.ActivityState {
	return st.sessions[id].Activity.State
}

// blockOnDialog drives a session into blocked through the real signal path:
// the blocking tool's pre-tool-use, then permission-request naming that tool.
func blockOnDialog(t *testing.T, m *Manager, st *fakeStore, id domain.SessionID, toolName, toolUseID string) {
	t.Helper()
	mustApply(t, m, id, sig(domain.ActivityActive, "pre-tool-use", toolName, toolUseID))
	mustApply(t, m, id, sig(domain.ActivityBlocked, "permission-request", toolName, ""))
	if got := stateOf(st, id); got != domain.ActivityBlocked {
		t.Fatalf("setup: state = %q, want blocked", got)
	}
}

func TestToolPrecedence_ApprovedToolPostClearsBlocked(t *testing.T) {
	// Approving the dialog fires no hook; the approved tool's own post is the
	// earliest observable resolution signal and must clear blocked -> active.
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after approved tool's post = %q, want active", got)
	}
}

func TestToolPrecedence_ApprovedToolFailurePostAlsoClears(t *testing.T) {
	// An approved tool that runs and FAILS still resolved the dialog.
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use-failure", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after approved tool's failure post = %q, want active", got)
	}
}

func TestToolPrecedence_SubagentTrafficDoesNotClearBlocked(t *testing.T) {
	// The failure that reverted the naive mapping in PR #5's review: parallel
	// subagent tool signals (same session id) land while the dialog is still
	// on screen. They must be tracked but never change the state.
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")

	// A different tool starts and finishes while the dialog is pending.
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "Read", "toolu_sub"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after subagent pre = %q, want blocked", got)
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Read", "toolu_sub"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after subagent post = %q, want blocked", got)
	}
	// The approved tool's post still clears afterwards.
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after approved post = %q, want active", got)
	}
}

func TestToolPrecedence_TurnBoundariesClearBlocked(t *testing.T) {
	// A prompt cannot be submitted and a turn cannot end while a dialog holds
	// the composer, so these events reliably mean the dialog is gone.
	cases := []struct {
		name string
		s    ports.ActivitySignal
		want domain.ActivityState
	}{
		{"user-prompt-submit", sig(domain.ActivityActive, "user-prompt-submit", "", ""), domain.ActivityActive},
		{"stop", sig(domain.ActivityIdle, "stop", "", ""), domain.ActivityIdle},
		{"session-end", sig(domain.ActivityExited, "session-end", "", ""), domain.ActivityExited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _ := newManager()
			seedSignaled(st, "mer-1", domain.ActivityActive)
			blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")
			mustApply(t, m, "mer-1", tc.s)
			if got := stateOf(st, "mer-1"); got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestToolPrecedence_NotificationSubtypesDoNotClearBlocked(t *testing.T) {
	// agent_completed (idle) and idle_prompt (waiting_input) arrive with
	// event "notification" and are no evidence the dialog closed — a
	// background `claude agents` run finishing must not unmask a live dialog
	// (the cycle-2 minor finding on PR #5).
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")

	mustApply(t, m, "mer-1", sig(domain.ActivityIdle, "notification", "", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after notification idle = %q, want blocked", got)
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityWaitingInput, "notification", "", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after notification waiting_input = %q, want blocked", got)
	}
}

func TestToolPrecedence_NoCandidatesFailsSafe(t *testing.T) {
	// A blocking signal that carries no tool identity (codex, a bare
	// Notification, or a daemon restarted mid-dialog) yields no candidates:
	// nothing tool-shaped may clear the block; the turn boundary still does.
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	// Blocked lands with NO prior pre and NO tool name (restart-equivalent).
	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "permission-request", "", ""))

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after uncorrelated post = %q, want blocked (fail safe)", got)
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityIdle, "stop", "", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityIdle {
		t.Fatalf("state after stop = %q, want idle", got)
	}
}

func TestToolPrecedence_LegacySignalsKeepLastWriterWins(t *testing.T) {
	// The compatibility pin: a signal WITHOUT an event (old CLIs, the 12
	// adapters that don't tag their signals) keeps today's last-writer-wins
	// semantics even out of blocked — the precedence rule must not change
	// behavior for anyone who doesn't opt in.
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")

	mustApply(t, m, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityActive})
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("legacy active over blocked = %q, want active (last-writer-wins preserved)", got)
	}
}

func TestToolPrecedence_ToolEventsDoNotDemoteWaitingInput(t *testing.T) {
	// waiting_input marks "the user's turn". Background subagent tool traffic
	// must not clear it; an explicit user signal does.
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityWaitingInput)

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "Read", "toolu_bg"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after background pre = %q, want waiting_input", got)
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Read", "toolu_bg"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after background post = %q, want waiting_input", got)
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "user-prompt-submit", "", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after user prompt = %q, want active", got)
	}
}

func TestToolPrecedence_SameNameSiblingPostClears_KnownResidual(t *testing.T) {
	// Documented limitation, pinned so a change here is deliberate: two
	// same-name tools in flight when the dialog appears both become
	// candidates, so the sibling's post clears the block early. The window is
	// narrow and the degradation equals the pre-guard behavior.
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "Bash", "toolu_1"))
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "Bash", "toolu_2"))
	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "permission-request", "Bash", ""))

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_2"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state = %q; the same-name sibling residual changed — update the PR notes if deliberate", got)
	}
}

func TestToolPrecedence_SuppressedSignalEmitsNoNotification(t *testing.T) {
	// A suppressed clear must not fan out: the session never left blocked, so
	// no needs-input exit/entry telemetry or notification may fire.
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")
	entered := len(sink.intents) // the blocked entry notification

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Read", "toolu_sub"))
	if len(sink.intents) != entered {
		t.Fatalf("suppressed signal emitted a notification: %d -> %d", entered, len(sink.intents))
	}
}
