package cli

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Ownership detection of the unified PR review entrypoint: canonical repo
// match → own, everything unmatched → foreign-safe default, and the
// configurable own accounts widen "own" by PR author or repository owner.
func TestDetectReviewMode(t *testing.T) {
	const fork = "https://github.com/axisrow/agent-orchestrator"
	const upstream = "https://github.com/OrchestratorInc/agent-orchestrator"
	const forkPR = fork + "/pull/191"
	const upstreamPR = upstream + "/pull/7496"

	cases := []struct {
		name        string
		override    string
		canonical   string
		origin      string
		ownAccounts []string
		prRepo      string
		prAuthor    string
		want        string
	}{
		{name: "canonical match is own", canonical: fork, origin: upstream, prRepo: forkPR, want: reviewModeOwn},
		{name: "origin fallback match is own", canonical: "", origin: fork, prRepo: forkPR, want: reviewModeOwn},
		{name: "repo mismatch defaults foreign-safe", canonical: fork, origin: fork, prRepo: upstreamPR, want: reviewModeForeign},
		{name: "case-insensitive repo match", canonical: "https://github.com/AxisRow/Agent-Orchestrator", prRepo: forkPR, want: reviewModeOwn},
		{name: "unparsable PR repo is foreign-safe", canonical: fork, prRepo: "not a url", want: reviewModeForeign},
		{name: "empty canonical and origin is foreign-safe", prRepo: forkPR, want: reviewModeForeign},
		// Configurable own accounts.
		{name: "author in ownAccounts is own despite repo mismatch", canonical: fork, ownAccounts: []string{"octocat"}, prRepo: upstreamPR, prAuthor: "octocat", want: reviewModeOwn},
		{name: "author match ignores case and @ prefix", canonical: fork, ownAccounts: []string{"@OctoCat"}, prRepo: upstreamPR, prAuthor: "OCTOCAT", want: reviewModeOwn},
		{name: "repo owner in ownAccounts is own", canonical: fork, ownAccounts: []string{"orchestratorinc"}, prRepo: upstreamPR, want: reviewModeOwn},
		{name: "other account in ownAccounts stays foreign", canonical: fork, ownAccounts: []string{"someoneelse"}, prRepo: upstreamPR, prAuthor: "octocat", want: reviewModeForeign},
		{name: "empty ownAccounts keeps canonical-only rule", canonical: fork, ownAccounts: nil, prRepo: upstreamPR, prAuthor: "octocat", want: reviewModeForeign},
		// Manual override wins over detection in both directions.
		{name: "override foreign forces foreign for own repo", override: reviewModeForeign, canonical: fork, prRepo: forkPR, want: reviewModeForeign},
		{name: "override own forces own for foreign repo", override: reviewModeOwn, canonical: fork, prRepo: upstreamPR, want: reviewModeOwn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectReviewMode(tc.override, tc.canonical, tc.origin, tc.ownAccounts, tc.prRepo, tc.prAuthor)
			if got != tc.want {
				t.Fatalf("detectReviewMode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSameCLIRepo(t *testing.T) {
	if !sameCLIRepo("git@github.com:axisrow/agent-orchestrator.git", "https://github.com/axisrow/agent-orchestrator") {
		t.Fatalf("ssh and https forms of one repo should match")
	}
	if sameCLIRepo("https://github.com/axisrow/agent-orchestrator", "https://github.com/axisrow/other") {
		t.Fatalf("different repos must not match")
	}
	if sameCLIRepo("not a url", "https://github.com/axisrow/agent-orchestrator") {
		t.Fatalf("unparsable repo must not match")
	}
}

func TestFormatReviewSessionName(t *testing.T) {
	if got := formatReviewSessionName("Fix the thing", 191, reviewModeOwn); got != "review #191: Fix the thing" {
		t.Fatalf("own name = %q", got)
	}
	if got := formatReviewSessionName("Foreign change", 7, reviewModeForeign); got != "review-only #7: Foreign change" {
		t.Fatalf("foreign name = %q", got)
	}
	long := formatReviewSessionName(strings.Repeat("x", 200), 1, reviewModeOwn)
	if utf8.RuneCountInString(long) > maxDisplayNameLen {
		t.Fatalf("name length %d exceeds cap %d", utf8.RuneCountInString(long), maxDisplayNameLen)
	}
}

// The spawned worker's prompt states the mode so the session report carries it.
func TestReviewSessionPromptStatesMode(t *testing.T) {
	own := reviewSessionPrompt(reviewModeOwn, "https://github.com/axisrow/agent-orchestrator/pull/191", "https://github.com/axisrow/agent-orchestrator")
	if !strings.Contains(own, "review+fix (own)") {
		t.Fatalf("own prompt misses the mode label: %s", own)
	}
	if !strings.Contains(own, "claimed by this session") {
		t.Fatalf("own prompt misses the claim context: %s", own)
	}
	foreign := reviewSessionPrompt(reviewModeForeign, "https://github.com/OrchestratorInc/agent-orchestrator/pull/7496", "https://github.com/OrchestratorInc/agent-orchestrator")
	if !strings.Contains(foreign, "review-only (foreign)") {
		t.Fatalf("foreign prompt misses the mode label: %s", foreign)
	}
	if !strings.Contains(foreign, "Do not attempt to modify files") {
		t.Fatalf("foreign prompt misses the mutation warning: %s", foreign)
	}
}

// Normalization of own-account config values, shared by detection and the CLI
// set-config flag.
func TestNormalizeOwnAccount(t *testing.T) {
	if got := domain.NormalizeOwnAccount("  @OctoCat "); got != "octocat" {
		t.Fatalf("NormalizeOwnAccount = %q, want %q", got, "octocat")
	}
	if got := domain.NormalizeOwnAccount(""); got != "" {
		t.Fatalf("empty input should stay empty, got %q", got)
	}
}
