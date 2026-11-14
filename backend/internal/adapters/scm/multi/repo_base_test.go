package multi

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// retargetingProvider supports the base change, default-branch and base-branch
// listing capabilities.
type retargetingProvider struct {
	fakeProvider
	baseRefs   []ports.SCMPRRef
	baseBranch []string
	branch     string
	listed     []ports.SCMPRRef
}

func (r *retargetingProvider) ChangePullRequestBase(_ context.Context, pr ports.SCMPRRef, base string) error {
	r.baseRefs = append(r.baseRefs, pr)
	r.baseBranch = append(r.baseBranch, base)
	return nil
}

func (r *retargetingProvider) DefaultBranch(context.Context, ports.SCMRepo) (string, error) {
	return r.branch, nil
}

func (r *retargetingProvider) FindOpenPRsByBaseBranch(_ context.Context, _ ports.SCMRepo, _ string) ([]ports.SCMPRRef, error) {
	return r.listed, nil
}

func TestBaseCapabilitiesRouteByProviderAndReportUnsupported(t *testing.T) {
	gh := &retargetingProvider{fakeProvider: fakeProvider{key: "github"}, branch: "main", listed: []ports.SCMPRRef{{Number: 604}}}
	gl := &fakeProvider{key: "gitlab"}
	m := New(NamedProvider{Key: "github", Provider: gh}, NamedProvider{Key: "gitlab", Provider: gl})
	glRef := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "gitlab"}, Number: 1}

	if err := m.ChangePullRequestBase(context.Background(), glRef, "main"); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("base change error = %v, want unsupported", err)
	}
	if _, err := m.DefaultBranch(context.Background(), glRef.Repo); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("default branch error = %v, want unsupported", err)
	}
	if _, err := m.FindOpenPRsByBaseBranch(context.Background(), glRef.Repo, "x"); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("base listing error = %v, want unsupported", err)
	}

	ghRef := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github"}, Number: 603}
	if err := m.ChangePullRequestBase(context.Background(), ghRef, "main"); err != nil || len(gh.baseRefs) != 1 || gh.baseRefs[0].Number != 603 || gh.baseBranch[0] != "main" {
		t.Fatalf("base change err = %v, refs = %#v, bases = %v", err, gh.baseRefs, gh.baseBranch)
	}
	if branch, err := m.DefaultBranch(context.Background(), ghRef.Repo); err != nil || branch != "main" {
		t.Fatalf("branch = %q, err = %v", branch, err)
	}
	if refs, err := m.FindOpenPRsByBaseBranch(context.Background(), ghRef.Repo, "head"); err != nil || len(refs) != 1 || refs[0].Number != 604 {
		t.Fatalf("refs = %#v, err = %v", refs, err)
	}
}
