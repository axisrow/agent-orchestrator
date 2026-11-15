package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A terminated session's PR record never refreshes on its own (the observer
// drops terminated subjects), so the card's one-shot peek is the only thing
// that can reconcile it. The peek must return provider-fresh facts read-only:
// no store writes, terminal rows skipped.
func TestPeekSessionPRsReturnsFreshFactsWithoutWriting(t *testing.T) {
	prURL := "https://github.com/acme/app/pull/1361"
	st := newFakeStore()
	st.sessions["term-1"] = domain.SessionRecord{ID: "term-1", IsTerminated: true}
	stored := domain.PullRequest{
		URL: prURL, Number: 1361, Merged: false, Closed: false,
		CI: domain.CIPassing, Review: domain.ReviewNone, Mergeability: domain.MergeMergeable,
		UpdatedAt: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC),
	}
	st.prs["term-1"] = []domain.PullRequest{stored}
	svc := NewWithDeps(Deps{
		Store: st,
		SCM: fakeSCM{obs: ports.SCMObservation{
			Fetched:    true,
			ObservedAt: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC),
			PR:         ports.SCMPRObservation{URL: prURL, Number: 1361, State: string(domain.PRStateMerged)},
			CI:         ports.SCMCIObservation{Summary: string(domain.CIPassing)},
			Mergeability: ports.SCMMergeabilityObservation{
				State: string(domain.MergeMergeable),
			},
		}},
	})

	got, err := svc.PeekSessionPRs(context.Background(), "term-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("peeked %d PRs, want 1", len(got))
	}
	if got[0].State != domain.PRStateMerged || got[0].URL != prURL || got[0].Number != 1361 {
		t.Fatalf("peeked facts = %+v", got[0])
	}
	if got[0].UpdatedAt.IsZero() {
		t.Fatal("peeked facts carry no freshness stamp")
	}
	// Read-only: the stored record stays exactly as it was.
	after := st.prs["term-1"][0]
	if after.Merged || !after.UpdatedAt.Equal(stored.UpdatedAt) {
		t.Fatalf("stored record changed: %+v", after)
	}
}

func TestPeekSessionPRsSkipsTerminalRowsAndFiltersUnfetched(t *testing.T) {
	prURL := "https://github.com/acme/app/pull/9"
	st := newFakeStore()
	st.sessions["term-2"] = domain.SessionRecord{ID: "term-2", IsTerminated: true}
	st.prs["term-2"] = []domain.PullRequest{
		{URL: prURL, Number: 9, UpdatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		{URL: "https://github.com/acme/app/pull/5", Number: 5, Merged: true},
	}
	svc := NewWithDeps(Deps{
		Store: st,
		SCM: fakeSCM{obs: ports.SCMObservation{
			// Fetched=false classifies a failed fetch: the snapshot must not
			// invent facts for it, so the PR is left out entirely.
			PR:    ports.SCMPRObservation{URL: prURL, Number: 9, State: string(domain.PRStateOpen)},
			Error: ports.ErrSCMNotFound,
		}},
	})

	got, err := svc.PeekSessionPRs(context.Background(), "term-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("peeked facts = %+v, want none from an unfetched observation", got)
	}
}

func TestPeekSessionPRsReportsSCMUnavailable(t *testing.T) {
	st := newFakeStore()
	st.sessions["term-3"] = domain.SessionRecord{ID: "term-3", IsTerminated: true}
	st.prs["term-3"] = []domain.PullRequest{{URL: "https://github.com/acme/app/pull/3", Number: 3}}

	if _, err := (&Service{store: st}).PeekSessionPRs(context.Background(), "term-3"); !errors.Is(err, ErrSCMUnavailable) {
		t.Fatalf("peek without SCM = %v, want ErrSCMUnavailable", err)
	}

	svc := NewWithDeps(Deps{Store: st, SCM: fakeSCM{fetchErr: errors.New("provider down")}})
	if _, err := svc.PeekSessionPRs(context.Background(), "term-3"); !errors.Is(err, ErrSCMUnavailable) {
		t.Fatalf("peek with failing provider = %v, want ErrSCMUnavailable", err)
	}
}
