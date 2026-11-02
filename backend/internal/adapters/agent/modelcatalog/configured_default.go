package modelcatalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// configuredDefaultSource describes where an agent CLI keeps the model it runs
// when no --model flag is passed. Only user-level sources are read.
type configuredDefaultSource struct {
	// paths lists user-level config files, lowest precedence first: a later
	// file that sets a model overrides an earlier one.
	paths func(home string, env map[string]string) []string
	parse func(raw []byte) string
	// envOverride names a variable that wins over every file when set.
	envOverride string
	// resolve replaces paths/parse for agents whose layering cannot be
	// expressed as a flat file list. It returns "" when the effective model
	// cannot be determined.
	resolve func(home, workingDir string, env map[string]string) string
}

// configuredDefaultSources covers agents whose model-list command reports
// models but never marks which one the CLI will run. Without this, the picker
// has no default to show and the task form reads "Model not reported" even
// though the user has already chosen a model in the agent's own settings.
var configuredDefaultSources = map[string]configuredDefaultSource{
	"opencode-v2": {resolve: func(home, workingDir string, env map[string]string) string {
		return resolveOpenCodeModel(home, workingDir, env)
	}},
	"pi": {
		paths: func(home string, _ map[string]string) []string {
			if home == "" {
				return nil
			}
			return []string{filepath.Join(home, ".pi", "agent", "settings.json")}
		},
		parse: parsePiDefaultModel,
	},
}

// readConfiguredModel returns the model set in path, or fallback when the file
// is missing or sets none.
func readConfiguredModel(path string, parse func([]byte) string, fallback string) string {
	raw, err := readModelConfig(path)
	if err != nil {
		return fallback
	}
	if value := strings.TrimSpace(parse(raw)); value != "" {
		return value
	}
	return fallback
}

// configuredDefaultModel returns the model the agent's local configuration
// selects, or "" when the agent has no such source or nothing is configured.
func configuredDefaultModel(agentID, workingDir string, env map[string]string) string {
	source, ok := configuredDefaultSources[agentID]
	if !ok {
		return ""
	}
	if source.envOverride != "" {
		if value := envValue(env, source.envOverride); value != "" {
			return value
		}
	}
	home, _ := os.UserHomeDir()
	if source.resolve != nil {
		return strings.TrimSpace(source.resolve(home, workingDir, env))
	}
	configured := ""
	for _, path := range source.paths(home, env) {
		configured = readConfiguredModel(path, source.parse, configured)
	}
	return configured
}

// applyConfiguredDefault marks the configured model as the catalog default. A
// catalog that already reports a default is left alone: the CLI's own answer
// is more authoritative than AO's reading of its config files. A configured
// model missing from the list is appended, because it is what the CLI will
// actually run.
func applyConfiguredDefault(models []ports.AgentModelInfo, configured string) []ports.AgentModelInfo {
	configured = strings.TrimSpace(configured)
	if configured == "" || !isConcreteModel(configured) {
		return models
	}
	for _, item := range models {
		if item.IsDefault && isConcreteModel(item.ID) {
			return models
		}
	}
	for i := range models {
		if strings.EqualFold(models[i].ID, configured) {
			models[i].IsDefault = true
			return models
		}
	}
	return append(models, ports.AgentModelInfo{ID: configured, Label: configured, IsDefault: true})
}

// isConcreteModel mirrors the renderer's rule: "default" is a placeholder, not
// a model the picker can resolve to a name.
func isConcreteModel(id string) bool {
	id = strings.TrimSpace(id)
	return id != "" && !strings.EqualFold(id, "default")
}

func parseJSONCModelKey(raw []byte) string {
	var config struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(stripJSONC(raw), &config) != nil {
		return ""
	}
	return config.Model
}

func parsePiDefaultModel(raw []byte) string {
	var settings struct {
		DefaultProvider string `json:"defaultProvider"`
		DefaultModel    string `json:"defaultModel"`
	}
	if json.Unmarshal(stripJSONC(raw), &settings) != nil {
		return ""
	}
	return joinProviderModel(settings.DefaultProvider, settings.DefaultModel)
}

// joinProviderModel builds the provider/model id these CLIs list. A model with
// no provider is returned bare; a provider with no model selects nothing.
func joinProviderModel(provider, model string) string {
	provider, model = strings.TrimSpace(provider), strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if provider == "" || strings.HasPrefix(model, provider+"/") {
		return model
	}
	return provider + "/" + model
}

// stripJSONC removes // and /* */ comments and trailing commas so JSONC config
// files (opencode.jsonc) decode with encoding/json. String contents are kept
// verbatim, including sequences that look like comments inside URLs.
func stripJSONC(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	inString, escaped := false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(raw) && raw[i+1] == '/':
			for i < len(raw) && raw[i] != '\n' {
				i++
			}
			if i < len(raw) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(raw) && raw[i+1] == '*':
			i += 2
			for i+1 < len(raw) && (raw[i] != '*' || raw[i+1] != '/') {
				i++
			}
			i++
		case c == ']' || c == '}':
			// Drop a trailing comma left before this closer.
			j := len(out) - 1
			for j >= 0 && (out[j] == ' ' || out[j] == '\t' || out[j] == '\n' || out[j] == '\r') {
				j--
			}
			if j >= 0 && out[j] == ',' {
				out = append(out[:j], out[j+1:]...)
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}

func xdgDir(home string, env map[string]string, key, fallback string) string {
	if dir := envValue(env, key); dir != "" {
		return dir
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, fallback)
}

// envValue prefers the discovery request's environment and falls back to the
// daemon's own, the same precedence the spawned CLI would see.
func envValue(env map[string]string, key string) string {
	if value, ok := env[key]; ok {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(os.Getenv(key))
}
