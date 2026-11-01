package lifecycle

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A resume relaunch confirms the native conversation id under a NEW
// RuntimeLaunchID (#109). The SessionStart-family trail must survive as
// append-only telemetry rows, carrying the provider's own sub-source, or a
// manual `claude --resume` outside the panel is unrecoverable after the fact.
func TestActivity_SessionStartEmitsNativeIdentityTrail(t *testing.T) {
	st := newFakeStore()
	sink := &telemetrySink{}
	m := New(st, nil, WithTelemetry(sink))
	now := time.Unix(100, 0).UTC()
	m.clock = func() time.Time { return now }
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Metadata: domain.SessionMetadata{
			RuntimeLaunchID:        "launch-2",
			AgentSessionID:         "old-native",
			AgentSessionIDLaunchID: "launch-1",
		},
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-time.Minute)},
	}

	err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
		Valid:          true,
		State:          domain.ActivityIdle,
		Event:          "session-start",
		AgentSessionID: "new-native",
		LaunchID:       "launch-2",
		HookSource:     "resume",
		Timestamp:      now,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(sink.events) != 2 {
		t.Fatalf("events = %#v, want native_session_start + native_identity_confirmed", sink.events)
	}
	start, confirmed := sink.events[0], sink.events[1]
	if start.Name != "ao.session.native_session_start" || confirmed.Name != "ao.session.native_identity_confirmed" {
		t.Fatalf("event names = %q, %q", start.Name, confirmed.Name)
	}
	for _, ev := range sink.events {
		if ev.Source != "lifecycle" || ev.Level != ports.TelemetryLevelInfo {
			t.Fatalf("%s source/level = %q/%v", ev.Name, ev.Source, ev.Level)
		}
		if ev.SessionID == nil || *ev.SessionID != domain.SessionID("mer-1") {
			t.Fatalf("%s session = %#v", ev.Name, ev.SessionID)
		}
		if got := ev.Payload["agentSessionId"]; got != "new-native" {
			t.Fatalf("%s agentSessionId = %#v", ev.Name, got)
		}
		if got := ev.Payload["launchId"]; got != "launch-2" {
			t.Fatalf("%s launchId = %#v", ev.Name, got)
		}
		if got := ev.Payload["source"]; got != "resume" {
			t.Fatalf("%s source = %#v", ev.Name, got)
		}
	}
	if got := confirmed.Payload["hookEvent"]; got != "session-start" {
		t.Fatalf("identity hookEvent = %#v", got)
	}
}

// The first SessionStart of a never-resumed spawn confirms its own launch once;
// a session-start under the CURRENT launch is an ordinary start and must not
// re-emit the identity confirmation.
func TestActivity_SessionStartSameLaunchSkipsIdentityConfirmation(t *testing.T) {
	st := newFakeStore()
	sink := &telemetrySink{}
	m := New(st, nil, WithTelemetry(sink))
	now := time.Unix(100, 0).UTC()
	m.clock = func() time.Time { return now }
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Metadata: domain.SessionMetadata{
			RuntimeLaunchID:        "launch-1",
			AgentSessionID:         "native-1",
			AgentSessionIDLaunchID: "launch-1",
		},
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-time.Minute)},
	}

	err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
		Valid:          true,
		State:          domain.ActivityIdle,
		Event:          "session-start",
		AgentSessionID: "native-1",
		LaunchID:       "launch-1",
		HookSource:     "startup",
		Timestamp:      now,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(sink.events) != 1 {
		t.Fatalf("events = %#v, want only native_session_start", sink.events)
	}
	if sink.events[0].Name != "ao.session.native_session_start" {
		t.Fatalf("event = %q", sink.events[0].Name)
	}
}

// A suppressed or non-committing signal must leave no trail.
func TestActivity_IdentityTrailRequiresCommit(t *testing.T) {
	st := newFakeStore()
	sink := &telemetrySink{}
	m := New(st, nil, WithTelemetry(sink))
	now := time.Unix(100, 0).UTC()
	m.clock = func() time.Time { return now }
	// ExpectedRevision fence: the signal is rejected before projection.
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Revision:  7,
		Metadata:  domain.SessionMetadata{RuntimeLaunchID: "launch-2"},
	}

	err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
		Valid:            true,
		State:            domain.ActivityIdle,
		Event:            "session-start",
		AgentSessionID:   "native-1",
		LaunchID:         "launch-2",
		ExpectedRevision: func(v int64) *int64 { return &v }(3),
		Timestamp:        now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("events = %#v, want none from a fenced signal", sink.events)
	}
}
