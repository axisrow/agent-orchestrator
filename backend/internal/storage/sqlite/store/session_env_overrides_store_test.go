package store_test

import (
	"context"
	"testing"
)

// Session env overrides round-trip through the dedicated column. Values are
// secrets: nothing in the served session record reads this column, so only
// these queries ever touch it.
func TestSessionEnvOverridesRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}

	env, err := s.SessionEnvOverrides(ctx, rec.ID)
	if err != nil || len(env) != 0 {
		t.Fatalf("fresh session env = %#v err=%v, want empty", env, err)
	}

	want := map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:11434", "ANTHROPIC_AUTH_TOKEN": "sk-local"}
	if err := s.SetSessionEnvOverrides(ctx, rec.ID, want); err != nil {
		t.Fatal(err)
	}
	env, err = s.SessionEnvOverrides(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 2 || env["ANTHROPIC_BASE_URL"] != want["ANTHROPIC_BASE_URL"] || env["ANTHROPIC_AUTH_TOKEN"] != want["ANTHROPIC_AUTH_TOKEN"] {
		t.Fatalf("round-trip env = %#v", env)
	}

	if err := s.SetSessionEnvOverrides(ctx, rec.ID, nil); err != nil {
		t.Fatal(err)
	}
	env, err = s.SessionEnvOverrides(ctx, rec.ID)
	if err != nil || len(env) != 0 {
		t.Fatalf("cleared env = %#v err=%v, want empty", env, err)
	}
}
