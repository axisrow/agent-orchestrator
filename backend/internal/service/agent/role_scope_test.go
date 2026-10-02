package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestRoleScopeParts(t *testing.T) {
	for _, tc := range []struct {
		scope     string
		projectID string
		role      string
		ok        bool
	}{
		{scope: "proj-1@role:worker", projectID: "proj-1", role: "worker", ok: true},
		{scope: "proj-1@role:reviewer", projectID: "proj-1", role: "reviewer", ok: true},
		{scope: "proj-1", ok: false},
		{scope: "proj-1@role:", ok: false},
		{scope: "@role:worker", ok: false},
		{scope: "@cred:openai_api_key", ok: false},
	} {
		projectID, role, ok := roleScopeParts(tc.scope)
		if ok != tc.ok || projectID != tc.projectID || role != tc.role {
			t.Errorf("roleScopeParts(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.scope, projectID, role, ok, tc.projectID, tc.role, tc.ok)
		}
	}
}

func TestModelCatalogScopePreservesRoleScope(t *testing.T) {
	projects := &fakeProjectLookup{records: map[string]domain.ProjectRecord{
		"proj-1": {ID: "proj-1", Path: "/work/project"},
	}}
	s := &Service{projects: projects}
	got, err := s.modelCatalogScope(context.Background(), "proj-1@role:worker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "proj-1@role:worker" {
		t.Fatalf("scope = %q, want the role scope preserved", got)
	}
}

func TestModelDiscoveryRequestFoldsRolePinIntoEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"env":{"ANTHROPIC_BASE_URL":"https://gw.example","ANTHROPIC_AUTH_TOKEN":"tok","ANTHROPIC_MODEL":"gateway-model"}}`
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	projects := &fakeProjectLookup{records: map[string]domain.ProjectRecord{
		"proj-1": {
			ID:   "proj-1",
			Path: "/work/project",
			Config: domain.ProjectConfig{
				Env:    map[string]string{"ANTHROPIC_MODEL": "project-model"},
				Worker: domain.RoleOverride{Provider: "https://gw.example"},
			},
		},
	}}
	s := &Service{projects: projects}

	request, err := s.modelDiscoveryRequest(context.Background(), "claude-code", "proj-1@role:worker", "/bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	if request.WorkingDir != "/work/project" {
		t.Fatalf("WorkingDir = %q, want the project path", request.WorkingDir)
	}
	if request.Env["ANTHROPIC_BASE_URL"] != "https://gw.example" || request.Env["ANTHROPIC_AUTH_TOKEN"] != "tok" || request.Env["ANTHROPIC_MODEL"] != "gateway-model" {
		t.Fatalf("pin env did not overlay the project env: %v", request.Env)
	}

	// A pinless role yields the plain project resolution.
	request, err = s.modelDiscoveryRequest(context.Background(), "claude-code", "proj-1@role:reviewer", "/bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	if request.Env["ANTHROPIC_MODEL"] != "project-model" || request.Env["ANTHROPIC_BASE_URL"] != "" {
		t.Fatalf("pinless role env = %v, want the project resolution", request.Env)
	}
}
