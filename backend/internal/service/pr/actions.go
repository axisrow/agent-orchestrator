package pr

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ActionManager is the controller-facing contract for guarded PR mutations
// and PR identity resolution.
type ActionManager interface {
	GetIdentity(ctx context.Context, prID string) (PRIdentity, error)
	Merge(ctx context.Context, request MergeRequest) (MergeResult, error)
	ResolveComments(ctx context.Context, prID string, commentIDs []string) (ResolveResult, error)
}

// PRIdentity is the tracked pull request identity a caller needs to observe
// before mutating: the number, its provider URL, and the tracked head SHA.
type PRIdentity struct {
	Number  int
	URL     string
	HeadSHA string
}

// MergeRequest identifies the tracked PR and pins the mutation to the exact
// head the user saw when they clicked Merge.
type MergeRequest struct {
	PRID            string
	PRURL           string
	ExpectedHeadSHA string
}

// MergeResult is the successful outcome of a PR merge.
type MergeResult struct {
	PRNumber       int
	Method         string
	State          domain.PRState
	MergeCommitSHA string
	StateChangedAt time.Time
}

// ResolveResult is the successful outcome of a resolve-comments operation.
type ResolveResult struct {
	Resolved int
}
