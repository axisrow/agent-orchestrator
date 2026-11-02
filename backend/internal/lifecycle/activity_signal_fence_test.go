package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestActivitySignalExpectedHarnessFencesStateAndMetadata(t *testing.T) {
	for _, state := range []bool{false, true} {
		store := newFakeStore()
		store.sessions["mer-1"] = domain.SessionRecord{
			ID: "mer-1", Harness: domain.HarnessCodex,
			Activity: domain.Activity{State: domain.ActivityIdle},
			Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1", AgentSessionID: "codex-native"},
		}
		m := New(store, nil)
		if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
			Valid: state, State: domain.ActivityActive, LaunchID: "launch-1",
			AgentSessionID: "pi-native", ExpectedHarness: domain.HarnessPi,
		}); err != nil {
			t.Fatal(err)
		}
		got := store.sessions["mer-1"]
		if got.Activity.State != domain.ActivityIdle || got.Metadata.AgentSessionID != "codex-native" {
			t.Fatalf("pi report changed another harness: %+v", got)
		}
	}
}

func TestActivitySignalExpectedHarnessFencesPendingNativeMetadata(t *testing.T) {
	store := newFakeAgentSwitchLifecycleStore()
	ref := domain.AgentNativeSessionID("target-native")
	store.native[ref] = domain.AgentNativeSession{
		ID: ref, AOSessionID: "mer-1", Harness: domain.HarnessCodex, LastGenerationID: "target-generation",
	}
	store.setSession(domain.SessionRecord{ID: "mer-1", Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "source-generation"}})
	store.setActiveSwitch(domain.AgentSwitch{ID: "switch-1", SessionID: "mer-1",
		TargetHarness: domain.HarnessCodex, State: domain.AgentSwitchStartingTarget,
		TargetGenerationID: "target-generation", TargetNativeSessionRef: &ref})
	m := New(store, nil)
	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
		LaunchID: "target-generation", AgentSessionID: "pi-native", ExpectedHarness: domain.HarnessPi,
	}); err != nil {
		t.Fatal(err)
	}
	if got := store.native[ref].NativeSessionID; got != "" {
		t.Fatalf("pi report staged metadata for another target harness: %q", got)
	}
}

// Current reports must survive the startup barrier. Validating the current
// source harness before ReleaseLaunch would drop the new process's report.
func TestActivitySignalExpectedHarnessWaitsForOwnershipTransfer(t *testing.T) {
	store := newFakeAgentSwitchLifecycleStore()
	store.setSession(domain.SessionRecord{ID: "mer-1", Harness: domain.HarnessCodex,
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "old-generation"}})
	m := New(store, nil)
	if err := m.PrepareLaunch("mer-1", "pi-generation"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
			Valid: true, State: domain.ActivityWaitingInput, LaunchID: "pi-generation",
			AgentSessionID: "pi-native", ExpectedHarness: domain.HarnessPi,
		})
	}()
	select {
	case err := <-done:
		t.Fatalf("report returned before ownership transfer: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	store.setSession(domain.SessionRecord{ID: "mer-1", Harness: domain.HarnessPi,
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "pi-generation"}})
	m.ReleaseLaunch("mer-1", "pi-generation")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := store.session("mer-1")
	if got.Metadata.AgentSessionID != "pi-native" || got.Activity.State != domain.ActivityWaitingInput {
		t.Fatalf("current pi report lost after ownership transfer: %+v", got)
	}
}
