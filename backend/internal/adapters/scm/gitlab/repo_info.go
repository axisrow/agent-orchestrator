package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMBaseChanger = (*Provider)(nil)
var _ ports.SCMRepoReader = (*Provider)(nil)

// ChangePullRequestBase retargets a GitLab merge request onto another base
// branch through its target_branch field. The response's target_branch must
// name the requested branch, so a provider that silently kept the old base
// reads as a refusal rather than a success.
func (p *Provider) ChangePullRequestBase(ctx context.Context, ref ports.SCMPRRef, base string) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("gitlab scm: base provider is not configured")
	}
	base = strings.TrimSpace(base)
	if ref.Number <= 0 || strings.TrimSpace(ref.Repo.Owner) == "" || strings.TrimSpace(ref.Repo.Name) == "" || base == "" {
		return fmt.Errorf("gitlab scm: invalid merge request base change")
	}
	client, err := p.clientForRepoErr(ref.Repo)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/projects/%s/merge_requests/%d", projectPath(ref.Repo.Owner, ref.Repo.Name), ref.Number)
	resp, err := client.doMERGE(ctx, path, nil, struct {
		TargetBranch string `json:"target_branch"`
	}{TargetBranch: base})
	if err != nil {
		return mapBaseMutationStatus(resp.StatusCode, err)
	}
	var mr struct {
		TargetBranch string `json:"target_branch"`
	}
	if err := json.Unmarshal(resp.Body, &mr); err != nil {
		return fmt.Errorf("gitlab scm: decode merge request base response: %w", err)
	}
	if !strings.EqualFold(mr.TargetBranch, base) {
		return fmt.Errorf("%w: merge request base is %q, want %q", ports.ErrSCMRejected, mr.TargetBranch, base)
	}
	return nil
}

// mapBaseMutationStatus classifies a failed REST base change the same way the
// provider classifies its other mutations: 404 is an unknown merge request,
// and 405, 409 or 422 is GitLab refusing it.
func mapBaseMutationStatus(status int, err error) error {
	switch status {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %w", ports.ErrSCMNotFound, err)
	case http.StatusUnprocessableEntity, http.StatusConflict, http.StatusMethodNotAllowed:
		return fmt.Errorf("%w: %w", ports.ErrSCMRejected, err)
	default:
		return err
	}
}

// DefaultBranch reads the project's default branch, the target every merge
// request is expected to land on unless it deliberately stacks elsewhere.
func (p *Provider) DefaultBranch(ctx context.Context, repo ports.SCMRepo) (string, error) {
	if p == nil || p.client == nil {
		return "", fmt.Errorf("gitlab scm: repository provider is not configured")
	}
	if strings.TrimSpace(repo.Owner) == "" || strings.TrimSpace(repo.Name) == "" {
		return "", fmt.Errorf("gitlab scm: invalid project reference")
	}
	client, err := p.clientForRepoErr(repo)
	if err != nil {
		return "", err
	}
	resp, err := client.doGET(ctx, fmt.Sprintf("/projects/%s", projectPath(repo.Owner, repo.Name)), nil)
	if err != nil {
		return "", err
	}
	var project struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(resp.Body, &project); err != nil {
		return "", fmt.Errorf("gitlab scm: decode project: %w", err)
	}
	if strings.TrimSpace(project.DefaultBranch) == "" {
		return "", fmt.Errorf("gitlab scm: project %s has no default branch", repo.Repo)
	}
	return project.DefaultBranch, nil
}
