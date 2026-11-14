package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMRepoReader = (*Provider)(nil)

// DefaultBranch reads the repository's default branch, the target every pull
// request is expected to land on unless it deliberately stacks elsewhere.
func (p *Provider) DefaultBranch(ctx context.Context, repo ports.SCMRepo) (string, error) {
	if p == nil || p.client == nil {
		return "", fmt.Errorf("github scm: repository provider is not configured")
	}
	if strings.TrimSpace(repo.Owner) == "" || strings.TrimSpace(repo.Name) == "" {
		return "", fmt.Errorf("github scm: invalid repository reference")
	}
	resp, err := p.client.doREST(ctx, http.MethodGet, repoPath(repo.Owner, repo.Name), nil, nil)
	if err != nil {
		return "", err
	}
	var repository struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(resp.Body, &repository); err != nil {
		return "", fmt.Errorf("github scm: decode repository: %w", err)
	}
	if strings.TrimSpace(repository.DefaultBranch) == "" {
		return "", fmt.Errorf("github scm: repository %s has no default branch", repoFullName(repo))
	}
	return repository.DefaultBranch, nil
}
