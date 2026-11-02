package activitydispatch

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Every deriver key must be a known harness name except fake, whose deriver is
// retained for test fixtures and historical callbacks even though the harness is
// no longer user-selectable. SupportsHarness equates tokens and harnesses, so any
// other drift would silently report a hooked harness as hook-less.
func TestDeriverTokensAreKnownHarnesses(t *testing.T) {
	for token := range Derivers {
		if token == string(domain.HarnessFake) {
			continue
		}
		if !domain.AgentHarness(token).IsKnown() {
			t.Errorf("deriver token %q is not a known AgentHarness", token)
		}
	}
}

func TestSupportsHarness(t *testing.T) {
	for _, h := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode, domain.HarnessOpenCodeV2, domain.HarnessPi, domain.HarnessOMP, domain.HarnessAgy} {
		if !SupportsHarness(h) {
			t.Errorf("SupportsHarness(%q) = false, want true", h)
		}
	}
	// Harnesses with no callback pipeline — including every cut id — must read
	// as unsupported.
	for _, h := range []domain.AgentHarness{
		"kimi", "goose", "memcode", "fx", "qwen", "amp", "autohand", "droid",
		domain.AgentHarness(""),
	} {
		if SupportsHarness(domain.AgentHarness(h)) {
			t.Errorf("SupportsHarness(%q) = true, want false", h)
		}
	}
}

func TestOpenCodeV2DispatchesManagedPluginActivity(t *testing.T) {
	got, ok := Derive("opencode-v2", "permission-blocked", []byte(`{"session_id":"ses_v2"}`))
	if !ok || got != domain.ActivityBlocked {
		t.Fatalf("Derive(opencode-v2, permission-blocked) = (%q, %v), want (%q, true)", got, ok, domain.ActivityBlocked)
	}
}

func TestOMPDispatchesManagedExtensionActivity(t *testing.T) {
	got, ok := Derive("omp", "permission-request", []byte(`{"tool_name":"bash"}`))
	if !ok || got != domain.ActivityWaitingInput {
		t.Fatalf("Derive(omp, permission-request) = (%q, %v), want (%q, true)", got, ok, domain.ActivityWaitingInput)
	}
}

func TestPiDispatchesActivity(t *testing.T) {
	got, ok := Derive("pi", "user-prompt-submit", []byte(`{}`))
	if !ok || got != domain.ActivityActive {
		t.Fatalf("Derive(pi, user-prompt-submit) = (%q, %v), want (%q, true)", got, ok, domain.ActivityActive)
	}
}

func TestSignalCoverageForHarness(t *testing.T) {
	tests := []struct {
		harness domain.AgentHarness
		want    SignalCoverage
	}{
		{domain.HarnessClaudeCode, SignalCoverageComplete},
		{"fx", SignalCoverageNone},
	}

	for _, tt := range tests {
		t.Run(string(tt.harness), func(t *testing.T) {
			if got := CoverageForHarness(tt.harness); got != tt.want {
				t.Fatalf("CoverageForHarness(%q) = %v, want %v", tt.harness, got, tt.want)
			}
		})
	}
}

func TestFullySupportsHarnessRequiresCompleteCoverage(t *testing.T) {
	if !FullySupportsHarness(domain.HarnessClaudeCode) {
		t.Fatal("FullySupportsHarness(claude-code) = false, want true")
	}
	if FullySupportsHarness(domain.AgentHarness("memcode")) {
		t.Fatal("FullySupportsHarness(memcode) = true, want false for a cut harness")
	}
}
