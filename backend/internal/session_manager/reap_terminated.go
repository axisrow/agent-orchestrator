package sessionmanager

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/orphanreap"
)

// ReapTerminated destroys the runtimes of sessions the database already marks
// terminated. Two passes cover the leak surfaces of a daemon death (fork
// issue #39):
//
//  1. The handle-based destroy (reconcileReap): the stored runtime handle
//     still steers tmux and pty-host teardown for rows whose metadata is
//     intact.
//  2. The process-table scan: pty-host chains that outlived their metadata —
//     a lost handle id, a pruned or never-written registry entry — are killed
//     only when the full evidence stack in the orphanreap package agrees
//     (executable, verb, session id against this store, worktrees cwd, start
//     predating this boot). Everything short of that is logged, not killed.
//
// The daemon runs it once at boot (inside ReconcileBackground) and once more
// during graceful shutdown, so leaks of already-terminated sessions never
// survive a daemon lifecycle in either direction. Live sessions are untouched:
// their runtimes are this daemon's adoption surface, not its leftovers.
func (m *Manager) ReapTerminated(ctx context.Context) error {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("reap terminated: list sessions: %w", err)
	}
	terminated := make(map[string]struct{})
	var terminatedRecs []domain.SessionRecord
	for _, rec := range recs {
		if !rec.IsTerminated {
			continue
		}
		terminated[string(rec.ID)] = struct{}{}
		terminatedRecs = append(terminatedRecs, rec)
	}
	for _, rec := range terminatedRecs {
		if err := m.reconcileReap(ctx, rec); err != nil {
			m.logger.Error("reap: runtime destroy failed, skipping", "sessionID", rec.ID, "error", err)
		}
	}
	m.reapOrphanProcesses(ctx, terminated)
	return nil
}

// reapOrphanProcesses runs the evidence-based process-table pass. Reading the
// table (or the daemon's own executable) failing degrades to a log: a scan
// that cannot decide must not guess.
func (m *Manager) reapOrphanProcesses(ctx context.Context, terminated map[string]struct{}) {
	if len(terminated) == 0 {
		return
	}
	tbl, at, err := m.processTable(ctx)
	if err != nil {
		m.logger.Warn("reap: process table unavailable, orphan scan skipped", "error", err)
		return
	}
	exe, err := m.executable()
	if err != nil {
		m.logger.Warn("reap: daemon executable unresolved, orphan scan skipped", "error", err)
		return
	}
	victims := orphanreap.Reap(ctx, tbl, at, orphanreap.Deps{
		Boot:           m.bootStartedAt,
		WorktreesDir:   filepath.Join(m.dataDir, "worktrees"),
		ExecutableBase: filepath.Base(exe),
		Terminated:     terminated,
		Logger:         m.logger,
	}, m.orphanKiller)
	for _, v := range victims {
		m.logger.Info("reap: orphaned worker tree removed", "session", v.SessionID, "pid", v.PID, "tree", v.Tree)
	}
}

// snapshotProcessTable reads the process table for the orphan scan. etime (not
// time=) is the age column: wall-clock time since start, locale-free on Linux
// and macOS, so start-time evidence does not depend on the machine locale. On
// platforms without ps (Windows) the command fails and the caller degrades to
// a log — the pass is a no-op there, never a guess.
func snapshotProcessTable(ctx context.Context) (*orphanreap.Table, time.Time, error) {
	at := time.Now().UTC()
	out, err := aoprocess.CommandContext(ctx, "ps", "-axww", "-o", "pid=,ppid=,rss=,etime=,args=").Output()
	if err != nil {
		return nil, at, fmt.Errorf("reap: ps: %w", err)
	}
	tbl, err := orphanreap.ParseTable(string(out), at)
	if err != nil {
		return nil, at, err
	}
	return tbl, at, nil
}
