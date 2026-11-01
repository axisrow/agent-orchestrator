package sessionmanager

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A review-only session (the unified PR review entrypoint's foreign mode) is
// seeded with the enforced policy: auto review on, review auto-inject off so
// findings never loop back as fixes, and a non-bypass launch permission the
// tool deny list can bind in.
func TestSeedRecordReviewOnlyPolicy(t *testing.T) {
	projectConfig := domain.ProjectConfig{AutoReview: false}
	rec := seedRecord(ports.SpawnConfig{
		ProjectID:  "fork",
		Kind:       domain.KindWorker,
		ReviewOnly: true,
		AgentConfig: ports.AgentConfig{
			Permissions: domain.PermissionModeBypassPermissions,
		},
	}, projectConfig, time.Date(2026, time.October, 31, 9, 0, 0, 0, time.UTC))
	if !rec.ReviewOnly {
		t.Fatalf("ReviewOnly = false, want true")
	}
	if !rec.AutoReviewEnabled {
		t.Fatalf("AutoReviewEnabled = false, want true (the reviewer must start without a project-wide auto-review setting)")
	}
	if rec.AutoInjectReview {
		t.Fatalf("AutoInjectReview = true, want false (foreign reviews have no fix loop)")
	}
	if rec.Metadata.Permissions != ports.PermissionModeAuto {
		t.Fatalf("Metadata.Permissions = %q, want %q (deny rules are ignored under bypass)", rec.Metadata.Permissions, ports.PermissionModeAuto)
	}
}

// An ordinary spawn keeps the project-derived policy: no review-only fields.
func TestSeedRecordOrdinarySpawnUnaffected(t *testing.T) {
	rec := seedRecord(ports.SpawnConfig{ProjectID: "fork", Kind: domain.KindWorker}, domain.ProjectConfig{AutoReview: true}, time.Date(2026, time.October, 31, 9, 0, 0, 0, time.UTC))
	if rec.ReviewOnly {
		t.Fatalf("ReviewOnly = true, want false")
	}
	if !rec.AutoReviewEnabled {
		t.Fatalf("AutoReviewEnabled = false, want project setting true")
	}
	if !rec.AutoInjectReview {
		t.Fatalf("AutoInjectReview = false, want true")
	}
	if rec.Metadata.Permissions != ports.PermissionModeAuto {
		t.Fatalf("Metadata.Permissions = %q, want the ordinary spawn default %q", rec.Metadata.Permissions, ports.PermissionModeAuto)
	}
}

// The launch policy a review-only session runs with denies the file-mutation
// tools and git/gh write prefixes, and pins a non-bypass permission so the
// deny rules are actually enforced.
func TestReviewOnlyLaunchPolicyDeniesMutation(t *testing.T) {
	cfg := ports.LaunchConfig{Permissions: ports.PermissionModeBypassPermissions}
	pinReviewOnlyLaunchPolicy(&cfg)
	if cfg.Permissions != ports.PermissionModeAuto {
		t.Fatalf("Permissions = %q, want %q", cfg.Permissions, ports.PermissionModeAuto)
	}
	for _, tool := range []string{"Edit", "Write", "NotebookEdit", "Bash(git push:*)", "Bash(git commit:*)"} {
		found := false
		for _, denied := range cfg.DisallowedTools {
			if denied == tool {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("DisallowedTools %v misses %q", cfg.DisallowedTools, tool)
		}
	}
}

// Restores re-derive the policy: the denial survives daemon restarts because
// restoredAgentConfig reads the durable ReviewOnly flag.
func TestRestoredAgentConfigReviewOnlyClampsPermissions(t *testing.T) {
	rec := domain.SessionRecord{
		Harness:    domain.HarnessClaudeCode,
		Kind:       domain.KindWorker,
		ReviewOnly: true,
	}
	cfg := restoredAgentConfig(rec, domain.ProjectConfig{AgentConfig: domain.AgentConfig{Permissions: domain.PermissionModeBypassPermissions}})
	if cfg.Permissions != ports.PermissionModeAuto {
		t.Fatalf("Permissions = %q, want %q on restore", cfg.Permissions, ports.PermissionModeAuto)
	}
}

func TestPinReviewOnlyRestorePolicy(t *testing.T) {
	cfg := ports.RestoreConfig{Permissions: ports.PermissionModeBypassPermissions}
	pinReviewOnlyRestorePolicy(&cfg)
	if cfg.Permissions != ports.PermissionModeAuto {
		t.Fatalf("Permissions = %q, want %q", cfg.Permissions, ports.PermissionModeAuto)
	}
	if len(cfg.DisallowedTools) == 0 {
		t.Fatalf("DisallowedTools empty, want the mutation deny list")
	}
}

// End to end through spawn: a review-only session's real launch carries the
// enforced deny list and the durable row records the mode, so a foreign PR's
// worker cannot mutate files even though its harness would normally allow it.
func TestSpawnReviewOnlyLaunchCarriesDenyList(t *testing.T) {
	st := newFakeStore()
	st.projects["fork"] = domain.ProjectRecord{ID: "fork", Config: domain.ProjectConfig{
		Worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode},
	}}
	agent := &recordingAgent{}
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    singleAgent{agent: agent},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID:     "fork",
		Kind:          domain.KindWorker,
		ReviewOnly:    true,
		RequestedMode: domain.SessionModeTUI,
	})
	if err != nil {
		t.Fatalf("review-only spawn failed: %v", err)
	}
	if !rec.ReviewOnly {
		t.Fatalf("spawned record ReviewOnly = false, want true")
	}
	if agent.lastLaunch.Permissions != ports.PermissionModeAuto {
		t.Fatalf("launch Permissions = %q, want %q", agent.lastLaunch.Permissions, ports.PermissionModeAuto)
	}
	for _, tool := range []string{"Edit", "Write", "NotebookEdit", "Bash(git push:*)"} {
		found := false
		for _, denied := range agent.lastLaunch.DisallowedTools {
			if denied == tool {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("launch DisallowedTools %v misses %q", agent.lastLaunch.DisallowedTools, tool)
		}
	}
	stored, ok, err := st.GetSession(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("stored session lookup failed: ok=%v err=%v", ok, err)
	}
	if !stored.ReviewOnly || !stored.AutoReviewEnabled || stored.AutoInjectReview {
		t.Fatalf("stored review-only policy wrong: reviewOnly=%t autoReview=%t autoInject=%t",
			stored.ReviewOnly, stored.AutoReviewEnabled, stored.AutoInjectReview)
	}
}
