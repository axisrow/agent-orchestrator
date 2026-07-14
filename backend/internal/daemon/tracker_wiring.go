package daemon

import (
	"errors"
	"log/slog"

	trackergithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/github"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func newGitHubTracker() (ports.Tracker, error) {
	return trackergithub.New(trackergithub.Options{Token: trackergithub.EnvTokenSource{EnvVars: []string{"AO_GITHUB_TOKEN"}}})
}

// trackerForSession resolves the GitHub tracker for the session service, returning a
// true nil ports.Tracker interface when setup fails (no token, etc.). A nil concrete
// value wrapped in a non-nil interface would slip past the `s.tracker == nil` guard in
// service/session/issue_context.go and panic on first use — issue #2685 — so the failure
// path must keep tracker as an interface-nil, not a typed-nil.
func trackerForSession(logger *slog.Logger) ports.Tracker {
	t, err := newGitHubTracker()
	if err != nil {
		logTrackerDisabled(logger, err)
		return nil
	}
	return t
}

func logTrackerDisabled(logger *slog.Logger, err error) {
	if errors.Is(err, trackergithub.ErrNoToken) {
		logger.Warn("tracker issue prompt enrichment disabled: no usable GitHub token", "err", err)
	} else {
		logger.Warn("tracker issue prompt enrichment disabled: GitHub tracker setup failed", "err", err)
	}
}
