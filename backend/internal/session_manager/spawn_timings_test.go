package sessionmanager

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// spawnTimingsLine returns the single "spawn timings" line in log, failing the
// test when there is none or more than one.
func spawnTimingsLine(t *testing.T, log string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, `msg="spawn timings"`) {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("spawn timings lines = %d, want 1; log = %q", len(found), log)
	}
	return found[0]
}

func assertTimingKeys(t *testing.T, line string, present, absent []string) {
	t.Helper()
	for _, key := range present {
		if !strings.Contains(line, " "+key+"=") {
			t.Errorf("timings line lacks %s: %q", key, line)
		}
	}
	for _, key := range absent {
		if strings.Contains(line, " "+key+"=") {
			t.Errorf("timings line has unexpected %s: %q", key, line)
		}
	}
}

func TestSpawnTimings_LogsEveryTUIStage(t *testing.T) {
	m, _, _, _ := newManager()
	var logBuf bytes.Buffer
	m.logger = slog.New(slog.NewTextHandler(&logBuf, nil))

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}

	line := spawnTimingsLine(t, logBuf.String())
	assertTimingKeys(t, line,
		[]string{"resolveMs", "seedMs", "gateWaitMs", "defaultBranchMs", "worktreeMs", "provisionMs", "launchPrepMs", "runtimeMs", "recordMs", "totalMs"},
		[]string{"taskPrepWaitMs", "attachmentsMs", "controllerMs", "error"},
	)
	for _, attr := range []string{"sessionID=mer-1", "harness=claude-code", "mode=tui", "async=false"} {
		if !strings.Contains(line, attr) {
			t.Errorf("timings line lacks %s: %q", attr, line)
		}
	}
}

func TestSpawnTimings_FailedWorktreeStopsAtFailedStage(t *testing.T) {
	m, _, _, ws := newManager()
	ws.createErr = errors.New("worktree add failed")
	var logBuf bytes.Buffer
	m.logger = slog.New(slog.NewTextHandler(&logBuf, nil))

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err == nil {
		t.Fatal("Spawn() = nil, want worktree failure")
	}

	line := spawnTimingsLine(t, logBuf.String())
	assertTimingKeys(t, line,
		[]string{"sessionID", "worktreeMs", "totalMs", "error"},
		[]string{"provisionMs", "launchPrepMs", "runtimeMs"},
	)
	if !strings.Contains(line, "worktree add failed") {
		t.Errorf("timings line lacks the failure: %q", line)
	}
}

func TestSpawnTimings_EarlyRejectionLogsTotalOnly(t *testing.T) {
	st := newFakeStore()
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: missingAgents{}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }})
	var logBuf bytes.Buffer
	m.logger = slog.New(slog.NewTextHandler(&logBuf, nil))

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: "bogus"}); !errors.Is(err, ErrUnknownHarness) {
		t.Fatalf("err = %v, want ErrUnknownHarness", err)
	}

	line := spawnTimingsLine(t, logBuf.String())
	assertTimingKeys(t, line,
		[]string{"harness", "totalMs", "error"},
		[]string{"sessionID", "resolveMs", "seedMs"},
	)
}
