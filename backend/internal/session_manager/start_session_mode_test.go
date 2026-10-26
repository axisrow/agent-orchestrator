package sessionmanager

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The cloud control plane resolves a cloud session's interface with the same
// precedence, against the same contract (cloud/internal/domain): the shared
// cases carry upstream's precedence, while the fork retires the local Chat
// interface, so the daemon's own cases live in withDefault.local and
// withoutDefault.local.
func TestStartSessionModeMatchesTheSharedCases(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/session-start-interface.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		WithDefault struct {
			Local []struct {
				Kind      string `json:"kind"`
				Requested string `json:"requested"`
				Default   string `json:"default"`
				Want      string `json:"want"`
			} `json:"local"`
		} `json:"withDefault"`
		WithoutDefault struct {
			Local []struct {
				Kind      string `json:"kind"`
				Requested string `json:"requested"`
				Want      string `json:"want"`
			} `json:"local"`
		} `json:"withoutDefault"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.WithDefault.Local) == 0 {
		t.Fatal("no local cases with a default")
	}
	for _, tc := range fixture.WithDefault.Local {
		got := startSessionMode(domain.SessionKind(tc.Kind), domain.SessionMode(tc.Requested), domain.SessionMode(tc.Default))
		if string(got) != tc.Want {
			t.Errorf("startSessionMode(%s, requested %q, default %q) = %q, want %q", tc.Kind, tc.Requested, tc.Default, got, tc.Want)
		}
	}
	// An unset default is the empty string here; the fork retires the local Chat
	// interface, so an orchestrator with no explicit mode starts in tui (the
	// cloud's rule for a create without one differs).
	if len(fixture.WithoutDefault.Local) == 0 {
		t.Fatal("no local cases without a default")
	}
	for _, tc := range fixture.WithoutDefault.Local {
		got := startSessionMode(domain.SessionKind(tc.Kind), domain.SessionMode(tc.Requested), "")
		if string(got) != tc.Want {
			t.Errorf("startSessionMode(%s, requested %q, no default) = %q, want %q", tc.Kind, tc.Requested, got, tc.Want)
		}
	}
}
