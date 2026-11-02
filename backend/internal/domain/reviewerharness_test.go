package domain

import "testing"

func TestOpenCodeV2ReviewerHarnessIsKnown(t *testing.T) {
	if ReviewerOpenCodeV2 != ReviewerHarness("opencode-v2") {
		t.Fatalf("ReviewerOpenCodeV2 = %q, want opencode-v2", ReviewerOpenCodeV2)
	}
	if !ReviewerOpenCodeV2.IsKnown() {
		t.Fatal("ReviewerOpenCodeV2.IsKnown() = false, want true")
	}
}
