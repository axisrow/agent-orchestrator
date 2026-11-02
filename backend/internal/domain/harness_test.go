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

func TestFXHarnessIsKnown(t *testing.T) {
	if HarnessFX != AgentHarness("fx") {
		t.Fatalf("HarnessFX = %q, want fx", HarnessFX)
	}
	if !HarnessFX.IsKnown() {
		t.Fatal("HarnessFX.IsKnown() = false, want true")
	}
	for _, harness := range AllHarnesses {
		if harness == HarnessFX {
			return
		}
	}
	t.Fatal("AllHarnesses does not contain HarnessFX")
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
