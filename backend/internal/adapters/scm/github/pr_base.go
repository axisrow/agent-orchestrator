package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMBaseChanger = (*Provider)(nil)

// ChangePullRequestBase retargets a GitHub pull request by patching its base
// field, the provider's own update of the base reference. The response's
// base.ref must name the requested branch, so a provider that silently kept
// the old base reads as a refusal rather than a success.
func (p *Provider) ChangePullRequestBase(ctx context.Context, ref ports.SCMPRRef, base string) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("github scm: base provider is not configured")
	}
	base = strings.TrimSpace(base)
	if ref.Number <= 0 || strings.TrimSpace(ref.Repo.Owner) == "" || strings.TrimSpace(ref.Repo.Name) == "" || base == "" {
		return fmt.Errorf("github scm: invalid pull request base change")
	}
	payload := struct {
		Base string `json:"base"`
	}{Base: base}
	resp, err := p.client.doREST(ctx, http.MethodPatch,
		repoPath(ref.Repo.Owner, ref.Repo.Name, "pulls", strconv.Itoa(ref.Number)), nil, payload)
	if err != nil {
		return mapBaseMutationStatus(resp.StatusCode, err)
	}
	var result struct {
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return fmt.Errorf("github scm: decode pull request base response: %w", err)
	}
	if !strings.EqualFold(result.Base.Ref, base) {
		return fmt.Errorf("%w: pull request base is %q, want %q", ports.ErrSCMRejected, result.Base.Ref, base)
	}
	return nil
}

// mapBaseMutationStatus classifies a failed REST base change: 404 is an
// unknown PR, and 405, 409 or 422 is GitHub refusing it.
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

// FindOpenPRsByBaseBranch lists the open pull requests whose base is branch in
// the base repository, where a stack's children live. One server-filtered
// request; a repository rarely holds more than a page of pull requests on one
// base, and the first 100 cover every realistic stack.
func (p *Provider) FindOpenPRsByBaseBranch(ctx context.Context, repo ports.SCMRepo, base string) ([]ports.SCMPRRef, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("github scm: base provider is not configured")
	}
	resp, err := p.client.doREST(ctx, http.MethodGet, repoPath(repo.Owner, repo.Name, "pulls"), url.Values{
		"base":     {base},
		"state":    {"open"},
		"per_page": {"100"},
	}, nil)
	if err != nil {
		return nil, err
	}
	var pulls []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(resp.Body, &pulls); err != nil {
		return nil, fmt.Errorf("github scm: decode pull requests: %w", err)
	}
	refs := make([]ports.SCMPRRef, 0, len(pulls))
	for _, pull := range pulls {
		refs = append(refs, ports.SCMPRRef{Repo: repo, Number: pull.Number, URL: pull.HTMLURL})
	}
	return refs, nil
}
