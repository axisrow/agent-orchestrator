package pushwatch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeSessions struct {
	rec domain.SessionRecord
}

func (f *fakeSessions) GetSession(_ context.Context, _ domain.SessionID) (domain.SessionRecord, bool, error) {
	return f.rec, true, nil
}

func (f *fakeSessions) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return []domain.SessionRecord{f.rec}, nil
}

// gitFixture builds a bare origin and a work repo whose branch mirrors an AO
// worker: commits land locally, then get pushed to origin.
type gitFixture struct {
	t       *testing.T
	repo    string
	branch  string
	commits int
}

func newGitFixture(t *testing.T, branch string) *gitFixture {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "work")
	bare := filepath.Join(root, "origin.git")
	git(t, "", "init", "--bare", "-b", branch, bare)
	git(t, "", "init", "-b", branch, repo)
	git(t, repo, "remote", "add", "origin", bare)
	git(t, repo, "config", "user.name", "pushwatch-test")
	git(t, repo, "config", "user.email", "pushwatch@test")
	return &gitFixture{t: t, repo: repo, branch: branch}
}

func (f *gitFixture) commit() string {
	f.t.Helper()
	f.commits++
	name := filepath.Join(f.repo, "file.txt")
	if err := os.WriteFile(name, []byte(string(rune('a'+f.commits))+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	git(f.t, f.repo, "add", "file.txt")
	git(f.t, f.repo, "commit", "-m", "c")
	out, err := exec.Command("git", "-C", f.repo, "rev-parse", "HEAD").Output()
	if err != nil {
		f.t.Fatal(err)
	}
	return string(out[:len(out)-1])
}

func (f *gitFixture) push() {
	f.t.Helper()
	git(f.t, f.repo, "push", "-u", "origin", f.branch)
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

type recordedPush struct {
	session domain.SessionID
	branch  string
	sha     string
}

func detectorFor(t *testing.T, session domain.SessionRecord, mu *sync.Mutex, pushes *[]recordedPush) *Detector {
	t.Helper()
	return New(&fakeSessions{rec: session}, func(_ context.Context, id domain.SessionID, branch, sha string) {
		mu.Lock()
		*pushes = append(*pushes, recordedPush{id, branch, sha})
		mu.Unlock()
	}, nil)
}

func TestDetectorFiresOncePerPush(t *testing.T) {
	fixture := newGitFixture(t, "ao/test/root")
	fixture.commit()
	fixture.push() // branch starts already pushed: no event without a seen unpushed state
	session := domain.SessionRecord{ID: "s1", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: fixture.repo, Branch: "ao/test/root"}}
	var mu sync.Mutex
	var pushes []recordedPush
	d := detectorFor(t, session, &mu, &pushes)
	ctx := context.Background()

	d.ReconcileSession(ctx, "s1")
	d.ReconcileAll(ctx)
	if got := countPushes(&mu, &pushes); got != 0 {
		t.Fatalf("pushes after first reconcile of an already-pushed branch = %d, want 0", got)
	}

	// Local commit: unpushed state. No event yet.
	sha2 := fixture.commit()
	d.ReconcileSession(ctx, "s1")
	if got := countPushes(&mu, &pushes); got != 0 {
		t.Fatalf("pushes after local commit = %d, want 0", got)
	}

	// The push: remote-tracking ref lands on the local tip. One event, with
	// the pushed sha.
	fixture.push()
	d.ReconcileSession(ctx, "s1")
	if got := countPushes(&mu, &pushes); got != 1 {
		t.Fatalf("pushes after push = %d, want 1", got)
	}
	mu.Lock()
	push := pushes[0]
	mu.Unlock()
	if push.session != "s1" || push.branch != "ao/test/root" || push.sha != sha2 {
		t.Fatalf("push = %+v, want session s1 branch ao/test/root sha %s", push, sha2)
	}

	// Reconciling again observes the same pushed state: no duplicate event.
	d.ReconcileSession(ctx, "s1")
	d.ReconcileAll(ctx)
	if got := countPushes(&mu, &pushes); got != 1 {
		t.Fatalf("pushes after re-reconcile = %d, want 1 (no duplicate)", got)
	}
}

// TestDetectorFiresWhenUnpushedStatePrecedesPush covers the hook path timing:
// the detector may observe the unpushed state mid-turn (any tool call) and
// then the pushed state right after the push.
func TestDetectorFiresWhenUnpushedStatePrecedesPush(t *testing.T) {
	fixture := newGitFixture(t, "ao/test-2/root")
	session := domain.SessionRecord{ID: "s2", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: fixture.repo, Branch: "ao/test-2/root"}}
	var mu sync.Mutex
	var pushes []recordedPush
	d := detectorFor(t, session, &mu, &pushes)
	ctx := context.Background()

	fixture.commit() // unpushed
	d.ReconcileSession(ctx, "s2")
	fixture.push()
	d.ReconcileSession(ctx, "s2")
	if got := countPushes(&mu, &pushes); got != 1 {
		t.Fatalf("pushes = %d, want 1", got)
	}
}

func TestDetectorSkipsNonWorkersAndBareRecords(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@t")
	os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644)
	git(t, repo, "add", "f")
	git(t, repo, "commit", "-m", "c")
	var mu sync.Mutex
	var pushes []recordedPush
	sessions := &fakeSessions{rec: domain.SessionRecord{ID: "orch", Kind: domain.KindOrchestrator, Metadata: domain.SessionMetadata{WorkspacePath: repo, Branch: "main"}}}
	d := New(sessions, func(_ context.Context, id domain.SessionID, branch, sha string) {
		mu.Lock()
		pushes = append(pushes, recordedPush{id, branch, sha})
		mu.Unlock()
	}, nil)
	d.ReconcileAll(context.Background())
	if got := countPushes(&mu, &pushes); got != 0 {
		t.Fatalf("non-worker session fired %d pushes, want 0", got)
	}
	// A missing workspace or branch is skipped without a git call.
	sessions.rec.Kind = domain.KindWorker
	sessions.rec.Metadata.WorkspacePath = ""
	d.ReconcileAll(context.Background())
	sessions.rec.Metadata.WorkspacePath = repo
	sessions.rec.Metadata.Branch = ""
	d.ReconcileAll(context.Background())
	if got := countPushes(&mu, &pushes); got != 0 {
		t.Fatalf("bare records fired %d pushes, want 0", got)
	}
}

func countPushes(mu *sync.Mutex, pushes *[]recordedPush) int {
	mu.Lock()
	defer mu.Unlock()
	return len(*pushes)
}
