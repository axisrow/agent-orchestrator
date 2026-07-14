package daemon

import (
	"io"
	"log/slog"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestTrackerForSession_NilInterfaceWhenNoToken is a regression test for issue #2685:
// `ao spawn --issue` panicked because the session service received a typed-nil
// *github.Tracker inside a non-nil ports.Tracker interface, which slipped past the
// `s.tracker == nil` guard in withIssueContext and dereferenced nil on Get.
//
// When no GitHub token is available, the tracker handed to the session service must be
// a true nil interface (so the guard fires), never a typed-nil concrete value.
func TestTrackerForSession_NilInterfaceWhenNoToken(t *testing.T) {
	t.Setenv("AO_GITHUB_TOKEN", "")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	tracker := trackerForSession(log)
	if tracker != nil {
		t.Fatalf("tracker = %T(%[1]v), want a true nil ports.Tracker interface when no token is configured", tracker)
	}

	// Guard against the typed-nil trap explicitly: a typed-nil concrete value is a non-nil
	// interface, which is exactly the regression. Mirror the existing nil-guard in
	// withIssueContext (issue_context.go) so this stays aligned with the call site.
	var _ ports.Tracker = tracker
	if tracker != nil {
		t.Fatal("tracker must be interface-nil so withIssueContext's `s.tracker == nil` guard fires")
	}
}
