package domain

// AgentHarness identifies which agent CLI/runtime a session drives.
type AgentHarness string

// Supported agent harnesses.
const (
	HarnessClaudeCode AgentHarness = "claude-code"
	HarnessCodex      AgentHarness = "codex"
	HarnessOpenCodeV2 AgentHarness = "opencode-v2"
	HarnessAgy        AgentHarness = "agy"
	HarnessPi         AgentHarness = "pi"
	HarnessOMP        AgentHarness = "omp"
	// HarnessFake is retained for existing test fixtures and historical session
	// rows, but is not user-selectable.
	HarnessFake AgentHarness = "fake"
)

// AllHarnesses lists every supported harness. It is the canonical set used to
// validate user-supplied harness names (e.g. per-project role overrides).
var AllHarnesses = []AgentHarness{
	HarnessClaudeCode, HarnessCodex, HarnessOpenCodeV2,
	HarnessAgy, HarnessPi, HarnessOMP,
}

// IsKnown reports whether h is one of the supported harnesses.
func (h AgentHarness) IsKnown() bool {
	for _, k := range AllHarnesses {
		if h == k {
			return true
		}
	}
	return false
}
