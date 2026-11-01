package cli

import "testing"

// TestAPIErrorString covers how the CLI renders the daemon's error envelope,
// including the requestId it now surfaces for log correlation.
func TestAPIErrorString(t *testing.T) {
	cases := []struct {
		name string
		in   apiError
		want string
	}{
		{"message only", apiError{Message: "boom"}, "boom"},
		{"message and code", apiError{Message: "boom", Code: "X"}, "boom (X)"},
		{"with request id", apiError{Message: "boom", Code: "X", RequestID: "req-1"}, "boom (X) [request req-1]"},
		{"message and request id", apiError{Message: "boom", RequestID: "req-1"}, "boom [request req-1]"},
		{"detail after code", apiError{Message: "boom", Code: "X", Details: map[string]any{"detail": "session orch-1 is an orchestrator"}}, "boom (X) details: detail=session orch-1 is an orchestrator"},
		{"details sorted on one line, request id last", apiError{Message: "boom", Code: "X", RequestID: "req-1", Details: map[string]any{"zeta": 1, "alpha": "a"}}, "boom (X) details: alpha=a, zeta=1 [request req-1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}
