package store_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The review-only flag (the unified PR review entrypoint's foreign mode) must
// round-trip through every full-row session read: a daemon restart re-derives
// the tool deny list from it, so a lost column silently re-arms Edit.
func TestSessionReviewOnlyRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "fork")

	rec := sampleRecord("fork")
	rec.ReviewOnly = true
	rec.AutoReviewEnabled = true
	rec.AutoInjectReview = false
	created, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create review-only session: %v", err)
	}
	id := created.ID

	got, ok, err := s.GetSession(ctx, id)
	if err != nil || !ok {
		t.Fatalf("get session: ok=%v err=%v", ok, err)
	}
	if !got.ReviewOnly || !got.AutoReviewEnabled || got.AutoInjectReview {
		t.Fatalf("review-only flags lost on GetSession: reviewOnly=%t autoReview=%t autoInject=%t",
			got.ReviewOnly, got.AutoReviewEnabled, got.AutoInjectReview)
	}

	byProject, err := s.ListSessions(ctx, domain.ProjectID("fork"))
	if err != nil {
		t.Fatalf("list by project: %v", err)
	}
	if len(byProject) != 1 || !byProject[0].ReviewOnly {
		t.Fatalf("ListSessions lost reviewOnly: %+v", byProject)
	}

	all, err := s.ListAllSessions(ctx)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 1 || !all[0].ReviewOnly {
		t.Fatalf("ListAllSessions lost reviewOnly: %+v", all)
	}

	// A generic UpdateSession must not clobber the spawn-fixed flag.
	stored, _, _ := s.GetSession(ctx, id)
	stored.DisplayName = "renamed"
	if err := s.UpdateSession(ctx, stored); err != nil {
		t.Fatalf("update session: %v", err)
	}
	after, ok, _ := s.GetSession(ctx, id)
	if !ok || !after.ReviewOnly {
		t.Fatalf("UpdateSession clobbered reviewOnly: ok=%t reviewOnly=%t", ok, after.ReviewOnly)
	}
}
