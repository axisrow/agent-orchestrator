package session

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// PeekedPRFacts is one provider-fresh PR fact set from a read-only peek.
// Unresolved-comment counts are deliberately absent: the peek's fast fetch
// does not read review threads, so it cannot speak for them authoritatively.
type PeekedPRFacts struct {
	URL          string
	Number       int
	State        domain.PRState
	CI           domain.CIState
	Review       domain.ReviewDecision
	Mergeability domain.Mergeability
	UpdatedAt    time.Time
}

// PeekSessionPRs fetches the session's non-terminal tracked PRs fresh from
// the provider, read-only, without touching the stored rows. The SCM observer
// skips terminated sessions (discoverSubjects drops them), so a frozen record
// would otherwise render a stale snapshot as live forever -- the PR card
// calls this when it opens onto facts older than its staleness threshold and
// shows the response instead (#338). A PR the provider fails to fetch for is
// left out; the card keeps showing its snapshot as stale.
func (s *Service) PeekSessionPRs(ctx context.Context, id domain.SessionID) ([]PeekedPRFacts, error) {
	if s.scm == nil {
		return nil, ErrSCMUnavailable
	}
	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return nil, err
	}
	// The card's summaries fold AO's own review verdict into the provider's
	// aggregate (GitHub hides a review published under the author's account),
	// so the peek must hand back the same folded decision: a raw provider
	// aggregate would overwrite a folded changes_requested with none and paint
	// the card green against AO's verdict.
	headRuns, err := s.store.ListCurrentHeadReviewRunsForSession(ctx, id)
	if err != nil {
		return nil, err
	}
	latestRuns := latestReviewRunByPR(headRuns)
	refs := make([]ports.SCMPRRef, 0, len(prs))
	for _, pr := range prs {
		// A terminal row cannot un-merge; reconciling one is the observer's job.
		if pr.Merged || pr.Closed {
			continue
		}
		repo, err := scmRepoForClaim(pr.URL)
		if err != nil {
			continue
		}
		refs = append(refs, ports.SCMPRRef{Repo: repo, Number: pr.Number, URL: pr.URL})
	}
	if len(refs) == 0 {
		return nil, nil
	}
	batch, err := s.scm.FetchPullRequests(ctx, refs)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSCMUnavailable, err)
	}
	now := s.now().UTC()
	out := make([]PeekedPRFacts, 0, len(batch))
	for _, obs := range batch {
		if !obs.Fetched {
			continue
		}
		if obs.PR.URL == "" {
			continue
		}
		out = append(out, PeekedPRFacts{
			URL:          obs.PR.URL,
			Number:       obs.PR.Number,
			State:        domain.PRState(obs.PR.State),
			CI:           domain.CIState(obs.CI.Summary),
			Review:       foldAORunDecision(domain.ReviewDecision(obs.Review.Decision), latestRuns[obs.PR.URL]),
			Mergeability: domain.Mergeability(obs.Mergeability.State),
			UpdatedAt:    now,
		})
	}
	return out, nil
}
