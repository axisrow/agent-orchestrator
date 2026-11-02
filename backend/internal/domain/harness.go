package domain

// AgentHarness identifies which agent CLI/runtime a session drives.
type AgentHarness string

// Supported agent harnesses.
const (
	HarnessClaudeCode AgentHarness = "claude-code"
	HarnessCodex      AgentHarness = "codex"
	HarnessOpenCodeV2 AgentHarness = "opencode-v2"
	HarnessDroid      AgentHarness = "droid"
	HarnessAmp        AgentHarness = "amp"
	HarnessAgy        AgentHarness = "agy"
	HarnessQwen       AgentHarness = "qwen"
	HarnessGoose      AgentHarness = "goose"
	HarnessKimi       AgentHarness = "kimi"
	HarnessPi         AgentHarness = "pi"
	HarnessAutohand   AgentHarness = "autohand"
	HarnessOMP        AgentHarness = "omp"
	HarnessFX         AgentHarness = "fx"
	HarnessMemcode    AgentHarness = "memcode"
	// HarnessFake is retained for existing test fixtures and historical session
	// rows, but is not user-selectable.
	HarnessFake AgentHarness = "fake"
)

// AllHarnesses lists every supported harness. It is the canonical set used to
// validate user-supplied harness names (e.g. per-project role overrides).
var AllHarnesses = []AgentHarness{
	HarnessClaudeCode, HarnessCodex, HarnessOpenCodeV2,
	HarnessDroid, HarnessAmp, HarnessAgy, HarnessQwen, HarnessGoose, HarnessKimi,
	HarnessPi, HarnessAutohand, HarnessOMP, HarnessFX, HarnessMemcode,
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
