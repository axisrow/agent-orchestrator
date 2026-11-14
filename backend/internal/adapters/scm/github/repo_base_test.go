package github

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestChangePullRequestBase_PatchesBaseAndConfirmsIt(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPatch, "/repos/octocat/hello/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 1 || body["base"] != "master" {
			t.Fatalf("body = %#v, want only the base field", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 42, "base": map[string]any{"ref": "master"}})
	})
	if err := newProviderForTest(t, f).ChangePullRequestBase(ctx(), validMergeRequest().PR, "master"); err != nil {
		t.Fatal(err)
	}
}

func TestChangePullRequestBase_RefusesUnexpectedBase(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPatch, "/repos/octocat/hello/pulls/42", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 42, "base": map[string]any{"ref": "develop"}})
	})
	err := newProviderForTest(t, f).ChangePullRequestBase(ctx(), validMergeRequest().PR, "master")
	if err == nil {
		t.Fatal("a provider that kept the old base must not read as success")
	}
}

func TestChangePullRequestBase_ClassifiesProviderRefusal(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPatch, "/repos/octocat/hello/pulls/42", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnprocessableEntity)
	})
	if err := newProviderForTest(t, f).ChangePullRequestBase(ctx(), validMergeRequest().PR, "master"); err == nil {
		t.Fatal("422 must surface as an error")
	}
}

func TestDefaultBranch_ReadsRepository(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octocat/hello", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "hello", "default_branch": "main"})
	})
	branch, err := newProviderForTest(t, f).DefaultBranch(ctx(), validMergeRequest().PR.Repo)
	if err != nil || branch != "main" {
		t.Fatalf("branch = %q, err = %v", branch, err)
	}
}

func TestDefaultBranch_MissingBranchIsAnError(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octocat/hello", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "hello"})
	})
	if _, err := newProviderForTest(t, f).DefaultBranch(ctx(), validMergeRequest().PR.Repo); err == nil {
		t.Fatal("a repository with no default branch must error, not return empty")
	}
}

func TestFindOpenPRsByBaseBranch_ListsServerFilteredPulls(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octocat/hello/pulls", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("base"); got != "parent-head" {
			t.Fatalf("base filter = %q", got)
		}
		if got := r.URL.Query().Get("state"); got != "open" {
			t.Fatalf("state = %q", got)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"number": 604, "html_url": "https://github.com/octocat/hello/pull/604"},
			{"number": 605, "html_url": "https://github.com/octocat/hello/pull/605"},
		})
	})
	refs, err := newProviderForTest(t, f).FindOpenPRsByBaseBranch(ctx(), validMergeRequest().PR.Repo, "parent-head")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].Number != 604 || refs[1].Number != 605 || refs[0].URL == "" {
		t.Fatalf("refs = %#v", refs)
	}
}
