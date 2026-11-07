// Package pushwatch observes live worker sessions for pushes the daemon can
// see locally: a successful `git push` updates the branch's remote-tracking
// ref before any provider fetch lands, so watching that ref turns the push
// into an immediate daemon event. The SCM observer's provider poll stays the
// fallback for pushes this process cannot observe (another host, a shell
// tab without hooks).
package pushwatch

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Callback receives one observed push: the worker session, its branch, and
// the remote tip the branch landed on.
type Callback func(ctx context.Context, sessionID domain.SessionID, branch, sha string)

// Sessions reads the session facts the detector needs.
type Sessions interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListAllSessions(context.Context) ([]domain.SessionRecord, error)
}

// Detector reconciles live worker sessions' local and remote branch tips and
// fires the callback on the unpushed→pushed transition. Fingerprint state is
// in memory: a detector restart observes current tips without firing (a push
// that already happened is not an event), and the next transition fires.
type Detector struct {
	sessions Sessions
	onPush   Callback
	logger   *slog.Logger

	mu   sync.Mutex
	tips map[domain.SessionID]string
}

// New constructs a detector. onPush must be non-nil.
func New(sessions Sessions, onPush Callback, logger *slog.Logger) *Detector {
	if logger == nil {
		logger = slog.Default()
	}
	return &Detector{sessions: sessions, onPush: onPush, logger: logger, tips: map[domain.SessionID]string{}}
}

// ReconcileSession refreshes one session's tips; the activity-hook path calls
// it right after a worker's post-tool-use event, which covers a `git push`
// the worker just completed.
func (d *Detector) ReconcileSession(ctx context.Context, id domain.SessionID) {
	rec, ok, err := d.sessions.GetSession(ctx, id)
	if err != nil || !ok {
		return
	}
	d.reconcile(ctx, []domain.SessionRecord{rec})
}

// ReconcileAll refreshes every live worker session.
func (d *Detector) ReconcileAll(ctx context.Context) {
	recs, err := d.sessions.ListAllSessions(ctx)
	if err != nil {
		d.logger.Debug("pushwatch: session list failed", "err", err)
		return
	}
	d.reconcile(ctx, recs)
}

func (d *Detector) reconcile(ctx context.Context, recs []domain.SessionRecord) {
	for _, rec := range recs {
		if rec.IsTerminated || rec.Kind != domain.KindWorker {
			continue
		}
		root := rec.Metadata.WorkspacePath
		branch := rec.Metadata.Branch
		if root == "" || branch == "" {
			continue
		}
		local, remote, ok := readBranchTips(ctx, root, branch)
		if !ok {
			continue
		}
		fingerprint := local + " " + remote
		d.mu.Lock()
		prev, seen := d.tips[rec.ID]
		d.tips[rec.ID] = fingerprint
		d.mu.Unlock()
		// Pushed means the branch's remote tip equals its local tip. An event
		// needs a previous observation of a not-pushed state: the daemon
		// seeing an already-pushed branch for the first time is not a push.
		pushed := remote != "" && remote == local
		wasPushed := seen && pushedFingerprint(prev)
		if pushed && seen && !wasPushed {
			d.logger.Info("pushwatch: worker push observed", "session", rec.ID, "branch", branch, "sha", local)
			d.onPush(ctx, rec.ID, branch, local)
		}
	}
}

func pushedFingerprint(fingerprint string) bool {
	local, remote, ok := strings.Cut(fingerprint, " ")
	return ok && remote != "" && remote == local
}

// readBranchTips returns the branch's local and remote (origin) tips with one
// git process. ok=false on any git failure; a failed read is not evidence of
// a push either way.
func readBranchTips(ctx context.Context, root, branch string) (local, remote string, ok bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := aoprocess.CommandContext(ctx, "git", "-C", root, "for-each-ref", "--format=%(objectname) %(refname)",
		"refs/heads/"+branch, "refs/remotes/origin/"+branch).Output()
	if err != nil {
		return "", "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		sha, ref, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found {
			continue
		}
		switch ref {
		case "refs/heads/" + branch:
			local = sha
		case "refs/remotes/origin/" + branch:
			remote = sha
		}
	}
	return local, remote, local != ""
}
