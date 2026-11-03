package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// SessionEnvOverrides returns the env overrides saved for a session. Values are
// secrets: this read is for launch-time env assembly only, never for serving.
func (s *Store) SessionEnvOverrides(ctx context.Context, id domain.SessionID) (map[string]string, error) {
	raw, err := s.qr.GetSessionEnvOverrides(ctx, id)
	if err != nil || raw == "" {
		return nil, err
	}
	var env map[string]string
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, fmt.Errorf("decode env overrides for %s: %w", id, err)
	}
	if env == nil {
		env = map[string]string{}
	}
	return env, nil
}

// SetSessionEnvOverrides saves (or, with an empty map, clears) the session's
// env overrides wholesale.
func (s *Store) SetSessionEnvOverrides(ctx context.Context, id domain.SessionID, env map[string]string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.SetSessionEnvOverrides(ctx, gen.SetSessionEnvOverridesParams{EnvOverrides: encodeEnvOverrides(env), ID: id})
}

func encodeEnvOverrides(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(env) // plain string map: cannot fail
	return string(encoded)
}
