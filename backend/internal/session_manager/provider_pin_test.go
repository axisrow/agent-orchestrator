package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestRoleProviderPinMapsSessionKind(t *testing.T) {
	cfg := domain.ProjectConfig{
		Worker:       domain.RoleOverride{Provider: "https://worker.example"},
		Orchestrator: domain.RoleOverride{Provider: domain.ProviderDirect},
	}
	if got := RoleProviderPin(domain.KindWorker, cfg); got != "https://worker.example" {
		t.Fatalf("worker pin = %q", got)
	}
	if got := RoleProviderPin(domain.KindOrchestrator, cfg); got != domain.ProviderDirect {
		t.Fatalf("orchestrator pin = %q", got)
	}
	if got := RoleProviderPin(domain.KindOrchestrator, domain.ProjectConfig{}); got != "" {
		t.Fatalf("pinless role = %q, want empty", got)
	}
}

func TestApplyRoleProviderPinOverlaysLaunchEnv(t *testing.T) {
	env := map[string]string{"ANTHROPIC_BASE_URL": "https://settings.example"}
	applyRoleProviderPin(context.Background(), env, "/proj", domain.KindWorker, domain.ProjectConfig{
		Worker: domain.RoleOverride{Provider: domain.ProviderDirect},
	})
	if env["ANTHROPIC_BASE_URL"] != "" {
		t.Fatalf("direct pin should shadow the base URL, got %q", env["ANTHROPIC_BASE_URL"])
	}

	// A pinless role must not touch the env.
	env = map[string]string{"ANTHROPIC_BASE_URL": "https://settings.example"}
	applyRoleProviderPin(context.Background(), env, "/proj", domain.KindWorker, domain.ProjectConfig{})
	if env["ANTHROPIC_BASE_URL"] != "https://settings.example" {
		t.Fatalf("pinless role changed the env: %v", env)
	}
}
