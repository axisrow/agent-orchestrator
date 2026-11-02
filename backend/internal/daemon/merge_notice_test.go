package daemon

import "testing"

func TestPRMergeNoticesEnabled(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false},
		{"1", true},
		{"0", false},
		{"true", false},
		{"yes", false},
	} {
		if got := prMergeNoticesEnabled(func(string) string { return tc.value }); got != tc.want {
			t.Fatalf("AO_ENABLE_PR_MERGE_NOTICES=%q: enabled = %v, want %v", tc.value, got, tc.want)
		}
	}
}
