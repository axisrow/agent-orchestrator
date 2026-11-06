package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The terminate-on-merge lifecycle default (fork #285) is resolved once, at
// spawn: project default over the user-scope default over hardcoded false.
// The seed matrix pins every overlap level; the per-session toggle level is
// the existing SetTerminateOnPRMerge write path (service_test.go), which
// overwrites the seeded value afterwards.
func TestSeedRecordTerminateOnPRMergeDefault(t *testing.T) {
	tests := []struct {
		name       string
		project    domain.ProjectConfig
		user       domain.AgentConfig
		wantSeeded bool
	}{
		{"nothing set seeds false", domain.ProjectConfig{}, domain.AgentConfig{}, false},
		{"user default only", domain.ProjectConfig{}, domain.AgentConfig{TerminateOnPRMerge: true}, true},
		{"project default only", domain.ProjectConfig{TerminateOnPRMerge: true}, domain.AgentConfig{}, true},
		{"both set", domain.ProjectConfig{TerminateOnPRMerge: true}, domain.AgentConfig{TerminateOnPRMerge: true}, true},
	}
	for _, tt := range tests {
		rec := seedRecord(ports.SpawnConfig{ProjectID: "fork", Kind: domain.KindWorker}, tt.project, tt.user, time.Date(2026, time.November, 6, 9, 0, 0, 0, time.UTC))
		if rec.TerminateOnPRMerge != tt.wantSeeded {
			t.Errorf("%s: seeded TerminateOnPRMerge = %v, want %v", tt.name, rec.TerminateOnPRMerge, tt.wantSeeded)
		}
	}
}

type fakeUserConfig struct {
	cfg domain.AgentConfig
	err error
}

func (f fakeUserConfig) GetUserConfig(context.Context) (domain.AgentConfig, bool, error) {
	return f.cfg, f.err == nil, f.err
}

func TestUserConfigForSeed(t *testing.T) {
	ctx := context.Background()
	m := &Manager{}
	if got := m.userConfigForSeed(ctx); !got.IsZero() {
		t.Errorf("nil userConfig field = %+v, want zero config", got)
	}
	m = &Manager{userConfig: fakeUserConfig{err: errors.New("read failed")}}
	if got := m.userConfigForSeed(ctx); !got.IsZero() {
		t.Errorf("read error = %+v, want zero config (spawn must not fail)", got)
	}
	m = &Manager{userConfig: fakeUserConfig{cfg: domain.AgentConfig{TerminateOnPRMerge: true}}}
	if got := m.userConfigForSeed(ctx); !got.TerminateOnPRMerge {
		t.Errorf("read config = %+v, want the user-scope default", got)
	}
}
