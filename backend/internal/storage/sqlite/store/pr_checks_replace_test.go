package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Issue #199 regression: pr_checks used to accumulate check-name generations
// per (pr_url, name, commit_hash), so the card showed a union of generations
// with contradicting verdicts. The BeamCraft#351 shape: an older generation of
// expanded matrix names coexisted with the live generation of literal
// ${{ matrix.shard }} template names on the same head.
func TestWriteSCMObservationReplacesCheckGenerationForHead(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)
	head := "a34a67ad282084d6821a9a53f4b51c3ec9e5fcbc"

	pr := domain.PullRequest{
		URL: "https://github.com/o/r/pull/351", SessionID: r.ID, Number: 351,
		Provider: "github", Host: "github.com", Repo: "o/r",
		SourceBranch: "feat/x", TargetBranch: "main", HeadSHA: head,
		Title: "checks", CI: domain.CIPassing,
		MetadataHash: "mh", CIHash: "ch1",
		UpdatedAt: now, ObservedAt: now, CIObservedAt: now,
	}
	deadGeneration := []domain.PullRequestCheck{
		{Name: "test (ubuntu-latest, shard 1/4)", CommitHash: head, Status: domain.PRCheckPassed, Conclusion: "success"},
		{Name: "test (windows-latest, shard 12/12)", CommitHash: head, Status: domain.PRCheckSkipped, Conclusion: "skipped"},
		{Name: "validate local test report", CommitHash: head, Status: domain.PRCheckPassed, Conclusion: "success"},
	}
	if err := s.WriteSCMObservation(ctx, pr, deadGeneration, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}

	// Same head, workflow now broken: the rollup reports literal template
	// names and a different verdict for an existing check.
	liveGeneration := []domain.PullRequestCheck{
		{Name: "test (ubuntu-latest, shard ${{ matrix.shard }}/4)", CommitHash: head, Status: domain.PRCheckFailed, Conclusion: "failure"},
		{Name: "test (windows-latest, shard ${{ matrix.shard }}/12)", CommitHash: head, Status: domain.PRCheckQueued},
		{Name: "validate local test report", CommitHash: head, Status: domain.PRCheckFailed, Conclusion: "failure"},
	}
	pr.CIHash = "ch2"
	pr.CI = domain.CIFailing
	if err := s.WriteSCMObservation(ctx, pr, liveGeneration, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListChecks(ctx, pr.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(liveGeneration) {
		t.Fatalf("stale generation survived: got %d checks %+v, want exactly the live generation", len(got), got)
	}
	byName := map[string]domain.PullRequestCheck{}
	for _, c := range got {
		byName[c.Name] = c
	}
	for _, want := range liveGeneration {
		gotc, ok := byName[want.Name]
		if !ok {
			t.Fatalf("live check %q missing after replace", want.Name)
		}
		if gotc.Status != want.Status || gotc.Conclusion != want.Conclusion {
			t.Fatalf("check %q: got %s/%s want %s/%s", want.Name, gotc.Status, gotc.Conclusion, want.Status, want.Conclusion)
		}
	}
	for _, stale := range []string{"test (ubuntu-latest, shard 1/4)", "test (windows-latest, shard 12/12)"} {
		if _, ok := byName[stale]; ok {
			t.Fatalf("phantom check %q from a dead generation survived the replace", stale)
		}
	}
}

// A re-run flips the conclusion of an existing check on the same head: the
// snapshot must re-read it, not keep the first recorded verdict.
func TestWriteSCMObservationRereadsConclusionAfterRerun(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)

	pr := domain.PullRequest{
		URL: "https://github.com/o/r/pull/7", SessionID: r.ID, Number: 7,
		Provider: "github", Host: "github.com", Repo: "o/r",
		HeadSHA: "h1", CI: domain.CIPassing,
		MetadataHash: "mh", CIHash: "ch1",
		UpdatedAt: now, ObservedAt: now, CIObservedAt: now,
	}
	passed := []domain.PullRequestCheck{{Name: "quality (ubuntu-latest)", CommitHash: "h1", Status: domain.PRCheckPassed, Conclusion: "success", Details: "100"}}
	if err := s.WriteSCMObservation(ctx, pr, passed, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}

	pr.CI = domain.CIFailing
	pr.CIHash = "ch2"
	failed := []domain.PullRequestCheck{{Name: "quality (ubuntu-latest)", CommitHash: "h1", Status: domain.PRCheckFailed, Conclusion: "failure", Details: "201"}}
	if err := s.WriteSCMObservation(ctx, pr, failed, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListChecks(ctx, pr.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Status != domain.PRCheckFailed || got[0].Conclusion != "failure" || got[0].Details != "201" {
		t.Fatalf("re-run verdict not re-read: %+v", got)
	}
}

// Pruning is scoped to the head the snapshot names: rows of previous heads are
// untouched, and batches that do not address one whole head never prune.
func TestWriteSCMObservationPruneScopesToSnapshotHead(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)

	pr := domain.PullRequest{
		URL: "https://github.com/o/r/pull/8", SessionID: r.ID, Number: 8,
		Provider: "github", Host: "github.com", Repo: "o/r",
		HeadSHA: "h2", CI: domain.CIPassing,
		MetadataHash: "mh", CIHash: "ch",
		UpdatedAt: now, ObservedAt: now, CIObservedAt: now,
	}
	oldHead := []domain.PullRequestCheck{{Name: "build", CommitHash: "h1", Status: domain.PRCheckPassed, Conclusion: "success"}}
	if err := s.WriteSCMObservation(ctx, pr, oldHead, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}

	// Metadata-only write carrying no checks: must not prune h1's row.
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	// Mixed-head batch: not a whole-head snapshot, must not prune either.
	mixed := []domain.PullRequestCheck{
		{Name: "build", CommitHash: "h1", Status: domain.PRCheckPassed, Conclusion: "success"},
		{Name: "lint", CommitHash: "h2", Status: domain.PRCheckPassed, Conclusion: "success"},
	}
	if err := s.WriteSCMObservation(ctx, pr, mixed, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListChecks(ctx, pr.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("uns scoped prune: got %+v, want both heads preserved", got)
	}
}
