package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestPublishReviewPostsVerdictReviewWithInlineComments(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octo/hello/pulls/42/files", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("per_page") != "100" {
			t.Errorf("per_page = %q, want 100", req.URL.Query().Get("per_page"))
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"filename": "main.go"}, {"filename": "util.go"}})
	})
	f.on(http.MethodPost, "/repos/octo/hello/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Event    string `json:"event"`
			CommitID string `json:"commit_id"`
			Body     string `json:"body"`
			Comments []struct {
				Path string `json:"path"`
				Line int    `json:"line"`
				Body string `json:"body"`
			} `json:"comments"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if payload.Event != "REQUEST_CHANGES" {
			t.Errorf("event = %q, want REQUEST_CHANGES", payload.Event)
		}
		if payload.CommitID != "abc123" {
			t.Errorf("commit_id = %q, want abc123", payload.CommitID)
		}
		if !strings.Contains(payload.Body, "quotes \"here\"") {
			t.Errorf("body lost quoting: %q", payload.Body)
		}
		if len(payload.Comments) != 2 {
			t.Fatalf("comments = %#v, want 2 entries", payload.Comments)
		}
		if payload.Comments[0].Path != "main.go" || payload.Comments[0].Line != 12 || payload.Comments[0].Body != "off-by-one" {
			t.Errorf("comment[0] = %#v", payload.Comments[0])
		}
		if payload.Comments[1].Path != "util.go" || payload.Comments[1].Line != 3 || payload.Comments[1].Body != "unused var" {
			t.Errorf("comment[1] = %#v", payload.Comments[1])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 9001, "html_url": "https://github.com/octo/hello/pull/42#pullrequestreview-9001", "state": "CHANGES_REQUESTED"})
	})
	p := newProviderForTest(t, f)

	result, err := p.PublishReview(ctx(), ports.SCMReviewPublishRequest{
		PR:        ports.SCMPRRef{Number: 42, Repo: ports.SCMRepo{Owner: "octo", Name: "hello"}},
		CommitSHA: "abc123",
		// Issue #188: the daemon decides the event; the adapter must send it
		// verbatim and echo the state GitHub assigned back to the service.
		Event: "REQUEST_CHANGES",
		Body:  `Summary with quotes "here" and a code block.`,
		Comments: []ports.SCMReviewComment{
			{Path: "main.go", Line: 12, Body: "off-by-one"},
			{Path: "util.go", Line: 3, Body: "unused var"},
		},
	})
	if err != nil {
		t.Fatalf("PublishReview: %v", err)
	}
	if result.ReviewID != "9001" || result.HTMLURL != "https://github.com/octo/hello/pull/42#pullrequestreview-9001" {
		t.Fatalf("result = %#v", result)
	}
	if result.State != "CHANGES_REQUESTED" {
		t.Fatalf("result state = %q, want CHANGES_REQUESTED", result.State)
	}
}

func TestPublishReviewDefaultsToCommentEvent(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/repos/octo/hello/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Event string `json:"event"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if payload.Event != "COMMENT" {
			t.Errorf("event = %q, want COMMENT for an unspecified event", payload.Event)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 9002, "state": "COMMENTED"})
	})
	p := newProviderForTest(t, f)

	result, err := p.PublishReview(ctx(), ports.SCMReviewPublishRequest{
		PR:   ports.SCMPRRef{Number: 42, Repo: ports.SCMRepo{Owner: "octo", Name: "hello"}},
		Body: "body",
	})
	if err != nil {
		t.Fatalf("PublishReview: %v", err)
	}
	if result.State != "COMMENTED" {
		t.Fatalf("result state = %q, want COMMENTED", result.State)
	}
}

func TestPublishReviewOmitsCommitAndCommentsWhenAbsent(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/repos/octo/hello/pulls/7/reviews", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if _, ok := body["commit_id"]; ok {
			t.Errorf("empty commit id must be omitted: %v", body)
		}
		if _, ok := body["comments"]; ok {
			t.Errorf("empty comments must be omitted: %v", body)
		}
		if body["event"] != "COMMENT" {
			t.Errorf("event = %v, want COMMENT", body["event"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7})
	})
	p := newProviderForTest(t, f)

	if _, err := p.PublishReview(ctx(), ports.SCMReviewPublishRequest{
		PR:   ports.SCMPRRef{Number: 7, Repo: ports.SCMRepo{Owner: "octo", Name: "hello"}},
		Body: "ship it",
	}); err != nil {
		t.Fatalf("PublishReview: %v", err)
	}
}

func TestPublishReviewRejectsInvalidInputs(t *testing.T) {
	p := &Provider{}
	if _, err := p.PublishReview(ctx(), ports.SCMReviewPublishRequest{
		PR: ports.SCMPRRef{Number: 0, Repo: ports.SCMRepo{Owner: "octo", Name: "hello"}},
	}); err == nil {
		t.Fatal("zero PR number accepted")
	}
}

func TestPublishReviewRejectsResponseWithoutID(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/repos/octo/hello/pulls/42/reviews", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"html_url": "https://github.com/octo/hello/pull/42"})
	})
	p := newProviderForTest(t, f)

	// A 2xx response means GitHub created the review even when its handle is
	// unreadable: the outcome is unknown, never a definitive failure a rerun
	// would turn into a duplicate review.
	if _, err := p.PublishReview(ctx(), ports.SCMReviewPublishRequest{
		PR:   ports.SCMPRRef{Number: 42, Repo: ports.SCMRepo{Owner: "octo", Name: "hello"}},
		Body: "body",
	}); err == nil || !strings.Contains(err.Error(), "no id") || !errors.Is(err, ports.ErrSCMPublishOutcomeUnknown) {
		t.Fatalf("err = %v, want missing-id error wrapping ErrSCMPublishOutcomeUnknown", err)
	}
}

func TestPublishReviewWrapsUndecodableBodyAsUnknownOutcome(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/repos/octo/hello/pulls/42/reviews", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>gateway garbage</html>"))
	})
	p := newProviderForTest(t, f)

	if _, err := p.PublishReview(ctx(), ports.SCMReviewPublishRequest{
		PR:   ports.SCMPRRef{Number: 42, Repo: ports.SCMRepo{Owner: "octo", Name: "hello"}},
		Body: "body",
	}); err == nil || !strings.Contains(err.Error(), "decode created review") || !errors.Is(err, ports.ErrSCMPublishOutcomeUnknown) {
		t.Fatalf("err = %v, want decode error wrapping ErrSCMPublishOutcomeUnknown", err)
	}
}

func TestPublishReviewSurfacesEnvelopeErrorDetails(t *testing.T) {
	// Issue #221: GitHub puts the actionable reason for a 422 (e.g. an inline
	// anchor outside the reviewed diff) in the envelope's errors[], not in the
	// message field. The error text must carry those details so publish_error
	// distinguishes rejection classes without provider access.
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octo/hello/pulls/42/files", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"filename": "main.go"}})
	})
	f.on(http.MethodPost, "/repos/octo/hello/pulls/42/reviews", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Unprocessable Entity","errors":[{"resource":"PullRequestReviewComment","field":"line","code":"unprocessable","message":"line 189 is not part of the diff"},"anchor row out of range"]}`))
	})
	p := newProviderForTest(t, f)

	_, err := p.PublishReview(ctx(), ports.SCMReviewPublishRequest{
		PR:       ports.SCMPRRef{Number: 42, Repo: ports.SCMRepo{Owner: "octo", Name: "hello"}},
		Body:     "body",
		Comments: []ports.SCMReviewComment{{Path: "main.go", Line: 189, Body: "boom"}},
	})
	if err == nil {
		t.Fatal("PublishReview: want error")
	}
	for _, want := range []string{
		"422 Unprocessable Entity",
		"PullRequestReviewComment: line: unprocessable: line 189 is not part of the diff",
		"anchor row out of range",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

// Fork issue #372: GitHub rejects the entire review with 422 "Path could not
// be resolved" when one inline anchor names a path outside the PR diff, and
// the service's fallback turned everything into a plain COMMENT — the verdict
// and the valid inlines were lost. Anchors must be resolved against the diff's
// file list first: valid ones stay inline (rewritten to the diff's spelling
// when only a repo-root/worktree prefix differs), the invalid one is demoted
// into the body, and the verdict is published as requested.
func TestPublishReviewResolvesAnchorsAgainstDiffFiles(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octo/hello/pulls/42/files", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"filename": "main.go"}, {"filename": "util.go"}})
	})
	f.on(http.MethodPost, "/repos/octo/hello/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Event    string `json:"event"`
			Body     string `json:"body"`
			Comments []struct {
				Path string `json:"path"`
				Line int    `json:"line"`
				Body string `json:"body"`
			} `json:"comments"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if payload.Event != "REQUEST_CHANGES" {
			t.Errorf("event = %q, want REQUEST_CHANGES despite the unresolvable anchor", payload.Event)
		}
		if len(payload.Comments) != 2 {
			t.Fatalf("comments = %#v, want the 2 valid anchors", payload.Comments)
		}
		if payload.Comments[0].Path != "main.go" || payload.Comments[0].Line != 12 {
			t.Errorf("comment[0] = %#v, want main.go:12", payload.Comments[0])
		}
		// The prefix-off path was rewritten to the diff's spelling, not demoted.
		if payload.Comments[1].Path != "util.go" || payload.Comments[1].Line != 3 {
			t.Errorf("comment[1] = %#v, want backend/util.go rewritten to util.go:3", payload.Comments[1])
		}
		for _, want := range []string{"`docs/notes.md:5`", "stale note", "Findings without a diff anchor"} {
			if !strings.Contains(payload.Body, want) {
				t.Errorf("body %q missing %q", payload.Body, want)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 9100, "state": "CHANGES_REQUESTED"})
	})
	p := newProviderForTest(t, f)

	result, err := p.PublishReview(ctx(), ports.SCMReviewPublishRequest{
		PR:    ports.SCMPRRef{Number: 42, Repo: ports.SCMRepo{Owner: "octo", Name: "hello"}},
		Event: "REQUEST_CHANGES",
		Body:  "summary",
		Comments: []ports.SCMReviewComment{
			{Path: "main.go", Line: 12, Body: "off-by-one"},
			{Path: "backend/util.go", Line: 3, Body: "unused var"},
			{Path: "docs/notes.md", Line: 5, Body: "stale note"},
		},
	})
	if err != nil {
		t.Fatalf("PublishReview: %v", err)
	}
	if result.ReviewID != "9100" || result.State != "CHANGES_REQUESTED" {
		t.Fatalf("result = %#v, want review 9100 CHANGES_REQUESTED", result)
	}
}

// With no resolvable anchor at all the review still carries the verdict:
// comments are dropped entirely instead of provoking the 422, and every
// finding lands in the body with its path:line binding.
func TestPublishReviewDemotesAllAnchorsKeepsVerdict(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octo/hello/pulls/42/files", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"filename": "main.go"}})
	})
	f.on(http.MethodPost, "/repos/octo/hello/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Event    string        `json:"event"`
			Body     string        `json:"body"`
			Comments []interface{} `json:"comments"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if payload.Event != "REQUEST_CHANGES" {
			t.Errorf("event = %q, want REQUEST_CHANGES", payload.Event)
		}
		if payload.Comments != nil {
			t.Errorf("comments = %#v, want omitted", payload.Comments)
		}
		if !strings.Contains(payload.Body, "`missing.go:7` — boom") {
			t.Errorf("body %q missing the demoted finding", payload.Body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 9101, "state": "CHANGES_REQUESTED"})
	})
	p := newProviderForTest(t, f)

	if _, err := p.PublishReview(ctx(), ports.SCMReviewPublishRequest{
		PR:       ports.SCMPRRef{Number: 42, Repo: ports.SCMRepo{Owner: "octo", Name: "hello"}},
		Event:    "REQUEST_CHANGES",
		Body:     "summary",
		Comments: []ports.SCMReviewComment{{Path: "missing.go", Line: 7, Body: "boom"}},
	}); err != nil {
		t.Fatalf("PublishReview: %v", err)
	}
}

// An ambiguous prefix must never guess: two diff files matching the same
// anchor suffix demote the finding instead of anchoring it to either.
func TestMatchDiffPathRejectsAmbiguousPrefix(t *testing.T) {
	if path, ok := matchDiffPath("app/main.go", []string{"cmd/app/main.go", "web/app/main.go"}); ok {
		t.Fatalf("ambiguous prefix resolved to %q, want demotion", path)
	}
	if path, ok := matchDiffPath("main.go", []string{"main.go"}); !ok || path != "main.go" {
		t.Fatalf("exact match = %q %v, want main.go", path, ok)
	}
}

func TestFindPublishedReviewMatchesBodyMarker(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octo/hello/pulls/42/reviews", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("per_page") != "100" {
			t.Errorf("per_page = %q, want 100", req.URL.Query().Get("per_page"))
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "body": "an unrelated human review"},
			{"id": 9001, "body": "findings...\n\n<!-- ao-review-run:run-1 -->", "html_url": "https://github.com/octo/hello/pull/42#review-9001", "state": "COMMENTED"},
		})
	})
	p := newProviderForTest(t, f)

	result, found, err := p.FindPublishedReview(ctx(), ports.SCMPRRef{Number: 42, Repo: ports.SCMRepo{Owner: "octo", Name: "hello"}}, "<!-- ao-review-run:run-1 -->")
	if err != nil || !found {
		t.Fatalf("found = %v, err = %v, want the marked review", found, err)
	}
	if result.ReviewID != "9001" || result.HTMLURL != "https://github.com/octo/hello/pull/42#review-9001" {
		t.Fatalf("result = %+v, want review 9001", result)
	}
	// The found review's state rides along so recovery can record the state
	// GitHub actually assigned (issue #188).
	if result.State != "COMMENTED" {
		t.Fatalf("result state = %q, want COMMENTED", result.State)
	}
}

func TestFindPublishedReviewReportsAbsence(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octo/hello/pulls/42/reviews", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "body": "an unrelated human review"}})
	})
	p := newProviderForTest(t, f)

	result, found, err := p.FindPublishedReview(ctx(), ports.SCMPRRef{Number: 42, Repo: ports.SCMRepo{Owner: "octo", Name: "hello"}}, "<!-- ao-review-run:run-1 -->")
	if err != nil || found || result != (ports.SCMReviewPublishResult{}) {
		t.Fatalf("found = %v, result = %+v, err = %v, want clean not-found", found, result, err)
	}
}

func TestFindPublishedReviewTreatsServerErrorsAsUnknown(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octo/hello/pulls/42/reviews", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	p := newProviderForTest(t, f)

	if _, _, err := p.FindPublishedReview(ctx(), ports.SCMPRRef{Number: 42, Repo: ports.SCMRepo{Owner: "octo", Name: "hello"}}, "<!-- ao-review-run:run-1 -->"); !errors.Is(err, ports.ErrSCMPublishOutcomeUnknown) {
		t.Fatalf("err = %v, want ErrSCMPublishOutcomeUnknown", err)
	}
}
