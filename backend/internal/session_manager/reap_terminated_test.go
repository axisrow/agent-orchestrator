package sessionmanager

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/orphanreap"
)

// TestReapTerminatedKillsOrphanOfTerminatedSessionOnly proves the wiring, not
// the decision logic (that lives in the orphanreap package tests): the store's
// terminated rows feed the scan, and only a terminated session's pty-host
// chain is signaled. The live session's identical chain is adoption surface.
func TestReapTerminatedKillsOrphanOfTerminatedSessionOnly(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.sessions["dead-1"] = domain.SessionRecord{ID: "dead-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true}
	st.sessions["live-1"] = domain.SessionRecord{ID: "live-1", ProjectID: "mer", Kind: domain.KindWorker}
	m := New(Deps{
		Runtime:    &fakeRuntime{},
		Agents:     fakeAgents{},
		Workspace:  &fakeWorkspace{},
		Store:      st,
		Messenger:  &fakeMessenger{},
		Lifecycle:  &fakeLCM{store: st},
		LookPath:   func(string) (string, error) { return "/bin/true", nil },
		DataDir:    t.TempDir(),
		Executable: func() (string, error) { return "/tmp/fake-bin/ao", nil },
	})
	boot := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	m.bootStartedAt = boot
	at := boot.Add(5 * time.Second)
	m.processTable = func(context.Context) (*orphanreap.Table, time.Time, error) {
		tbl, err := orphanreap.ParseTable(strings.Join([]string{
			"  500     1  7168  2-00:00:00 /tmp/fake-bin/ao pty-host dead-1 " +
				filepath.Join(m.dataDir, "worktrees", "mer", "dead-1") + " bash -lc claude",
			"  501   500  6144  2-00:00:00 /tmp/fake-bin/ao agent-process supervise --session dead-1 --launch L1 -- claude",
			"  502     1  7168  2-00:00:00 /tmp/fake-bin/ao pty-host live-1 " +
				filepath.Join(m.dataDir, "worktrees", "mer", "live-1") + " bash -lc claude",
		}, "\n"), at)
		return tbl, at, err
	}
	var killed []int
	m.orphanKiller = orphanreap.Killer{
		Signal: func(pid int) error { killed = append(killed, pid); return nil },
		Kill:   func(pid int) error { killed = append(killed, pid); return nil },
		Alive:  func(int) bool { return false },
		Grace:  time.Millisecond,
		Poll:   time.Millisecond,
	}

	if err := m.ReapTerminated(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(killed) != 1 || killed[0] != 500 {
		t.Fatalf("killed = %v, want exactly the dead session's pty-host root 500", killed)
	}
}

// TestReapTerminatedDegradesToLogWithoutProcessTable proves an unreadable
// process table only skips the scan — it must not fail the pass (the
// handle-based destroy before it still ran) and must not guess.
func TestReapTerminatedDegradesToLogWithoutProcessTable(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.sessions["dead-1"] = domain.SessionRecord{ID: "dead-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true}
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    fakeAgents{},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
		DataDir:   t.TempDir(),
	})
	m.processTable = func(context.Context) (*orphanreap.Table, time.Time, error) {
		return nil, time.Time{}, context.DeadlineExceeded
	}
	if err := m.ReapTerminated(context.Background()); err != nil {
		t.Fatalf("unreadable process table failed the pass: %v", err)
	}
}
