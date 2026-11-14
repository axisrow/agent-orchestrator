package gitlab

import (
	"encoding/json"
	"net/http"
	"testing"
)

const gitlabMRBasePath = "/api/v4/projects/myorg%2Fmyrepo/merge_requests/42"

func TestChangePullRequestBase_PutsTargetBranchAndConfirmsIt(t *testing.T) {
	_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.EscapedPath() != gitlabMRBasePath {
			t.Fatalf("request = %s %s", r.Method, r.URL.EscapedPath())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 1 || body["target_branch"] != "main" {
			t.Fatalf("body = %#v, want only target_branch", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"iid": 42, "target_branch": "main"})
	}))
	if err := p.ChangePullRequestBase(ctx(), validMergeRequest().PR, "main"); err != nil {
		t.Fatal(err)
	}
}

func TestChangePullRequestBase_RefusesUnexpectedBase(t *testing.T) {
	_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"iid": 42, "target_branch": "develop"})
	}))
	if err := p.ChangePullRequestBase(ctx(), validMergeRequest().PR, "main"); err == nil {
		t.Fatal("a provider that kept the old base must not read as success")
	}
}

func TestDefaultBranch_ReadsProject(t *testing.T) {
	_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/v4/projects/myorg%2Fmyrepo" {
			t.Fatalf("request = %s %s", r.Method, r.URL.EscapedPath())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"path_with_namespace": "myorg/myrepo", "default_branch": "main"})
	}))
	branch, err := p.DefaultBranch(ctx(), validMergeRequest().PR.Repo)
	if err != nil || branch != "main" {
		t.Fatalf("branch = %q, err = %v", branch, err)
	}
}

func TestFindOpenPRsByBaseBranch_FiltersByTargetBranch(t *testing.T) {
	_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v4/projects/myorg%2Fmyrepo/merge_requests" {
			t.Fatalf("path = %s", r.URL.EscapedPath())
		}
		q := r.URL.Query()
		if q.Get("target_branch") != "parent-head" || q.Get("state") != "opened" {
			t.Fatalf("query = %v", q)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"iid": 604, "web_url": "https://gitlab.com/myorg/myrepo/-/merge_requests/604", "source_branch": "child", "target_branch": "parent-head"},
		})
	}))
	refs, err := p.FindOpenPRsByBaseBranch(ctx(), validMergeRequest().PR.Repo, "parent-head")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Number != 604 || refs[0].URL == "" {
		t.Fatalf("refs = %#v", refs)
	}
}
