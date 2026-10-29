package envfilter

import "testing"

func TestIsParentSessionMarkerBlocksKnownMarkers(t *testing.T) {
	blocked := []string{
		"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_CHILD_SESSION",
		"CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_BRIDGE_SESSION_ID",
		"CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_MESSAGING_SOCKET",
		"CLAUDE_CODE_EXECPATH", "CLAUDE_PID", "CLAUDE_PLUGIN_DATA",
		"CLAUDE_EFFORT", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_SUBAGENT_MODEL",
	}
	for _, key := range blocked {
		if !IsParentSessionMarker(key) {
			t.Errorf("IsParentSessionMarker(%q) = false, want true", key)
		}
	}
}

func TestIsParentSessionMarkerKeepsAuthAndUserVars(t *testing.T) {
	// Auth credentials must never be blocked — workers need them to
	// authenticate, and claudecode.go reads CLAUDE_CODE_OAUTH_TOKEN directly.
	kept := []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
		// A plain user-set var sharing the CLAUDE_ prefix but not a known
		// parent-session marker: this is a denylist, not a blanket prefix block.
		"CLAUDE_CUSTOM_USER_VAR",
		"PATH", "HOME", "LANG",
	}
	for _, key := range kept {
		if IsParentSessionMarker(key) {
			t.Errorf("IsParentSessionMarker(%q) = true, want false", key)
		}
	}
}

func TestDropParentSessionMarkersFiltersEntries(t *testing.T) {
	in := []string{
		"PATH=/bin",
		"CLAUDE_CODE_CHILD_SESSION=1",
		"ANTHROPIC_API_KEY=sk-keep",
		"CLAUDECODE=1",
	}
	got := DropParentSessionMarkers(in)
	want := []string{"PATH=/bin", "ANTHROPIC_API_KEY=sk-keep"}
	if len(got) != len(want) {
		t.Fatalf("DropParentSessionMarkers(%v) = %v, want %v", in, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DropParentSessionMarkers(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestDropParentSessionMarkersPreservesMalformedEntries(t *testing.T) {
	// An entry with no "=" is malformed but must not be silently dropped —
	// that would be a behavior change unrelated to this filter's purpose.
	in := []string{"PATH=/bin", "MALFORMED_NO_EQUALS"}
	got := DropParentSessionMarkers(in)
	if len(got) != 2 || got[1] != "MALFORMED_NO_EQUALS" {
		t.Fatalf("DropParentSessionMarkers(%v) = %v, want malformed entry preserved", in, got)
	}
}

func TestIsGitIdentityKeyBlocksOnlyIdentityKeys(t *testing.T) {
	for _, key := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		if !IsGitIdentityKey(key) {
			t.Errorf("IsGitIdentityKey(%q) = false, want true", key)
		}
	}
	// AO's own GIT_* plumbing and unrelated keys must survive: only the four
	// commit-identity overrides are blocked.
	for _, key := range []string{"GIT_CONFIG_GLOBAL", "GIT_TERMINAL_PROMPT", "GIT_EDITOR", "GIT_AUTHOR", "PATH", "HOME"} {
		if IsGitIdentityKey(key) {
			t.Errorf("IsGitIdentityKey(%q) = true, want false", key)
		}
	}
}

// An empty cloud profile must leave nothing behind: the filter is a pure
// removal, and every other entry survives in order.
func TestStripGitIdentityRemovesOnlyIdentityKeys(t *testing.T) {
	in := []string{
		"GIT_AUTHOR_NAME=Cloud Profile",
		"PATH=/usr/bin:/bin",
		"GIT_AUTHOR_EMAIL=cloud@example.com",
		"GIT_CONFIG_GLOBAL=/tmp/session.gitconfig",
		"GIT_COMMITTER_NAME=Cloud Profile",
		"HOME=/Users/dev",
		"GIT_COMMITTER_EMAIL=cloud@example.com",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_EDITOR=true",
	}
	got := StripGitIdentity(in)
	want := []string{
		"PATH=/usr/bin:/bin",
		"GIT_CONFIG_GLOBAL=/tmp/session.gitconfig",
		"HOME=/Users/dev",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_EDITOR=true",
	}
	if len(got) != len(want) {
		t.Fatalf("StripGitIdentity(%v) = %v, want %v", in, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("StripGitIdentity(%v) = %v, want %v", in, got, want)
		}
	}
	// The input must be untouched: callers still own their slice.
	if in[0] != "GIT_AUTHOR_NAME=Cloud Profile" || len(in) != 9 {
		t.Fatalf("StripGitIdentity mutated its input: %v", in)
	}
}

func TestStripGitIdentityKeepsEmptyEnvironment(t *testing.T) {
	if got := StripGitIdentity(nil); got == nil || len(got) != 0 {
		t.Fatalf("StripGitIdentity(nil) = %v, want an empty slice", got)
	}
}
