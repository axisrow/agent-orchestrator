package ports

import (
	"context"
	"errors"
)

// ErrSCMHeadChanged indicates that the pull request advanced beyond the head
// SHA supplied with the mutation.
var ErrSCMHeadChanged = errors.New("scm: pull request head changed")

// ErrSCMNotMergeable indicates that the provider rejected the pull request's
// current mergeability state.
var ErrSCMNotMergeable = errors.New("scm: pull request not mergeable")

// ErrSCMUnsupported indicates that the configured SCM provider does not support
// the requested mutation.
var ErrSCMUnsupported = errors.New("scm: unsupported operation")

// ErrSCMRejected indicates that the provider refused a well-formed mutation,
// for example an invalid base change or a validation failure.
var ErrSCMRejected = errors.New("scm: provider rejected mutation")

// SCMMergeMethod identifies the provider merge strategy.
type SCMMergeMethod string

// SCMMergeSquash requests a squash merge from the provider.
const SCMMergeSquash SCMMergeMethod = "squash"

// SCMMergeRequest uses ExpectedHeadSHA as a compare-and-swap guard. Provider
// implementations must reject the mutation if the live head has advanced.
type SCMMergeRequest struct {
	PR              SCMPRRef
	ExpectedHeadSHA string
	Method          SCMMergeMethod
}

// SCMMergeResult reports the provider's resulting merge commit.
type SCMMergeResult struct {
	MergeCommitSHA string
}

// MergeInitiator classifies who drove an observed PR merge, for the merge
// notice the orchestrator receives (fork issue #243). Only AO's own merge API
// marks its context; every other path — the SCM observer seeing a merge made
// in the GitHub UI, via gh CLI, or by auto-merge — reads as outside.
type MergeInitiator string

// MergeInitiatorAO marks a merge initiated through AO's merge API by the user.
const MergeInitiatorAO MergeInitiator = "ao"

type mergeInitiatorKey struct{}

// WithMergeInitiator annotates a service-call context with the merge
// initiator, so lifecycle can attribute the merge notice without widening the
// SCM observation DTO for a fact the provider does not report.
func WithMergeInitiator(ctx context.Context, initiator MergeInitiator) context.Context {
	return context.WithValue(ctx, mergeInitiatorKey{}, initiator)
}

// MergeInitiatorFromContext reads the merge initiator back. The zero value
// means the merge did not originate from AO's API.
func MergeInitiatorFromContext(ctx context.Context) MergeInitiator {
	initiator, _ := ctx.Value(mergeInitiatorKey{}).(MergeInitiator)
	return initiator
}

// SCMMerger mutates pull requests through an SCM provider.
type SCMMerger interface {
	MergePullRequest(ctx context.Context, request SCMMergeRequest) (SCMMergeResult, error)
}

// SCMReviewRequest asks the provider to request another review from Reviewer.
type SCMReviewRequest struct {
	PR       SCMPRRef
	Reviewer string
}

// SCMReviewRequester mutates pull-request review requests through an SCM provider.
type SCMReviewRequester interface {
	RequestReview(ctx context.Context, request SCMReviewRequest) error
}

// SCMReviewResolveRequest asks the provider to resolve one review thread.
type SCMReviewResolveRequest struct {
	PR       SCMPRRef
	ThreadID string
}

// SCMReviewResolver resolves pull-request review threads through an SCM provider.
type SCMReviewResolver interface {
	ResolveReviewThread(ctx context.Context, request SCMReviewResolveRequest) error
}

// SCMReviewComment is one inline comment of a daemon-published review.
type SCMReviewComment struct {
	Path string
	Line int
	Body string
}

// SCMReviewPublishRequest is one review the daemon publishes: a summary body
// plus optional inline comments, anchored at CommitSHA (empty = the PR's
// current head). Event is the provider review event ("APPROVE",
// "REQUEST_CHANGES", or "COMMENT"); providers default it to "COMMENT" when
// empty.
type SCMReviewPublishRequest struct {
	PR        SCMPRRef
	CommitSHA string
	Event     string
	Body      string
	Comments  []SCMReviewComment
}

// SCMReviewPublishResult reports the provider-created review. State is the
// review state the provider assigned (a ports-level echo of the provider's own
// vocabulary, e.g. GitHub's APPROVED); empty when the provider does not report
// one.
type SCMReviewPublishResult struct {
	ReviewID string
	HTMLURL  string
	State    string
}

// SCMReviewPublisher publishes pull-request reviews through an SCM provider.
// AO owns publication since #5701: reviewers submit to AO and never post to
// the provider themselves.
type SCMReviewPublisher interface {
	PublishReview(ctx context.Context, request SCMReviewPublishRequest) (SCMReviewPublishResult, error)
}

// SCMReviewPublicationFinder locates a review the daemon previously published,
// by the marker the service embeds in the review body. It is the recovery path
// for publications whose outcome was recorded as uncertain or interrupted: a
// found review proves the publication happened. The capability is optional —
// providers that cannot search published reviews do not implement it, and the
// service falls back to keeping the run's outcome unknown.
type SCMReviewPublicationFinder interface {
	FindPublishedReview(ctx context.Context, ref SCMPRRef, bodyMarker string) (SCMReviewPublishResult, bool, error)
}

// SCMBaseChanger retargets a pull request onto another base branch through an
// SCM provider, the provider's own update of the base reference.
type SCMBaseChanger interface {
	// ChangePullRequestBase points PR's base at base and confirms the provider
	// took it. Providers return ErrSCMRejected when they refuse the change.
	ChangePullRequestBase(ctx context.Context, pr SCMPRRef, base string) error
}

// SCMRepoReader reads repository-level facts through an SCM provider.
type SCMRepoReader interface {
	// DefaultBranch returns the repository's default branch name.
	DefaultBranch(ctx context.Context, repo SCMRepo) (string, error)
}

// SCMBaseBranchPRLister lists open pull requests by the branch they target, so
// the pull requests stacked on a merged branch can be found again. Providers
// return an empty list, not an error, when nothing targets the branch.
type SCMBaseBranchPRLister interface {
	// FindOpenPRsByBaseBranch returns the open pull requests whose base is
	// branch in the repository itself; pull requests from forks are not listed.
	FindOpenPRsByBaseBranch(ctx context.Context, repo SCMRepo, base string) ([]SCMPRRef, error)
}
