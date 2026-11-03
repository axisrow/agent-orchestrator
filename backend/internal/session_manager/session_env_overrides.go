package sessionmanager

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// sessionEnvStore persists per-session env overrides; optional so manager tests
// with narrower store fakes keep compiling.
type sessionEnvStore interface {
	SessionEnvOverrides(ctx context.Context, id domain.SessionID) (map[string]string, error)
	SetSessionEnvOverrides(ctx context.Context, id domain.SessionID, env map[string]string) error
}

// applySessionEnvOverrides overlays the session's persisted env overrides onto
// a fully assembled launch env, so the most explicit layer (one named session)
// wins over project env, role env, and the provider pin/gateway env that
// applyRoleProviderPin laid down just before. AO-internal protected vars and
// the hook PATH pin are set elsewhere and keep winning. A failed read launches
// without the overrides rather than blocking the launch.
func (m *Manager) applySessionEnvOverrides(ctx context.Context, id domain.SessionID, env map[string]string) {
	if env == nil {
		return
	}
	store, ok := m.store.(sessionEnvStore)
	if !ok {
		return
	}
	overrides, err := store.SessionEnvOverrides(ctx, id)
	if err != nil {
		m.logger.Warn("read session env overrides", "session", id, "error", err)
		return
	}
	for key, value := range overrides {
		env[key] = value
	}
}

// SetSessionEnvOverrides persists (an empty map clears) the session's env
// overrides wholesale, mirroring the project set-config --env replace
// semantics. Values are secrets: they live in the sessions row only, are read
// solely to assemble launch env, and are never served through the session API.
func (m *Manager) SetSessionEnvOverrides(ctx context.Context, id domain.SessionID, env map[string]string) error {
	if err := domain.ValidateEnv(env); err != nil {
		return err
	}
	store, ok := m.store.(sessionEnvStore)
	if !ok {
		return fmt.Errorf("session env overrides are not supported by this store")
	}
	return store.SetSessionEnvOverrides(ctx, id, env)
}
