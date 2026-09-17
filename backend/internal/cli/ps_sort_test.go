package cli

import "testing"

// Regression for the live finding: a snapshot whose RSS collapsed mid-flight
// (processes swapping out) must still come out heaviest-first.
func TestSortByRSS_HeaviestFirst(t *testing.T) {
	trees := []processTreeDTO{
		{SessionID: "a", RSSBytes: 363 * 1024 * 1024},
		{SessionID: "b", RSSBytes: 317 * 1024 * 1024},
		{SessionID: "c", RSSBytes: 31 * 1024 * 1024},
		{SessionID: "d", RSSBytes: 306 * 1024 * 1024},
		{SessionID: "e", RSSBytes: 305 * 1024 * 1024},
		{SessionID: "f", RSSBytes: 24 * 1024 * 1024},
	}
	sortByRSS(trees)
	for i := 1; i < len(trees); i++ {
		if trees[i-1].RSSBytes < trees[i].RSSBytes {
			t.Fatalf("order violation at %d: %d MB before %d MB", i, trees[i-1].RSSBytes/(1<<20), trees[i].RSSBytes/(1<<20))
		}
	}
	if trees[0].SessionID != "a" || trees[3].SessionID != "d" {
		t.Fatalf("unexpected head: %s, %s", trees[0].SessionID, trees[3].SessionID)
	}
}
