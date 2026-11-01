package sessionmanager

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// EventSessionRestored records one observable trace of "who restarted this
// conversation, when, and why": every successful native relaunch of an exited
// agent (daemon boot restore, manual resume, post-update restart, agent or
// interface switch, reviewer restore) emits it alongside an INFO daemon-log
// line (#109). Without it a resume is invisible everywhere except the
// rotating daemon.log's DEBUG level.
const EventSessionRestored = "ao.session.restored"

// SetRestoreTelemetry late-binds the sink for session-restored events.
func (m *Manager) SetRestoreTelemetry(sink ports.EventSink) {
	m.restoreTelemetryMu.Lock()
	defer m.restoreTelemetryMu.Unlock()
	m.restoreTelemetry = sink
}

// restoreTrigger maps a relaunch's operation label onto the issue's trigger
// categories. Unknown operations pass through unchanged so a future relaunch
// caller stays visible instead of masquerading as "manual".
func restoreTrigger(operation string) string {
	switch operation {
	case "restore":
		return "daemon-start"
	case "resume after update", "version refresh":
		return "update-resume"
	case "agent switch", "agent switch rollback":
		return "agent-switch"
	case "switch interface":
		return "interface-transition"
	case "resume agent":
		return "manual"
	default:
		return operation
	}
}

func (m *Manager) emitRestored(rec domain.SessionRecord, mode RestoreMode, trigger string) {
	m.logger.Info("session restored",
		"sessionID", rec.ID, "mode", string(mode),
		"nativeSessionId", rec.Metadata.AgentSessionID,
		"harness", string(rec.Harness), "trigger", trigger)
	m.restoreTelemetryMu.Lock()
	sink := m.restoreTelemetry
	m.restoreTelemetryMu.Unlock()
	if sink == nil {
		return
	}
	sink.Emit(context.Background(), ports.TelemetryEvent{
		Name: EventSessionRestored, Source: "session_manager", OccurredAt: time.Now().UTC(),
		Level: ports.TelemetryLevelInfo, ProjectID: &rec.ProjectID, SessionID: &rec.ID,
		Payload: map[string]any{
			"mode":            string(mode),
			"nativeSessionId": rec.Metadata.AgentSessionID,
			"trigger":         trigger,
			"harness":         string(rec.Harness),
		},
	})
}
