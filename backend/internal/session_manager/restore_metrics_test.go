package sessionmanager

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type restoreTelemetryCapture struct{ events []ports.TelemetryEvent }

func (s *restoreTelemetryCapture) Emit(_ context.Context, ev ports.TelemetryEvent) {
	s.events = append(s.events, ev)
}

func (*restoreTelemetryCapture) Close(context.Context) error { return nil }

func TestRestoreTrigger_MapsOperationsToCategories(t *testing.T) {
	for _, tc := range []struct{ operation, want string }{
		{"restore", "daemon-start"},
		{"resume after update", "update-resume"},
		{"version refresh", "update-resume"},
		{"agent switch", "agent-switch"},
		{"agent switch rollback", "agent-switch"},
		{"switch interface", "interface-transition"},
		{"resume agent", "manual"},
		// Unknown operations pass through: a future relaunch caller stays
		// visible instead of masquerading as a known category.
		{"retry start", "retry start"},
	} {
		if got := restoreTrigger(tc.operation); got != tc.want {
			t.Fatalf("restoreTrigger(%q) = %q, want %q", tc.operation, got, tc.want)
		}
	}
}

func TestEmitRestored_LogsInfoAndEmitsEvent(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sink := &restoreTelemetryCapture{}
	m := &Manager{logger: logger}
	m.SetRestoreTelemetry(sink)
	rec := domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{AgentSessionID: "native-1"},
	}

	m.emitRestored(rec, RestoreModeNative, "daemon-start")

	line := buf.String()
	if !strings.Contains(line, "level=INFO") || !strings.Contains(line, "session restored") {
		t.Fatalf("restore log = %q, want INFO session restored", line)
	}
	if len(sink.events) != 1 {
		t.Fatalf("events = %#v, want one ao.session.restored", sink.events)
	}
	ev := sink.events[0]
	if ev.Name != EventSessionRestored || ev.Source != "session_manager" || ev.Level != ports.TelemetryLevelInfo {
		t.Fatalf("event header = %#v", ev)
	}
	if ev.SessionID == nil || *ev.SessionID != domain.SessionID("mer-1") || ev.ProjectID == nil || *ev.ProjectID != domain.ProjectID("mer") {
		t.Fatalf("event ids = %#v", ev)
	}
	if ev.OccurredAt.Location() != time.UTC {
		t.Fatalf("occurred location = %v, want UTC", ev.OccurredAt.Location())
	}
	if ev.Payload["mode"] != "native" || ev.Payload["trigger"] != "daemon-start" ||
		ev.Payload["nativeSessionId"] != "native-1" || ev.Payload["harness"] != "claude-code" {
		t.Fatalf("payload = %#v", ev.Payload)
	}

	// A nil sink drops the event without logging an error.
	buf.Reset()
	m2 := &Manager{logger: logger}
	m2.emitRestored(rec, RestoreModeNative, "manual")
	if buf.String() == "" || strings.Contains(buf.String(), "level=ERROR") {
		t.Fatalf("nil-sink emit log = %q", buf.String())
	}
}

// A successful native resume leaves the observable trace: one INFO line and
// one ao.session.restored event naming the real trigger (#109).
func TestResumeAgentWithMode_EmitsRestoredTelemetry(t *testing.T) {
	baseRuntime := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
	runtime := &fakeRestartRuntime{fakeRuntime: baseRuntime}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}
	m, _, _ := newExitedResumeManager(t, runtime, agent)
	sink := &restoreTelemetryCapture{}
	m.SetRestoreTelemetry(sink)

	if _, err := m.ResumeAgentWithMode(context.Background(), "mer-1"); err != nil {
		t.Fatal(err)
	}

	if len(sink.events) != 1 {
		t.Fatalf("events = %#v, want one ao.session.restored", sink.events)
	}
	ev := sink.events[0]
	if ev.Name != EventSessionRestored || ev.Payload["trigger"] != "manual" || ev.Payload["mode"] != "native" {
		t.Fatalf("event = %#v, want restored native manual", ev)
	}
	if ev.Payload["nativeSessionId"] != "agent-x" {
		t.Fatalf("nativeSessionId = %#v, want agent-x", ev.Payload["nativeSessionId"])
	}
}
