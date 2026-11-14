package pr

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Retarget points one open pull request stacked on a merged branch at the
// repository's default branch, so the stack's next merge lands where close
// keywords and releases look. The base is the provider's default branch, read
// at call time; the client only names the child.
func (s *ActionService) Retarget(ctx context.Context, request RetargetRequest) (RetargetResult, error) {
	prNumber, err := parsePRNumber(request.PRID)
	if err != nil || strings.TrimSpace(request.PRURL) == "" {
		return RetargetResult{}, fmt.Errorf("%w: invalid pull request identity", ErrInvalidPR)
	}
	if s.store == nil {
		return RetargetResult{}, errors.New("pr: retarget action is not configured")
	}
	tracked, ok, err := s.store.GetPR(ctx, request.PRURL)
	if err != nil {
		return RetargetResult{}, fmt.Errorf("load pull request: %w", err)
	}
	if !ok || tracked.Number != prNumber {
		return RetargetResult{}, ErrPRNotFound
	}
	if s.baseChanger == nil {
		return RetargetResult{}, ErrUnsupported
	}
	if request.Child <= 0 {
		return RetargetResult{}, fmt.Errorf("%w: child pull request number is required", ErrInvalidAction)
	}
	if request.Child == tracked.Number {
		return RetargetResult{}, fmt.Errorf("%w: a pull request cannot retarget itself", ErrInvalidAction)
	}
	repo, ok := scmRepoForPR(tracked)
	if !ok {
		return RetargetResult{}, fmt.Errorf("%w: pull request repository is unknown", ErrPRPreconditions)
	}
	base, err := s.providerDefaultBranch(ctx, repo)
	if err != nil {
		return RetargetResult{}, err
	}
	ref := ports.SCMPRRef{Repo: repo, Number: request.Child}
	if err := s.baseChanger.ChangePullRequestBase(ctx, ref, base); err != nil {
		switch {
		case errors.Is(err, ports.ErrSCMNotFound):
			return RetargetResult{}, fmt.Errorf("%w: %w", ErrPRNotFound, err)
		case errors.Is(err, ports.ErrSCMRejected):
			return RetargetResult{}, fmt.Errorf("%w: %w", ErrPRRejected, err)
		case errors.Is(err, ports.ErrSCMUnsupported):
			return RetargetResult{}, fmt.Errorf("%w: %w", ErrUnsupported, err)
		default:
			return RetargetResult{}, fmt.Errorf("retarget pull request: %w", err)
		}
	}
	at := s.clock().UTC()
	s.logger.Info("pull request retargeted to the default branch",
		"pr", tracked.URL, "child", request.Child, "base", base, "changed_at", at)
	return RetargetResult{Child: request.Child, Base: base, StateChangedAt: at}, nil
}

// DefaultBranch reads the tracked PR repository's default branch, the fact the
// merge confirmation compares the PR's base against.
func (s *ActionService) DefaultBranch(ctx context.Context, prID, prURL string) (string, error) {
	prNumber, err := parsePRNumber(prID)
	if err != nil || strings.TrimSpace(prURL) == "" {
		return "", fmt.Errorf("%w: invalid pull request identity", ErrInvalidPR)
	}
	if s.store == nil {
		return "", errors.New("pr: retarget action is not configured")
	}
	tracked, ok, err := s.store.GetPR(ctx, prURL)
	if err != nil {
		return "", fmt.Errorf("load pull request: %w", err)
	}
	if !ok || tracked.Number != prNumber {
		return "", ErrPRNotFound
	}
	repo, ok := scmRepoForPR(tracked)
	if !ok {
		return "", fmt.Errorf("%w: pull request repository is unknown", ErrPRPreconditions)
	}
	return s.providerDefaultBranch(ctx, repo)
}

// providerDefaultBranch reads the default branch through the provider, mapping
// a missing capability to ErrUnsupported.
func (s *ActionService) providerDefaultBranch(ctx context.Context, repo ports.SCMRepo) (string, error) {
	if s.repoReader == nil {
		return "", ErrUnsupported
	}
	branch, err := s.repoReader.DefaultBranch(ctx, repo)
	if err != nil {
		return "", fmt.Errorf("read default branch: %w", err)
	}
	return branch, nil
}

// logNonDefaultBaseMerge records the audit trace of a merge that landed
// anywhere but the repository's default branch, so incidents like a stack
// child merged into its parent's head branch stay findable. The read is
// best effort: an unreachable provider only skips the log line.
func (s *ActionService) logNonDefaultBaseMerge(ctx context.Context, repo ports.SCMRepo, pr ports.SCMPRObservation, mergeCommitSHA string) {
	if s.repoReader == nil || strings.TrimSpace(pr.TargetBranch) == "" {
		return
	}
	def, err := s.repoReader.DefaultBranch(ctx, repo)
	if err != nil || strings.EqualFold(pr.TargetBranch, def) {
		return
	}
	s.logger.Warn("pull request merged into a non-default base",
		"pr", pr.URL,
		"number", pr.Number,
		"base", pr.TargetBranch,
		"default_branch", def,
		"merge_commit", mergeCommitSHA,
	)
}

// retargetableChildren names the open pull requests still stacked on the
// merged branch, as the merge outcome's retarget offer. A provider that
// cannot list by base branch, or that fails the read, returns nothing: the
// merge itself already succeeded and must not look failed.
func (s *ActionService) retargetableChildren(ctx context.Context, repo ports.SCMRepo, headBranch string, landed []int) []int {
	if s.baseLister == nil || strings.TrimSpace(headBranch) == "" {
		return nil
	}
	refs, err := s.baseLister.FindOpenPRsByBaseBranch(ctx, repo, headBranch)
	if err != nil {
		s.logger.Warn("read pull requests stacked on the merged branch", "base", headBranch, "err", err)
		return nil
	}
	done := make(map[int]bool, len(landed))
	for _, number := range landed {
		done[number] = true
	}
	var children []int
	for _, ref := range refs {
		if ref.Number <= 0 || done[ref.Number] {
			continue
		}
		children = append(children, ref.Number)
	}
	return children
}
