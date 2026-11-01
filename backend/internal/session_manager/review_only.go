package sessionmanager

import "github.com/aoagents/agent-orchestrator/backend/internal/ports"

// reviewOnlyDisallowedTools hard-denies the code-mutation paths for a
// review-only session (see ports.SpawnConfig.ReviewOnly). The deny list is the
// same shape the native reviewer adapters enforce for their own panes: Edit and
// Write calls fail outright, and the git/gh mutation prefixes cannot run even
// through Bash.
var reviewOnlyDisallowedTools = []string{
	"Edit",
	"Write",
	"NotebookEdit",
	"Bash(git push:*)",
	"Bash(git commit:*)",
	// Merging a foreign PR is not this session's job; denied so a future
	// broader allow rule can never admit it.
	"Bash(gh pr merge:*)",
}

// reviewOnlyPermissions returns the launch policy a review-only session runs
// with. Deny rules only bind in a non-bypass mode, and auto mode approves the
// read-only work unattended, so a review never stalls on a permission prompt.
func reviewOnlyPermissions() ports.PermissionMode {
	return ports.PermissionModeAuto
}

// pinReviewOnlyLaunchPolicy clamps a review-only session's launch config onto
// the enforced tool policy. Every launch and restore of a review-only session
// goes through one of these helpers, so the denial survives daemon restarts.
func pinReviewOnlyLaunchPolicy(cfg *ports.LaunchConfig) {
	if cfg == nil {
		return
	}
	cfg.Permissions = reviewOnlyPermissions()
	cfg.DisallowedTools = reviewOnlyDisallowedTools
}

func pinReviewOnlyRestorePolicy(cfg *ports.RestoreConfig) {
	if cfg == nil {
		return
	}
	cfg.Permissions = reviewOnlyPermissions()
	cfg.DisallowedTools = reviewOnlyDisallowedTools
}
