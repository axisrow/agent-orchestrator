package usage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Claude 2.1.319 fires SessionStart/UserPromptSubmit before it creates the
// transcript file its own payload names (#300). The hook must degrade — no
// source registration for a file that does not exist — not drop the event:
// the binding still lands, the failure counters say so, and the next hook
// that finds the file registers it and ingests from byte 0.
func TestCollectorHookDegradesWhenTheArtifactIsAbsent(t *testing.T) {
	store := collectorTestStore(t)
	const nativeID = "native-absent-artifact"
	session := collectorTestSession(t, store, domain.HarnessClaudeCode, nativeID, false)
	root := filepath.Join(t.TempDir(), "projects")
	absent := filepath.Join(root, "workspace", nativeID+".jsonl")
	collector := NewCollector(store, SourceRoots{ClaudeProjects: root}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness:         domain.HarnessClaudeCode,
		Event:           "session-start",
		NativeSessionID: nativeID,
		TranscriptPath:  absent,
	}))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 0 {
		t.Fatalf("sources=%+v err=%v, want none for an absent artifact", sources, err)
	}
	metrics := collector.HookMetricsSnapshot()
	if metrics["hookArtifactsMissing"] != 1 || metrics["hookFailures"] != 0 {
		t.Fatalf("metrics=%v, want one degraded artifact and no dropped events", metrics)
	}

	// The transcript appears; the next hook registers it from byte 0, so the
	// events written before registration are collected, not lost.
	writeUsageFixture(t, absent, "{}\n")
	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness:         domain.HarnessClaudeCode,
		Event:           "post-tool-use",
		NativeSessionID: nativeID,
		TranscriptPath:  absent,
	}))
	sources, err = store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources after the file appeared=%+v err=%v", sources, err)
	}
	metrics = collector.HookMetricsSnapshot()
	if metrics["hookArtifactsMissing"] != 1 || metrics["hookFailures"] != 0 {
		t.Fatalf("metrics=%v, want the second hook to process cleanly", metrics)
	}
}

// A subagent artifact that is absent must not drop the main artifact: Claude
// 2.1.319 names subagents/ paths it no longer writes on every SubagentStop.
func TestCollectorHookKeepsTheMainArtifactWhenTheSubagentArtifactIsAbsent(t *testing.T) {
	store := collectorTestStore(t)
	const nativeID = "native-subagent-absent"
	session := collectorTestSession(t, store, domain.HarnessClaudeCode, nativeID, false)
	root := filepath.Join(t.TempDir(), "projects")
	main := filepath.Join(root, "workspace", nativeID+".jsonl")
	writeUsageFixture(t, main, "{}\n")
	collector := NewCollector(store, SourceRoots{ClaudeProjects: root}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness:                domain.HarnessClaudeCode,
		Event:                  "subagent-stop",
		NativeSessionID:        nativeID,
		TranscriptPath:         main,
		SubagentID:             "agent-1",
		SubagentTranscriptPath: filepath.Join(root, "workspace", nativeID, "subagents", "agent-1.jsonl"),
	}))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceClaudeMain {
		t.Fatalf("sources=%+v err=%v, want the main source only", sources, err)
	}
	metrics := collector.HookMetricsSnapshot()
	if metrics["hookArtifactsMissing"] != 1 || metrics["hookFailures"] != 0 {
		t.Fatalf("metrics=%v, want one degraded subagent artifact and no dropped events", metrics)
	}
}

// Path rejection is a policy boundary, not an absence: a hook naming an
// existing transcript outside the allowed roots must keep failing the event.
func TestCollectorHookStillRejectsPathsOutsideTheAllowedRoots(t *testing.T) {
	store := collectorTestStore(t)
	const nativeID = "native-path-rejected"
	session := collectorTestSession(t, store, domain.HarnessClaudeCode, nativeID, false)
	outside := filepath.Join(t.TempDir(), "elsewhere", nativeID+".jsonl")
	writeUsageFixture(t, outside, "{}\n")
	collector := NewCollector(store, SourceRoots{ClaudeProjects: filepath.Join(t.TempDir(), "projects")}, nil)

	if err := collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness:         domain.HarnessClaudeCode,
		Event:           "session-start",
		NativeSessionID: nativeID,
		TranscriptPath:  outside,
	}); err == nil {
		t.Fatal("a transcript outside the allowed roots must not register")
	}
	metrics := collector.HookMetricsSnapshot()
	if metrics["hookFailures"] != 1 || metrics["hookArtifactsMissing"] != 0 {
		t.Fatalf("metrics=%v, want one dropped event and no degraded artifacts", metrics)
	}
}

// Healthy hooks stay clean: no counters move when every artifact exists.
func TestCollectorHealthyHookFlowLeavesCountersAtZero(t *testing.T) {
	store := collectorTestStore(t)
	const nativeID = "native-healthy"
	session := collectorTestSession(t, store, domain.HarnessClaudeCode, nativeID, false)
	root := filepath.Join(t.TempDir(), "projects")
	main := filepath.Join(root, "workspace", nativeID+".jsonl")
	writeUsageFixture(t, main, "{}\n")
	collector := NewCollector(store, SourceRoots{ClaudeProjects: root}, nil)
	now := time.Unix(1700000000, 0).UTC()
	collector.now = func() time.Time { return now }

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness:         domain.HarnessClaudeCode,
		Event:           "user-prompt-submit",
		NativeSessionID: nativeID,
		TranscriptPath:  main,
	}))
	metrics := collector.HookMetricsSnapshot()
	if metrics["hookFailures"] != 0 || metrics["hookArtifactsMissing"] != 0 {
		t.Fatalf("metrics=%v, want both counters at zero", metrics)
	}
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources=%+v err=%v, want the registered main source", sources, err)
	}
}
