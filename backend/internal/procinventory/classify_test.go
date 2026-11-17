package procinventory

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	testDaemonPID = 4001
	testTmuxPID   = 4055
)

func classifyEntries(t *testing.T, entries []Entry, live map[domain.SessionID]domain.SessionRecord) Inventory {
	t.Helper()
	return BuildInventory(entries, live, testDaemonPID, "ao-test", time.Unix(1758000000, 0))
}

func ptyRoot(pid, ppid int, sessionID string) Entry {
	return Entry{PID: pid, PPID: ppid, PGID: pid, RSSKB: 7000,
		Command: "/usr/local/bin/ao pty-host " + sessionID + " /tmp/wk -- claude --resume abc"}
}

func TestClassify_OwnedFreshAndAdopted(t *testing.T) {
	live := map[domain.SessionID]domain.SessionRecord{
		"web-api-3": {Kind: domain.KindWorker},
	}
	entries := []Entry{
		{PID: testDaemonPID, PPID: 1, PGID: testDaemonPID, RSSKB: 90000, Command: "/usr/local/bin/ao daemon"},
		ptyRoot(100, testDaemonPID, "web-api-3"), // fresh: parent is the daemon
		ptyRoot(200, 1, "web-api-3"),             // adopted after a restart: PPID 1
	}
	inv := classifyEntries(t, entries, live)

	if !inv.Daemon.Present || inv.Daemon.PID != testDaemonPID {
		t.Fatalf("daemon group = %+v", inv.Daemon)
	}
	if inv.Totals.SessionsCount != 2 {
		t.Fatalf("sessions = %d, want 2", inv.Totals.SessionsCount)
	}
	if inv.Totals.OrphansCount != 0 {
		t.Fatalf("orphans = %d, want 0 — adopted live trees must never be orphans", inv.Totals.OrphansCount)
	}
	for _, tree := range inv.Trees {
		if tree.State != StateOwned {
			t.Fatalf("tree %s state = %s, want owned", tree.SessionID, tree.State)
		}
		if tree.Kind != "worker" {
			t.Fatalf("tree %s kind = %q", tree.SessionID, tree.Kind)
		}
	}
}

func TestClassify_SpacedBundlePathRootMatches(t *testing.T) {
	// The packaged binary lives under "/Applications/Agent Orchestrator.app/"
	// — a space inside the executable path — and ps renders args unquoted, so
	// the pty-host verb is NOT fields[1] of the row. The matcher must find it
	// relative to the verb.
	spaced := Entry{PID: 1439, PPID: 1, PGID: 1439, RSSKB: 25040,
		Command: "/Applications/Agent Orchestrator.app/Contents/Resources/daemon/ao pty-host direct-cli-17 " +
			"/Users/x/.ao/data/worktrees/direct-cli/orchestrator/direct-cli-orchestrator " +
			"/Applications/Agent Orchestrator.app/Contents/Resources/daemon/ao agent-process supervise " +
			"--session direct-cli-17 --launch f9b0df1e -- /Users/x/.local/bin/claude --resume abc"}
	inv := classifyEntries(t, []Entry{spaced}, nil)

	if inv.Totals.OrphansCount != 1 {
		t.Fatalf("orphans = %d, want 1; trees = %+v", inv.Totals.OrphansCount, inv.Trees)
	}
	if inv.Trees[0].SessionID != "direct-cli-17" {
		t.Fatalf("session = %q", inv.Trees[0].SessionID)
	}
}

func TestClassify_OrphansAndForeign(t *testing.T) {
	live := map[domain.SessionID]domain.SessionRecord{}
	entries := []Entry{
		{PID: testDaemonPID, PPID: 1, PGID: testDaemonPID, RSSKB: 90000, Command: "/usr/local/bin/ao daemon"},
		{PID: 555, PPID: 1, PGID: 555, RSSKB: 1000, Command: "/other/ao daemon"}, // another live daemon
		ptyRoot(300, testDaemonPID, "dead-task-1"),                               // daemon died but tree leaks under it
		ptyRoot(310, 1, "dead-task-2"),                                           // daemon-dead leak, launchd-adopted
		ptyRoot(320, 555, "dead-task-3"),                                         // parent is another live AO daemon
	}
	inv := classifyEntries(t, entries, live)

	states := map[string]string{}
	for _, tree := range inv.Trees {
		states[tree.SessionID] = tree.State
	}
	if states["dead-task-1"] != StateOrphan || states["dead-task-2"] != StateOrphan {
		t.Fatalf("orphan states = %v", states)
	}
	if states["dead-task-3"] != StateForeign {
		t.Fatalf("foreign state = %s", states["dead-task-3"])
	}
	if inv.Totals.OrphansCount != 2 || inv.Totals.ForeignCount != 1 {
		t.Fatalf("totals = %+v", inv.Totals)
	}
}

func TestClassify_ReviewerTreeUnderDaemonIsNotOrphan(t *testing.T) {
	// Fork issue #364 live case: a reviewer pty-host has no sessions row, so
	// the live-set check misses it; while its spawning daemon lives (PPID is
	// the daemon — Setsid does not re-parent), its tree is daemon-owned.
	live := map[domain.SessionID]domain.SessionRecord{
		"tg_content_factory-284": {Kind: domain.KindWorker},
	}
	entries := []Entry{
		{PID: testDaemonPID, PPID: 1, PGID: testDaemonPID, RSSKB: 90000, Command: "/usr/local/bin/ao daemon"},
		ptyRoot(690, testDaemonPID, "tg_content_factory-284"), // the live worker's own tree
		// The reviewer tree, exactly as ps renders it in the field.
		{PID: 700, PPID: testDaemonPID, PGID: 700, RSSKB: 20000, Command: "/Applications/Agent Orchestrator.app/Contents/Resources/daemon/ao pty-host review-tg_content_factory-284 " +
			"/Users/x/.ao/data/worktrees/tcf/tg_content_factory-284 " +
			"/Applications/Agent Orchestrator.app/Contents/Resources/daemon/ao agent-process supervise " +
			"--session review-tg_content_factory-284 --launch f9b0df1e --activity-review rev-1 -- /Users/x/.local/bin/claude --resume abc"},
		ptyRoot(710, 1, "review-adopted-1"), // daemon died, tree adopted: still an orphan
	}
	inv := classifyEntries(t, entries, live)

	states := map[string]string{}
	for _, tree := range inv.Trees {
		states[tree.SessionID] = tree.State
	}
	if states["review-tg_content_factory-284"] != StateDaemon {
		t.Fatalf("reviewer tree state = %q, want %q; trees = %+v", states["review-tg_content_factory-284"], StateDaemon, inv.Trees)
	}
	if states["review-adopted-1"] != StateOrphan {
		t.Fatalf("adopted reviewer tree state = %q, want %q", states["review-adopted-1"], StateOrphan)
	}
	if inv.Totals.OrphansCount != 1 {
		t.Fatalf("orphans = %d, want 1 — the attached reviewer tree must never count as orphaned", inv.Totals.OrphansCount)
	}
	if inv.Totals.SessionsCount != 1 {
		t.Fatalf("sessions = %d, want 1 — reviewer trees have no session row", inv.Totals.SessionsCount)
	}
}

func TestClassify_TmuxGroupSocketFilter(t *testing.T) {
	base := []Entry{
		{PID: testDaemonPID, PPID: 1, PGID: testDaemonPID, RSSKB: 90000, Command: "/usr/local/bin/ao daemon"},
		{PID: testTmuxPID, PPID: 1, PGID: testTmuxPID, RSSKB: 45000, Command: "/usr/bin/tmux -L ao-test new-session -d"},
		{PID: 777, PPID: 1, PGID: 777, RSSKB: 30000, Command: "/usr/bin/tmux -U 0"}, // user's personal server
	}
	inv := classifyEntries(t, base, nil)
	if !inv.Tmux.Present || inv.Tmux.PID != testTmuxPID {
		t.Fatalf("tmux group = %+v", inv.Tmux)
	}

	// Without a socket name any tmux process counts (server keeps its argv;
	// clients live milliseconds, so snapshots effectively only see servers).
	inv = BuildInventory(base, nil, testDaemonPID, "", time.Unix(0, 0))
	if !inv.Tmux.Present || inv.Tmux.RSSBytes < 45000*1024 {
		t.Fatalf("tmux group (default socket) = %+v", inv.Tmux)
	}
}

func TestClassify_TreeMetricsAndRemnants(t *testing.T) {
	live := map[domain.SessionID]domain.SessionRecord{
		"live-1": {Kind: domain.KindOrchestrator},
	}
	entries := []Entry{
		ptyRoot(400, 1, "live-1"), // adopted live orchestrator
		{PID: 401, PPID: 400, PGID: 400, RSSKB: 6000, Command: "/usr/local/bin/ao agent-process supervise --session live-1 --launch l1 -- claude"},
		{PID: 402, PPID: 401, PGID: 400, RSSKB: 140000, Command: "claude --resume abc"},
		// Remnant: supervise whose pty-host leader is dead, session not live.
		{PID: 500, PPID: 1, PGID: 4000, RSSKB: 5000, Command: "/usr/local/bin/ao agent-process supervise --session dead-9 --launch l2 -- claude"},
	}
	inv := classifyEntries(t, entries, live)

	var tree *Tree
	for i := range inv.Trees {
		if inv.Trees[i].SessionID == "live-1" {
			tree = &inv.Trees[i]
		}
	}
	if tree == nil {
		t.Fatalf("live-1 tree missing: %+v", inv.Trees)
	}
	if tree.PIDCount != 3 {
		t.Fatalf("pid count = %d, want 3", tree.PIDCount)
	}
	if want := int64((7000 + 6000 + 140000) * 1024); tree.RSSBytes != want {
		t.Fatalf("rss = %d, want %d", tree.RSSBytes, want)
	}
	if tree.Kind != "orchestrator" {
		t.Fatalf("kind = %q", tree.Kind)
	}

	if len(inv.Remnants) != 1 || inv.Remnants[0].SessionID != "dead-9" {
		t.Fatalf("remnants = %+v", inv.Remnants)
	}
}
