package domain

import "testing"

func TestOpenCodeV2HarnessIsKnown(t *testing.T) {
	if HarnessOpenCodeV2 != AgentHarness("opencode-v2") {
		t.Fatalf("HarnessOpenCodeV2 = %q, want opencode-v2", HarnessOpenCodeV2)
	}
	if !HarnessOpenCodeV2.IsKnown() {
		t.Fatal("HarnessOpenCodeV2.IsKnown() = false, want true")
	}
}

// A cut harness is no longer known, so user-supplied names fail validation.
func TestCutHarnessIsUnknown(t *testing.T) {
	for _, cut := range []AgentHarness{"kimi", "goose", "memcode", "fx", "qwen", "amp", "autohand", "droid"} {
		if AgentHarness(cut).IsKnown() {
			t.Fatalf("harness %q IsKnown() = true, want false after the cut", cut)
		}
	}
}

func TestAgyHarnessIsKnown(t *testing.T) {
	if HarnessAgy != AgentHarness("agy") {
		t.Fatalf("HarnessAgy = %q, want agy", HarnessAgy)
	}
	if !HarnessAgy.IsKnown() {
		t.Fatal("HarnessAgy.IsKnown() = false, want true")
	}
	found := false
	for _, harness := range AllHarnesses {
		if harness == HarnessAgy {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("AllHarnesses does not contain HarnessAgy")
	}
}

func TestOMPHarnessIsKnown(t *testing.T) {
	if HarnessOMP != AgentHarness("omp") {
		t.Fatalf("HarnessOMP = %q, want omp", HarnessOMP)
	}
	if !HarnessOMP.IsKnown() {
		t.Fatal("HarnessOMP.IsKnown() = false, want true")
	}
	found := false
	for _, harness := range AllHarnesses {
		if harness == HarnessOMP {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("AllHarnesses does not contain HarnessOMP")
	}
}
