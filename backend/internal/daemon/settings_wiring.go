package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/gateway"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	settingssvc "github.com/aoagents/agent-orchestrator/backend/internal/service/settings"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// settingsStore adapts the SQLite store to the settings service's Store.
//
// The two define their own snapshot types so neither depends on the other's; this
// is the one place that knows both, keeping the translation in the wiring.
type settingsStore struct{ store *sqlite.Store }

var _ settingssvc.Store = settingsStore{}

func (s settingsStore) GetAppSettings(ctx context.Context) (settingssvc.Snapshot, error) {
	row, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return settingssvc.Snapshot{}, err
	}
	return settingssvc.Snapshot{
		DefaultSessionMode: row.DefaultSessionMode,
		CloudOffering:      row.CloudOffering,
		UpdatedAt:          row.UpdatedAt,
	}, nil
}

func (s settingsStore) SetDefaultSessionMode(
	ctx context.Context,
	mode domain.SessionMode,
	now time.Time,
) error {
	return s.store.SetDefaultSessionMode(ctx, mode, now)
}

func (s settingsStore) SetCloudOffering(ctx context.Context, enabled bool, now time.Time) error {
	return s.store.SetCloudOffering(ctx, enabled, now)
}

// newGatewayService builds the gateway service on AO storage and seeds it from
// pre-existing Claude settings files exactly once; those files are read and
// never written back.
func newGatewayService(ctx context.Context, store *sqlite.Store, mgr projectsvc.Manager) *gateway.Service {
	svc := gateway.New(gatewayStore{store: store}, projectPathLookup(mgr))
	if _, err := svc.ImportLegacy(ctx, legacyGatewaySources(ctx, mgr)); err != nil {
		// A failed import leaves storage empty; the settings screen can seed it
		// by hand. Startup never fails over it.
		slog.Warn("gateway: legacy settings import skipped", "error", err)
	}
	return svc
}

// projectPathLookup adapts the project manager to the gateway service's
// project-ID-to-path lookup, keeping neither package importing the other.
func projectPathLookup(mgr projectsvc.Manager) gateway.ProjectLookup {
	return func(ctx context.Context, projectID string) (string, error) {
		result, err := mgr.Get(ctx, domain.ProjectID(projectID))
		if err != nil {
			return "", err
		}
		if result.Project == nil {
			return "", fmt.Errorf("gateway: project %s not found", projectID)
		}
		return result.Project.Path, nil
	}
}

// gatewayStore adapts the SQLite store to the gateway service's Store.
type gatewayStore struct{ store *sqlite.Store }

var _ gateway.Store = gatewayStore{}

func (s gatewayStore) GetGatewayEntry(ctx context.Context, scope gateway.Scope, projectID string) (gateway.Entry, bool, error) {
	return s.store.GetGatewayEntry(ctx, scope, projectID)
}

func (s gatewayStore) UpsertGatewayEntry(ctx context.Context, entry gateway.Entry, now time.Time) error {
	return s.store.UpsertGatewayEntry(ctx, entry, now)
}

func (s gatewayStore) ListGatewayEntries(ctx context.Context) ([]gateway.Entry, error) {
	return s.store.ListGatewayEntries(ctx)
}

// providerEntryLookup adapts the SQLite store to the gateway-entry list every
// launch, relaunch, reviewer, and model-discovery path resolves its provider
// pin — or default entry — against.
func providerEntryLookup(store *sqlite.Store) func(ctx context.Context) []agentcreds.GatewayEntry {
	return func(ctx context.Context) []agentcreds.GatewayEntry {
		entries, err := store.ListGatewayEntries(ctx)
		if err != nil {
			return nil
		}
		out := make([]agentcreds.GatewayEntry, 0, len(entries))
		for _, entry := range entries {
			out = append(out, agentcreds.GatewayEntry{
				Scope:     string(entry.Scope),
				ProjectID: entry.ProjectID,
				BaseURL:   entry.BaseURL,
				Token:     entry.Token,
				Model:     entry.Model,
			})
		}
		return out
	}
}

// legacyGatewaySources names the pre-existing Claude settings files whose
// gateway keys seed AO storage once. AO reads them and never writes back.
func legacyGatewaySources(ctx context.Context, mgr projectsvc.Manager) []gateway.LegacySource {
	sources := []gateway.LegacySource{{Scope: gateway.ScopeApp, Path: filepath.Join(claudeSettingsDir(), "settings.json")}}
	projects, err := mgr.List(ctx)
	if err != nil {
		return sources
	}
	for _, project := range projects {
		if project.Path == "" {
			continue
		}
		sources = append(sources, gateway.LegacySource{
			Scope:     gateway.ScopeProject,
			ProjectID: string(project.ID),
			Path:      filepath.Join(project.Path, ".claude", "settings.json"),
		})
	}
	return sources
}

// claudeSettingsDir resolves ~/.claude, honoring CLAUDE_CONFIG_DIR.
func claudeSettingsDir() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			return abs
		}
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}
