// Package gateway reads, writes, and probes the Anthropic-compatible gateway
// configuration AO resolves from Claude settings files. It persists through
// the same files the resolver reads — the user's global Claude settings for
// the app scope and the project's .claude/settings.json for the project
// scope — so the UI and a hand-edited file can never disagree.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// Scope selects which settings file a gateway entry lives in.
type Scope string

const (
	// ScopeApp is the user's global Claude settings (~/.claude/settings.json).
	ScopeApp Scope = "app"
	// ScopeProject is <project>/.claude/settings.json.
	ScopeProject Scope = "project"
)

// The three keys a gateway entry is made of, as the resolver reads them.
const (
	keyBaseURL = "ANTHROPIC_BASE_URL"
	// gosec: settings-file env key name, not a credential value.
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

// Service implements the settings-screen surface on top of Claude settings
// files and the shared credential validator.
type Service struct {
	projects ProjectLookup
	// claudeDir overrides ~/.claude, for tests.
	claudeDir func() (string, error)
}

// New builds a Service.
func New(projects ProjectLookup) *Service {
	return &Service{projects: projects, claudeDir: defaultClaudeDir}
}

func defaultClaudeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("gateway: resolve home directory: %w", err)
	}
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Abs(dir)
	}
	return filepath.Join(home, ".claude"), nil
}

// Get reports the stored entry per scope and the effective resolution.
func (s *Service) Get(ctx context.Context, projectID string) (Config, error) {
	cfg := Config{}
	appPath, err := s.claudeDir()
	if err != nil {
		return Config{}, err
	}
	cfg.App = readScope(filepath.Join(appPath, "settings.json"))
	if strings.TrimSpace(projectID) == "" {
		cfg.Effective = effective(cfg.App, ScopeValue{})
		return cfg, nil
	}
	projectPath, err := s.projects(ctx, projectID)
	if err != nil {
		return Config{}, err
	}
	project := readScope(filepath.Join(projectPath, ".claude", "settings.json"))
	cfg.Project = &project
	cfg.Effective = effective(cfg.App, project)
	return cfg, nil
}

// effective folds two scope entries the way the resolver does: project
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

func readScope(path string) ScopeValue {
	env, ok := readEnv(path)
	if !ok {
		return ScopeValue{}
	}
	return ScopeValue{
		BaseURL:  env[keyBaseURL],
		TokenSet: env[keyToken] != "",
		Model:    env[keyModel],
	}
}

// Set writes the entry into the scope's settings file and returns the
// refreshed config for the same scope.
func (s *Service) Set(ctx context.Context, in SetInput) (Config, error) {
	if in.Scope != ScopeApp && in.Scope != ScopeProject {
		return Config{}, fmt.Errorf("gateway: unknown scope %q", in.Scope)
	}
	if in.BaseURL != nil && strings.TrimSpace(*in.BaseURL) != "" {
		parsed, err := url.Parse(strings.TrimSpace(*in.BaseURL))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return Config{}, fmt.Errorf("gateway: base URL must be an http(s) URL")
		}
	}
	updates := make(map[string]string, 3)
	for key, value := range map[string]*string{keyBaseURL: in.BaseURL, keyToken: in.Token, keyModel: in.Model} {
		if value != nil {
			updates[key] = strings.TrimSpace(*value)
		}
	}
	path, err := s.settingsPath(ctx, in)
	if err != nil {
		return Config{}, err
	}
	if err := writeEnvKeys(path, updates); err != nil {
		return Config{}, err
	}
	projectID := in.ProjectID
	if in.Scope == ScopeApp {
		projectID = ""
	}
	return s.Get(ctx, projectID)
}

func (s *Service) settingsPath(ctx context.Context, in SetInput) (string, error) {
	if in.Scope == ScopeApp {
		dir, err := s.claudeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "settings.json"), nil
	}
	if strings.TrimSpace(in.ProjectID) == "" {
		return "", fmt.Errorf("gateway: projectId is required for the project scope")
	}
	projectPath, err := s.projects(ctx, in.ProjectID)
	if err != nil {
		return "", err
	}
	return filepath.Join(projectPath, ".claude", "settings.json"), nil
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

// readEnv reads just the gateway keys from one settings file. A missing,
// malformed, or keyless file is an empty entry, not an error.
func readEnv(path string) (map[string]string, bool) {
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

// writeEnvKeys applies updates to a settings file's env object, preserving
// every other field Claude Code stores there. An empty value removes the key.
func writeEnvKeys(path string, updates map[string]string) error {
	document := make(map[string]json.RawMessage)
	if raw, err := os.ReadFile(path); err == nil { //nolint:gosec // documented Claude settings location
		if err := json.Unmarshal(raw, &document); err != nil {
			return fmt.Errorf("gateway: %s is not valid JSON: %w", path, err)
		}
	}
	env := make(map[string]json.RawMessage)
	if raw, ok := document["env"]; ok {
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("gateway: env in %s is not an object", path)
		}
	}
	for key, value := range updates {
		if value == "" {
			delete(env, key)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		env[key] = encoded
	}
	envEncoded, err := json.Marshal(env)
	if err != nil {
		return err
	}
	document["env"] = envEncoded
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	mode := os.FileMode(0o600) // the file may carry a token
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, encoded, mode)
}
