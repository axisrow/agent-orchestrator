package pr

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeBaseLister struct {
	byBase map[string][]ports.SCMPRRef
	err    error
	asked  []string
}

func (f *fakeBaseLister) FindOpenPRsByBaseBranch(_ context.Context, _ ports.SCMRepo, base string) ([]ports.SCMPRRef, error) {
	f.asked = append(f.asked, base)
	if f.err != nil {
		return nil, f.err
	}
	return f.byBase[base], nil
}

type fakeBaseChanger struct {
	refs  []ports.SCMPRRef
	bases []string
	err   error
}

func (f *fakeBaseChanger) ChangePullRequestBase(_ context.Context, pr ports.SCMPRRef, base string) error {
	f.refs = append(f.refs, pr)
	f.bases = append(f.bases, base)
	return f.err
}

type fakeRepoReader struct {
	branch string
	err    error
	calls  int
}

func (f *fakeRepoReader) DefaultBranch(context.Context, ports.SCMRepo) (string, error) {
	f.calls++
	return f.branch, f.err
}

// A merge lists the open pull requests still stacked on its head branch, so
// the card can offer retargeting them; the merged PR itself never appears.
func TestActionServiceMerge_ListsChildrenStackedOnTheMergedBranch(t *testing.T) {
	pr, scm := mergeableActionFixture()
	scm.observation.PR.SourceBranch = "feature-x"
	scm.observation.PR.TargetBranch = "main"
	lister := &fakeBaseLister{byBase: map[string][]ports.SCMPRRef{"feature-x": {
		{Number: 604, URL: "https://github.com/acme/widgets/pull/604"},
		{Number: 42},
	}}}
	svc := NewActionService(ActionDeps{
		Store:      &fakeActionStore{pr: pr, ok: true},
		Reader:     scm,
		Merger:     scm,
		RepoReader: &fakeRepoReader{branch: "main"},
		BaseLister: lister,
	})

	result, err := svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Retargetable; len(got) != 1 || got[0] != 604 {
		t.Fatalf("retargetable = %v, want [604]: the merged PR itself is not its own child", got)
	}
	if len(lister.asked) != 1 || lister.asked[0] != "feature-x" {
		t.Fatalf("asked = %v, want the merged head branch once", lister.asked)
	}
}

// A merge into a side branch is the audit event: warn with the base, the
// default it dodged, and the merge commit.
func TestActionServiceMerge_NonDefaultBaseIsLogged(t *testing.T) {
	pr, scm := mergeableActionFixture()
	scm.observation.PR.SourceBranch = "ifc-display"
	scm.observation.PR.TargetBranch = "ao/ifc-import/ifc-reading"
	var logs bytes.Buffer
	svc := NewActionService(ActionDeps{
		Store:      &fakeActionStore{pr: pr, ok: true},
		Reader:     scm,
		Merger:     scm,
		RepoReader: &fakeRepoReader{branch: "master"},
		Logger:     slog.New(slog.NewTextHandler(&logs, nil)),
	})

	if _, err := svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"merged into a non-default base", "base=ao/ifc-import/ifc-reading", "default_branch=master", "merge_commit=merge-sha"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("audit log missing %q: %s", want, logs.String())
		}
	}
}

// A default-branch merge stays log-quiet about bases even with the reader wired.
func TestActionServiceMerge_DefaultBaseLogsNothing(t *testing.T) {
	pr, scm := mergeableActionFixture()
	scm.observation.PR.TargetBranch = "master"
	var logs bytes.Buffer
	svc := NewActionService(ActionDeps{
		Store:      &fakeActionStore{pr: pr, ok: true},
		Reader:     scm,
		Merger:     scm,
		RepoReader: &fakeRepoReader{branch: "master"},
		Logger:     slog.New(slog.NewTextHandler(&logs, nil)),
	})

	if _, err := svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "non-default") {
		t.Fatalf("default merge logged a base warning: %s", logs.String())
	}
}

func retargetFixture(t *testing.T) (domain.PullRequest, *fakeBaseChanger, *fakeRepoReader, *bytes.Buffer, *ActionService) {
	t.Helper()
	pr := domain.PullRequest{URL: "https://github.com/acme/widgets/pull/603", Number: 603, Provider: "github", Host: "github.com", Repo: "acme/widgets"}
	changer := &fakeBaseChanger{}
	reader := &fakeRepoReader{branch: "master"}
	var logs bytes.Buffer
	svc := NewActionService(ActionDeps{
		Store:       &fakeActionStore{pr: pr, ok: true},
		BaseChanger: changer,
		RepoReader:  reader,
		Logger:      slog.New(slog.NewTextHandler(&logs, nil)),
	})
	return pr, changer, reader, &logs, svc
}

func TestActionServiceRetarget_PointsChildAtProviderDefault(t *testing.T) {
	pr, changer, _, logs, svc := retargetFixture(t)

	res, err := svc.Retarget(context.Background(), RetargetRequest{PRID: "603", PRURL: pr.URL, Child: 604})
	if err != nil {
		t.Fatal(err)
	}
	if res.Child != 604 || res.Base != "master" {
		t.Fatalf("result = %#v", res)
	}
	if len(changer.refs) != 1 || changer.refs[0].Number != 604 || changer.bases[0] != "master" {
		t.Fatalf("changer = %#v %v", changer.refs, changer.bases)
	}
	if !strings.Contains(logs.String(), "retargeted to the default branch") || !strings.Contains(logs.String(), "child=604") {
		t.Fatalf("missing retarget audit line: %s", logs.String())
	}
}

func TestActionServiceRetarget_RejectsSelfAndUnknownChild(t *testing.T) {
	pr, changer, _, _, svc := retargetFixture(t)
	if _, err := svc.Retarget(context.Background(), RetargetRequest{PRID: "603", PRURL: pr.URL}); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("missing child err = %v", err)
	}
	if _, err := svc.Retarget(context.Background(), RetargetRequest{PRID: "603", PRURL: pr.URL, Child: 603}); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("self retarget err = %v", err)
	}
	if len(changer.refs) != 0 {
		t.Fatalf("provider called %d times for rejected requests", len(changer.refs))
	}
}

func TestActionServiceRetarget_MapsProviderErrors(t *testing.T) {
	for _, tc := range []struct {
		provider error
		want     error
	}{
		{ports.ErrSCMNotFound, ErrPRNotFound},
		{ports.ErrSCMRejected, ErrPRRejected},
		{ports.ErrSCMUnsupported, ErrUnsupported},
	} {
		pr, _, _, _, svc := retargetFixture(t)
		svc.baseChanger = &fakeBaseChanger{err: tc.provider}
		if _, err := svc.Retarget(context.Background(), RetargetRequest{PRID: "603", PRURL: pr.URL, Child: 604}); !errors.Is(err, tc.want) {
			t.Fatalf("provider %v mapped to %v, want %v", tc.provider, err, tc.want)
		}
	}
}

func TestActionServiceRetarget_UnsupportedWithoutCapability(t *testing.T) {
	pr := domain.PullRequest{URL: "https://github.com/acme/widgets/pull/603", Number: 603, Provider: "github", Host: "github.com", Repo: "acme/widgets"}
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}})
	if _, err := svc.Retarget(context.Background(), RetargetRequest{PRID: "603", PRURL: pr.URL, Child: 604}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("no changer err = %v", err)
	}
	if _, err := svc.DefaultBranch(context.Background(), "603", pr.URL); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("no reader err = %v", err)
	}
}

func TestActionServiceDefaultBranch_ReadsProvider(t *testing.T) {
	pr := domain.PullRequest{URL: "https://github.com/acme/widgets/pull/603", Number: 603, Provider: "github", Host: "github.com", Repo: "acme/widgets"}
	reader := &fakeRepoReader{branch: "main"}
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, RepoReader: reader})

	branch, err := svc.DefaultBranch(context.Background(), "603", pr.URL)
	if err != nil || branch != "main" {
		t.Fatalf("branch = %q err = %v", branch, err)
	}
	if reader.calls != 1 {
		t.Fatalf("reader calls = %d, want 1", reader.calls)
	}
}
