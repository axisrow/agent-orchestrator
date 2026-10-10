// Package gateway reads, writes, and probes the Anthropic-compatible gateway
// configuration. Entries persist in AO's own storage — never in Claude
// settings files, which the user owns (a personal provider switcher may manage
// the same keys there) — and reach agent processes as launch env, so the UI
// and the launch path can never disagree.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// Scope selects which gateway entry a settings surface edits.
type Scope string

const (
	// ScopeApp is the app-wide entry every project inherits.
	ScopeApp Scope = "app"
	// ScopeProject is one project's overriding entry.
	ScopeProject Scope = "project"
)

// The three keys a gateway entry is made of, as the resolver reads them.
const (
	keyBaseURL = "ANTHROPIC_BASE_URL"
	// gosec: env key name, not a credential value.
	keyToken = "ANTHROPIC_AUTH_TOKEN" //nolint:gosec // env key name, not a credential value
	keyModel = "ANTHROPIC_MODEL"
)

// ScopeValue is one scope's stored gateway entry. The token is never
// returned, only whether one is stored.
type ScopeValue struct {
	BaseURL  string `json:"baseUrl,omitempty"`
	TokenSet bool   `json:"tokenSet"`
	Model    string `json:"model,omitempty"`
}

// Effective is what the resolution chain actually yields right now.
type Effective struct {
	BaseURL string `json:"baseUrl,omitempty"`
	Model   string `json:"model,omitempty"`
	// Source names the scope that wins under the resolution precedence:
	// "project", "app", or "" when no gateway is configured.
	Source string `json:"source,omitempty"`
}

// Config is the GET response: each scope's stored entry plus the effective
// resolution. Project is nil when no project was requested.
type Config struct {
	App       ScopeValue  `json:"app"`
	Project   *ScopeValue `json:"project,omitempty"`
	Effective Effective   `json:"effective"`
}

// SetInput is a PUT payload. Every key is tri-state: nil leaves the stored
// value untouched, a pointer to "" clears the key, a pointer to a value
// writes it. This is what makes a token-only rotation safe.
type SetInput struct {
	Scope     Scope
	ProjectID string
	BaseURL   *string
	Token     *string
	Model     *string
}

// ProbeResult mirrors the validator's verdict plus the gateway's own model
// list, so the settings screen can confirm the credential and offer models.
type ProbeResult struct {
	State  string  `json:"state"` // valid | invalid | unknown
	Detail string  `json:"detail,omitempty"`
	Models []Model `json:"models,omitempty"`
}

// Model is one model ID the gateway reported.
type Model struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
}

// ProjectLookup resolves a project ID to its working directory.
type ProjectLookup func(ctx context.Context, projectID string) (string, error)

// Entry is the stored form of one scope's gateway record.
type Entry struct {
	Scope     Scope
	ProjectID string
	BaseURL   string
	Token     string
	Model     string
}

// Store is the durable gateway-entry storage boundary.
type Store interface {
	GetGatewayEntry(ctx context.Context, scope Scope, projectID string) (Entry, bool, error)
	UpsertGatewayEntry(ctx context.Context, entry Entry, now time.Time) error
	ListGatewayEntries(ctx context.Context) ([]Entry, error)
}

// LegacySource names one pre-existing Claude settings file whose gateway keys
// should be imported once. AO reads the file and never writes it back.
type LegacySource struct {
	Scope     Scope
	ProjectID string
	Path      string
}

// Service implements the settings-screen surface on top of AO's storage and
// the shared credential validator.
type Service struct {
	store    Store
	projects ProjectLookup
}

// New builds a Service.
func New(store Store, projects ProjectLookup) *Service {
	return &Service{store: store, projects: projects}
}

// Get reports the stored entry per scope and the effective resolution.
func (s *Service) Get(ctx context.Context, projectID string) (Config, error) {
	cfg := Config{}
	app, _, err := s.store.GetGatewayEntry(ctx, ScopeApp, "")
	if err != nil {
		return Config{}, err
	}
	cfg.App = scopeValue(app)
	if strings.TrimSpace(projectID) == "" {
		cfg.Effective = effective(cfg.App, ScopeValue{})
		return cfg, nil
	}
	project, _, err := s.store.GetGatewayEntry(ctx, ScopeProject, projectID)
	if err != nil {
		return Config{}, err
	}
	projectValue := scopeValue(project)
	cfg.Project = &projectValue
	cfg.Effective = effective(cfg.App, projectValue)
	return cfg, nil
}

// effective folds two scope entries the way the launch path does: project
// values win, and an entry wins only if it configures anything at all.
func effective(app, project ScopeValue) Effective {
	for _, entry := range []struct {
		scope  string
		values ScopeValue
	}{{"project", project}, {"app", app}} {
		if entry.values.BaseURL != "" || entry.values.TokenSet || entry.values.Model != "" {
			return Effective{BaseURL: entry.values.BaseURL, Model: entry.values.Model, Source: entry.scope}
		}
	}
	return Effective{}
}

func scopeValue(entry Entry) ScopeValue {
	return ScopeValue{BaseURL: entry.BaseURL, TokenSet: entry.Token != "", Model: entry.Model}
}

// Set writes the entry into AO storage and returns the refreshed config for
// the same scope.
func (s *Service) Set(ctx context.Context, in SetInput) (Config, error) {
	if in.Scope != ScopeApp && in.Scope != ScopeProject {
		return Config{}, fmt.Errorf("gateway: unknown scope %q", in.Scope)
	}
	if in.Scope == ScopeProject && strings.TrimSpace(in.ProjectID) == "" {
		return Config{}, fmt.Errorf("gateway: projectId is required for the project scope")
	}
	// The app scope is keyed by an empty project id: normalize a stray
	// projectId away so the PUT reads and writes the (app, "") row instead of
	// storing an entry Get can never return.
	if in.Scope == ScopeApp {
		in.ProjectID = ""
	}
	if in.BaseURL != nil && strings.TrimSpace(*in.BaseURL) != "" {
		parsed, err := url.Parse(strings.TrimSpace(*in.BaseURL))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return Config{}, fmt.Errorf("gateway: base URL must be an http(s) URL")
		}
	}
	entry, _, err := s.store.GetGatewayEntry(ctx, in.Scope, in.ProjectID)
	if err != nil {
		return Config{}, err
	}
	// Tri-state per key: nil keeps the stored value, a pointer clears or sets.
	apply := func(dst *string, update *string) {
		if update != nil {
			*dst = strings.TrimSpace(*update)
		}
	}
	apply(&entry.BaseURL, in.BaseURL)
	apply(&entry.Token, in.Token)
	apply(&entry.Model, in.Model)
	entry.Scope = in.Scope
	entry.ProjectID = in.ProjectID
	if err := s.store.UpsertGatewayEntry(ctx, entry, time.Now().UTC()); err != nil {
		return Config{}, err
	}
	return s.Get(ctx, in.ProjectID)
}

// ImportLegacy seeds storage from pre-existing Claude settings files exactly
// once: when storage already holds an entry, it is a no-op — the files stay
// owned by the user and are never written back. Returns the imported count.
func (s *Service) ImportLegacy(ctx context.Context, sources []LegacySource) (int, error) {
	existing, err := s.store.ListGatewayEntries(ctx)
	if err != nil {
		return 0, err
	}
	if len(existing) > 0 {
		return 0, nil
	}
	imported := 0
	for _, source := range sources {
		env, ok := readRawEnv(source.Path)
		if !ok {
			continue
		}
		entry := Entry{
			Scope:     source.Scope,
			ProjectID: source.ProjectID,
			BaseURL:   env[keyBaseURL],
			Token:     env[keyToken],
			Model:     env[keyModel],
		}
		if entry.BaseURL == "" && entry.Token == "" && entry.Model == "" {
			continue
		}
		if err := s.store.UpsertGatewayEntry(ctx, entry, time.Now().UTC()); err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}

// Probe validates a base URL + token the way the resolver will use them,
// before anything is saved.
func (s *Service) Probe(ctx context.Context, baseURL, token string) (ProbeResult, error) {
	baseURL = strings.TrimSpace(baseURL)
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return ProbeResult{}, fmt.Errorf("gateway: base URL and token are required to probe")
	}
	if parsed, err := url.Parse(baseURL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ProbeResult{}, fmt.Errorf("gateway: base URL must be an http(s) URL")
	}
	result := agentcreds.New(nil).Validate(ctx, agentcreds.Credential{
		Kind:     agentcreds.KindAuthToken,
		Source:   keyToken,
		Provider: agentcreds.ProviderGateway,
		Secret:   token,
		BaseURL:  baseURL,
	})
	models := make([]Model, 0, len(result.Models))
	for _, model := range result.Models {
		models = append(models, Model{ID: model.ID, DisplayName: model.DisplayName})
	}
	return ProbeResult{State: string(result.State), Detail: result.Detail, Models: models}, nil
}

// readRawEnv reads just the gateway keys from one legacy settings file. A
// missing, malformed, or keyless file is an empty entry, not an error.
func readRawEnv(path string) (map[string]string, bool) {
	raw, err := os.ReadFile(path) //nolint:gosec // documented Claude settings location
	if err != nil {
		return nil, false
	}
	var payload struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil, false
	}
	env := make(map[string]string, len(payload.Env))
	for _, key := range []string{keyBaseURL, keyToken, keyModel} {
		env[key] = strings.TrimSpace(payload.Env[key])
	}
	return env, true
}
