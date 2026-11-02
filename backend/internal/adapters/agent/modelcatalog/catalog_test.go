package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestModelCommandUsesProjectWorkingDirectory(t *testing.T) {
	cmd := modelCommand(context.Background(), "agent", []string{"models"}, "/work/project", map[string]string{"OPENCODE_CONFIG": "/project/opencode.json"})
	if cmd.Dir != "/work/project" {
		t.Fatalf("Dir = %q, want /work/project", cmd.Dir)
	}
	if cmd.WaitDelay != commandTerminationWait {
		t.Fatalf("WaitDelay = %s, want %s", cmd.WaitDelay, commandTerminationWait)
	}
	if !environmentContains(cmd.Env, "OPENCODE_CONFIG=/project/opencode.json") {
		t.Fatalf("Env does not contain project override: %#v", cmd.Env)
	}
}

// OpenCode 2.0.14 exits 0 with no output the first time `models` runs in a
// folder, so every new project started with a failed, empty catalog.
func TestOpenCodeV2RetriesEmptyFirstRunInNewFolder(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "opencode2")
	script := "#!/bin/sh\nif [ ! -f seen ]; then : > seen; exit 0; fi\nprintf 'anthropic/claude-opus-5-5\\n'\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": t.TempDir(), "XDG_CACHE_HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir()}
	catalog, err := discoverCommandCatalog(context.Background(), "opencode-v2", binary, t.TempDir(), env, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 1 || catalog.Models[0].ID != "anthropic/claude-opus-5-5" {
		t.Fatalf("models = %#v, want the second run's list", catalog.Models)
	}
}

func TestNormalizeShowsConcreteNameForDefaultCatalogModel(t *testing.T) {
	got := normalize([]ports.AgentModelInfo{
		{ID: "opus", Label: "Opus (default)"},
		{ID: "sonnet", Label: "Default (recommended)"},
	})
	if len(got) != 2 || got[0].ID != "opus" || got[0].Label != "Opus" || !got[0].IsDefault ||
		got[1].ID != "sonnet" || got[1].Label != "sonnet" || !got[1].IsDefault {
		t.Fatalf("normalized models = %#v", got)
	}
}

func environmentContains(env []string, wanted string) bool {
	for _, item := range env {
		if item == wanted {
			return true
		}
	}
	return false
}

func TestCommandDiscoveryTimeoutAllowsSlowModelRegistries(t *testing.T) {
	if commandTimeout < 20*time.Second {
		t.Fatalf("commandTimeout = %s, want at least 20s", commandTimeout)
	}
}

func TestModelDiscoveryErrorExplainsTimeout(t *testing.T) {
	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	err := modelDiscoveryError(deadlineCtx, "kilocode", errors.New("signal: killed"), nil)
	if !strings.Contains(err.Error(), "kilocode model discovery timed out after 20s") {
		t.Fatalf("error = %q, want clear timeout", err)
	}
}

func TestModelDiscoveryErrorSurfacesCommandOutput(t *testing.T) {
	// A live-exit failure must carry the CLI's own stderr so an "exit status 1"
	// is diagnosable; ANSI is stripped and whitespace collapsed to one line.
	err := modelDiscoveryError(context.Background(), "opencode", errors.New("exit status 1"),
		[]byte("\x1b[31mError:\x1b[0m Configuration is invalid at\n  /x/opencode.json: bad file reference\n"))
	msg := err.Error()
	if !strings.Contains(msg, "opencode model discovery: exit status 1") {
		t.Fatalf("error = %q, want wrapped exit status", err)
	}
	if !strings.Contains(msg, "Configuration is invalid at /x/opencode.json: bad file reference") {
		t.Fatalf("error = %q, want single-line command output", err)
	}
	if strings.Contains(msg, "\x1b[") {
		t.Fatalf("error = %q, want ANSI stripped", err)
	}
}

func TestModelDiscoveryErrorTailBounded(t *testing.T) {
	err := modelDiscoveryError(context.Background(), "opencode", errors.New("exit status 1"),
		[]byte(strings.Repeat("x", discoveryErrorDetailMax*3)))
	// The detail is the wrapped error plus a bounded, ellipsis-prefixed tail.
	if detail := discoveryErrorDetail([]byte(strings.Repeat("x", discoveryErrorDetailMax*3))); len([]rune(detail)) != discoveryErrorDetailMax+1 {
		t.Fatalf("detail rune length = %d, want %d", len([]rune(detail)), discoveryErrorDetailMax+1)
	}
	if !strings.Contains(err.Error(), "…") {
		t.Fatalf("error = %q, want truncation ellipsis", err)
	}
}

func TestOpenCodeDiscoveryUsesStableModelsCommand(t *testing.T) {
	// Must be the bare `models` subcommand. `--pure` is a global flag some
	// opencode builds reject ("Unrecognized flag: --pure"), which would empty the
	// picker; the stable contract is `opencode models` with no rejectable flag.
	for _, agentID := range []string{"opencode-v2"} {
		spec, ok := commandSpecs[agentID]
		if !ok {
			t.Errorf("%s has no discovery command", agentID)
			continue
		}
		if len(spec.args) != 1 || spec.args[0] != "models" {
			t.Errorf("%s discovery args = %q, want [models]", agentID, spec.args)
		}
		models, err := spec.parser([]byte("anthropic/claude-sonnet-4-6\nopenai/gpt-5.4\n"))
		if err != nil || len(models) != 2 || models[0].ID != "anthropic/claude-sonnet-4-6" || models[1].ID != "openai/gpt-5.4" {
			t.Errorf("%s parsed models = %#v, %v", agentID, models, err)
		}
		base := Base(agentID)
		if !base.AllowCustom || base.CustomModelEntry != ports.CustomModelEntryDirect {
			t.Errorf("%s custom model policy = (%v, %q), want direct", agentID, base.AllowCustom, base.CustomModelEntry)
		}
	}
}

func TestOMPUsesDocumentedDiscoveryCommand(t *testing.T) {
	tests := []struct {
		agent string
		want  []string
	}{
		{agent: "omp", want: []string{"models", "--json"}},
	}
	for _, tc := range tests {
		t.Run(tc.agent, func(t *testing.T) {
			spec, ok := commandSpecs[tc.agent]
			if !ok {
				t.Fatalf("%s has no discovery command", tc.agent)
			}
			if !reflect.DeepEqual(spec.args, tc.want) {
				t.Fatalf("%s discovery args = %q, want %q", tc.agent, spec.args, tc.want)
			}
			if spec.parser == nil {
				t.Fatalf("%s discovery parser is nil", tc.agent)
			}
		})
	}
}

func TestBaseClassifiesDiscoveryAndManualAgents(t *testing.T) {
	tests := []struct {
		agent string
		mode  ports.ModelSelectionMode
		count int
	}{
		{agent: "claude-code", mode: ports.ModelSelectionCatalog},
		{agent: "codex", mode: ports.ModelSelectionCatalog},
		{agent: "opencode-v2", mode: ports.ModelSelectionCatalog},
		{agent: "agy", mode: ports.ModelSelectionCatalog},
		{agent: "pi", mode: ports.ModelSelectionCatalog},
		{agent: "omp", mode: ports.ModelSelectionCatalog},
		// An agent with no discovery source falls back to the manual catalog.
		{agent: "amp", mode: ports.ModelSelectionCatalog},
	}
	for _, tc := range tests {
		t.Run(tc.agent, func(t *testing.T) {
			got := Base(tc.agent)
			if got.SelectionMode != tc.mode || len(got.Models) != tc.count {
				t.Fatalf("Base(%q) = %#v", tc.agent, got)
			}
		})
	}
}

func TestClaudeWithoutDiscoveryDoesNotInventModels(t *testing.T) {
	request := claudeRequest(t)
	request.Binary = "/missing/claude"
	request.Env["ANTHROPIC_MODEL"] = "claude-opus-4-5-20251101"
	got, err := (Discoverer{}).Discover(context.Background(), request)
	if err == nil {
		t.Fatal("missing provider discovery was reported as success")
	}
	if len(got.Models) != 0 || !got.AllowCustom {
		t.Fatalf("catalog = %#v, want no invented choices with manual entry allowed", got)
	}
}

func TestCustomModelEntryPolicy(t *testing.T) {
	tests := []struct {
		agent         string
		wantEntryMode string
		wantSelection ports.ModelSelectionMode
	}{
		{agent: "claude-code", wantEntryMode: "direct", wantSelection: ports.ModelSelectionCatalog},
		{agent: "codex", wantEntryMode: "direct", wantSelection: ports.ModelSelectionCatalog},
		{agent: "opencode-v2", wantEntryMode: "direct", wantSelection: ports.ModelSelectionCatalog},
		{agent: "agy", wantEntryMode: "none", wantSelection: ports.ModelSelectionCatalog},
		{agent: "omp", wantEntryMode: "none", wantSelection: ports.ModelSelectionCatalog},
		{agent: "pi", wantEntryMode: "configured", wantSelection: ports.ModelSelectionCatalog},
		// An agent with no discovery source keeps the catalog-only manual shape.
		{agent: "amp", wantEntryMode: "none", wantSelection: ports.ModelSelectionCatalog},
	}

	for _, tc := range tests {
		t.Run(tc.agent, func(t *testing.T) {
			got := Base(tc.agent)
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(encoded, &wire); err != nil {
				t.Fatal(err)
			}
			if wire["customModelEntry"] != tc.wantEntryMode {
				t.Fatalf("Base(%q) customModelEntry = %#v, want %q", tc.agent, wire["customModelEntry"], tc.wantEntryMode)
			}
			if got.AllowCustom != (tc.wantEntryMode == "direct") {
				t.Fatalf("Base(%q) allowCustom = %v, want %v", tc.agent, got.AllowCustom, tc.wantEntryMode == "direct")
			}
			if got.SelectionMode != tc.wantSelection {
				t.Fatalf("Base(%q) selectionMode = %q, want %q", tc.agent, got.SelectionMode, tc.wantSelection)
			}
		})
	}
}

func TestParsePiModelsFromPrimeAgentStyleTableBuildsProviderQualifiedIDs(t *testing.T) {
	got, err := parsePiModels([]byte(`provider   model                 context  max-out  thinking  images
anthropic  claude-opus-4-8       200K     64K      yes       yes
openai     gpt-5.6-sol           400K     128K     yes       yes
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.AgentModelInfo{
		{ID: "anthropic/claude-opus-4-8", Label: "claude-opus-4-8", Provider: "anthropic", ContextWindow: 200_000},
		{ID: "openai/gpt-5.6-sol", Label: "gpt-5.6-sol", Provider: "openai", ContextWindow: 400_000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %#v, want %#v", got, want)
	}
}

func TestBaseDynamicCatalogsContainNoAOOwnedModelIDs(t *testing.T) {
	for _, agentID := range []string{"claude-code", "codex"} {
		t.Run(agentID, func(t *testing.T) {
			got := Base(agentID)
			if got.SelectionMode != ports.ModelSelectionCatalog || !got.AllowCustom || got.Source != "cli" {
				t.Fatalf("Base(%q) = %#v", agentID, got)
			}
			if len(got.Models) != 0 {
				t.Fatalf("Base(%q) models = %#v, want no AO-owned model IDs", agentID, got.Models)
			}
		})
	}
}

func TestCodexDiscoveryUsesStructuredProviderCatalog(t *testing.T) {
	discoverer := Discoverer{CodexModels: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatModel, error) {
		return []ports.ChatModel{
			{ID: "gpt-current", DisplayName: "GPT Current", Default: true},
			{ID: "gpt-other", DisplayName: "GPT Other"},
		}, nil
	}}
	got, err := discoverer.Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "codex", Binary: "/bin/codex"})
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.AgentModelInfo{
		{ID: "gpt-current", Label: "GPT Current", IsDefault: true},
		{ID: "gpt-other", Label: "GPT Other"},
	}
	if !reflect.DeepEqual(got.Models, want) || got.Source != "cli" {
		t.Fatalf("catalog = %#v, want models %#v", got, want)
	}
}

func TestCodexDiscoveryListsNewestModelsFirst(t *testing.T) {
	discoverer := Discoverer{CodexModels: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatModel, error) {
		return []ports.ChatModel{
			{ID: "gpt-5.2", DisplayName: "GPT-5.2", Default: true},
			{ID: "legacy", DisplayName: "Legacy"},
			{ID: "sol-5", DisplayName: "Sol 5"},
			{ID: "sol-6-astra", DisplayName: "Sol 6 Astra"},
			{ID: "gpt-5.10", DisplayName: "GPT-5.10"},
			{ID: "sol-6", DisplayName: "Sol 6"},
		}, nil
	}}
	got, err := discoverer.Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "codex", Binary: "/bin/codex"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, item := range got.Models {
		ids = append(ids, item.ID)
	}
	want := []string{"sol-6", "sol-6-astra", "gpt-5.10", "gpt-5.2", "sol-5", "legacy"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("model order = %v, want %v", ids, want)
	}
}

func TestParseIDLinesAcceptsOnlyWholeModelIDs(t *testing.T) {
	got := parseIDLines([]byte("\x1b[32mModels\x1b[0m\nanthropic/claude-sonnet\nopenai/gpt-5.4\nTip: use --model <id>\nopenai/gpt-5.4 duplicate\n"))
	if len(got) != 2 || got[0].ID != "anthropic/claude-sonnet" || got[1].ID != "openai/gpt-5.4" {
		t.Fatalf("models = %#v", got)
	}
}

func TestParseAgyModelsUsesFirstColumnAsModelID(t *testing.T) {
	got, err := parseAgyModels([]byte(`gemini-3.7-flash-high  Gemini 3.7 Flash (High)
claude-sonnet-4-6  Claude Sonnet 4.6 (Thinking)
gpt-oss-120b-medium  GPT-OSS 120B (Medium)
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.AgentModelInfo{
		{ID: "claude-sonnet-4-6", Label: "Claude Sonnet 4.6 (Thinking)"},
		{ID: "gemini-3.7-flash-high", Label: "Gemini 3.7 Flash (High)"},
		{ID: "gpt-oss-120b-medium", Label: "GPT-OSS 120B (Medium)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %#v, want %#v", got, want)
	}
}

func TestParsePiModelsBuildsProviderQualifiedIDs(t *testing.T) {
	got, err := parsePiModels([]byte(`provider   model                       context  max-out  thinking  images
anthropic  claude-sonnet-4-6           1M       64K      yes       yes
openai     gpt-5.5                     272K     128K     yes       yes
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "anthropic/claude-sonnet-4-6" || got[1].ID != "openai/gpt-5.5" {
		t.Fatalf("models = %#v", got)
	}
}

func TestParseJSONModelsFindsNestedModels(t *testing.T) {
	got, err := parseJSONModels([]byte(`{"providers":[{"id":"anthropic","models":[{"modelId":"claude-sonnet","displayName":"Claude Sonnet","isDefault":true}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("models = %#v", got)
	}
	var found bool
	for _, model := range got {
		if model.ID == "claude-sonnet" && model.Label == "Claude Sonnet" && model.IsDefault {
			found = true
		}
	}
	if !found {
		t.Fatalf("models = %#v, want nested claude-sonnet", got)
	}
}

func TestParseOMPModelsUsesSelectorAsLaunchID(t *testing.T) {
	got, err := parseJSONModels([]byte(`{"models":[{"provider":"anthropic","id":"claude-opus-5","selector":"anthropic/claude-opus-5","name":"Claude Opus 5"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.AgentModelInfo{{ID: "anthropic/claude-opus-5", Label: "Claude Opus 5", Provider: "anthropic"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %#v, want %#v", got, want)
	}
}

func TestParseJSONModelsUsesModelMapKeysAsSelectableIDs(t *testing.T) {
	got, err := parseJSONModels([]byte(`{
		"models": {
			"kimi-code/kimi-for-coding": {
				"provider": "managed:kimi-code",
				"model": "kimi-for-coding",
				"displayName": "K2.7 Coding"
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "kimi-code/kimi-for-coding" || got[0].Label != "K2.7 Coding" || got[0].Provider != "managed:kimi-code" {
		t.Fatalf("models = %#v, want provider-qualified Kimi config alias", got)
	}
}

func TestParseJSONModelsWalksGroupedModelsMaps(t *testing.T) {
	got, err := parseJSONModels([]byte(`{
		"models": {
			"available": [{"modelId": "claude-sonnet", "displayName": "Claude Sonnet"}]
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "claude-sonnet" || got[0].Label != "Claude Sonnet" {
		t.Fatalf("models = %#v, want recursively discovered claude-sonnet", got)
	}
}

func TestParseJSONModelsWalksProviderGroupsWithNestedModels(t *testing.T) {
	got, err := parseJSONModels([]byte(`{
		"models": {
			"anthropic": {
				"provider": "anthropic",
				"models": [{"modelId": "claude-sonnet", "displayName": "Claude Sonnet"}]
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "claude-sonnet" || got[0].Label != "Claude Sonnet" {
		t.Fatalf("models = %#v, want nested claude-sonnet without provider-group alias", got)
	}
}

func TestParseJSONModelsSupportsKiroAndDevinFields(t *testing.T) {
	got, err := parseJSONModels([]byte(`{
		"models": [{"model_name": "Auto", "model_id": "auto"}],
		"families": [{
			"slug": "claude-opus-5",
			"family_label": "Claude Opus 5",
			"variants": [{"model_uid": "claude-opus-5-high", "label": "Claude Opus 5 High"}]
		}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"auto":               true,
		"claude-opus-5":      true,
		"claude-opus-5-high": true,
	}
	for _, item := range got {
		delete(want, item.ID)
	}
	if len(want) != 0 {
		t.Fatalf("models = %#v, missing %#v", got, want)
	}
}

func writeClaudeSettings(t *testing.T, dir, model string) {
	t.Helper()
	settingsDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "{}"
	if model != "" {
		body = `{"model": "` + model + `"}`
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogFingerprintTracksTheConfiguredClaudeCodeModel(t *testing.T) {
	claudeRequest(t)
	t.Setenv("ANTHROPIC_MODEL", "")
	dir := t.TempDir()
	writeClaudeSettings(t, dir, "opus")

	first := CatalogFingerprint(context.Background(), "claude-code", "", dir, nil)
	if first == "" {
		t.Fatal("fingerprint is empty for a configured model")
	}
	writeClaudeSettings(t, dir, "haiku")
	second := CatalogFingerprint(context.Background(), "claude-code", "", dir, nil)
	if second == first {
		t.Fatalf("fingerprint unchanged (%q) after the configured model changed", second)
	}
}

func TestCatalogFingerprintTracksClaudeProviderInputs(t *testing.T) {
	claudeRequest(t)
	dir := t.TempDir()
	writeClaudeSettings(t, dir, "opus")
	base := map[string]string{
		"CLAUDE_CODE_USE_BEDROCK":     "1",
		"ANTHROPIC_BASE_URL":          "https://gateway.example",
		"AWS_REGION":                  "us-east-1",
		"ANTHROPIC_VERTEX_PROJECT_ID": "project-a",
		"ANTHROPIC_FOUNDRY_RESOURCE":  "resource-a",
		"ANTHROPIC_API_KEY":           "secret-a",
	}
	first := CatalogFingerprint(context.Background(), "claude-code", "", dir, base)
	if strings.Contains(first, "secret-a") {
		t.Fatal("catalog fingerprint exposed a raw credential")
	}
	changes := map[string]string{
		"CLAUDE_CODE_USE_BEDROCK":     "",
		"CLAUDE_CODE_USE_FOUNDRY":     "1",
		"ANTHROPIC_BASE_URL":          "https://other.example",
		"AWS_REGION":                  "eu-west-1",
		"ANTHROPIC_VERTEX_PROJECT_ID": "project-b",
		"ANTHROPIC_FOUNDRY_RESOURCE":  "resource-b",
		"ANTHROPIC_API_KEY":           "secret-b",
	}
	for key, value := range changes {
		t.Run(key, func(t *testing.T) {
			changed := make(map[string]string, len(base))
			for name, current := range base {
				changed[name] = current
			}
			changed[key] = value
			if got := CatalogFingerprint(context.Background(), "claude-code", "", dir, changed); got == first {
				t.Fatalf("fingerprint unchanged after %s changed", key)
			}
		})
	}
}

func TestClaudeCatalogFingerprintIncludesProviderIdentity(t *testing.T) {
	request := claudeRequest(t)
	identity := "firstParty\x00account-a"
	discoverer := Discoverer{ClaudeFingerprint: func(context.Context, ports.AgentModelDiscoveryRequest) string {
		return identity
	}}
	first := discoverer.CatalogFingerprint(context.Background(), request)
	identity = "firstParty\x00account-b"
	if got := discoverer.CatalogFingerprint(context.Background(), request); got == first {
		t.Fatal("fingerprint unchanged after provider account identity changed")
	}
	identity = "gateway\x00account-b"
	if got := discoverer.CatalogFingerprint(context.Background(), request); got == first {
		t.Fatal("fingerprint unchanged after CLI provider changed")
	}
}

func TestCatalogFingerprintTracksClaudeProviderSettings(t *testing.T) {
	claudeRequest(t)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	writeClaudeSettings(t, dir, "opus")
	if err := os.WriteFile(settingsPath, []byte(`{"model":"opus","env":{"ANTHROPIC_BASE_URL":"https://gateway-a.example"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	first := CatalogFingerprint(context.Background(), "claude-code", "", dir, nil)
	if err := os.WriteFile(settingsPath, []byte(`{"model":"opus","env":{"ANTHROPIC_BASE_URL":"https://gateway-b.example"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := CatalogFingerprint(context.Background(), "claude-code", "", dir, nil); got == first {
		t.Fatal("fingerprint unchanged after Claude provider settings changed")
	}
}

func TestClaudeCatalogDefaultUsesResolvedSettingsEnvironment(t *testing.T) {
	request := claudeRequest(t)
	configDir := t.TempDir()
	request.Env["CLAUDE_CONFIG_DIR"] = configDir
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"model":"top-level-model","env":{"ANTHROPIC_MODEL":"kimi-k2"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_MODEL", "daemon-model")
	catalog, err := discoverClaudeCatalog(context.Background(), request, func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		return []ports.AgentModelInfo{{ID: "kimi-k2", Label: "Kimi K2"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range catalog.Models {
		if item.IsDefault {
			if item.ID != "kimi-k2" {
				t.Fatalf("default = %q, want settings env model", item.ID)
			}
			return
		}
	}
	t.Fatal("configured gateway model is not the default")
}

func TestClaudeCatalogFingerprintUsesOnlyResolvedSettings(t *testing.T) {
	request := claudeRequest(t)
	configDir := t.TempDir()
	request.Env["CLAUDE_CONFIG_DIR"] = configDir
	request.Env["ANTHROPIC_API_KEY"] = "explicit-key"
	path := filepath.Join(configDir, "settings.json")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fingerprint := func() string { return (Discoverer{}).CatalogFingerprint(context.Background(), request) }
	write(`{"env":{"ANTHROPIC_API_KEY":"shadowed-key-a","SECRET":"unrelated-a","ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-4"}}`)
	first := fingerprint()
	write(`{"env":{"ANTHROPIC_API_KEY":"shadowed-key-b","SECRET":"unrelated-b","ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-4"}}`)
	if got := fingerprint(); got != first {
		t.Fatal("shadowed or unapproved settings changed the fingerprint")
	}
	write(`{"env":{"ANTHROPIC_API_KEY":"shadowed-key-b","SECRET":"unrelated-b","ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-5"}}`)
	if got := fingerprint(); got == first {
		t.Fatal("effective configured alias did not change the fingerprint")
	}
}

func TestCatalogFingerprintKeepsTheExecutableOnlyValueForConfiglessAgents(t *testing.T) {
	dir := t.TempDir()
	writeClaudeSettings(t, dir, "opus")
	// codex reads no configuration, so its fingerprint must stay byte-identical
	// to the executable fingerprint earlier daemons cached under.
	got := CatalogFingerprint(context.Background(), "codex", "codex", dir, nil)
	if want := BinaryVersion(context.Background(), "codex"); got != want {
		t.Fatalf("fingerprint = %q, want the executable fingerprint %q", got, want)
	}
}

// TestACPOnlyHarnessReportsDiscoveryFailure guards the ACP-only discovery
// path: a harness whose catalog comes only through ACP has no fallback source,
// and the generic path answers with an empty catalog and no error — which the
// caller stores as a successful discovery, parking the picker until the next
// calendar day and skipping the retry ladder. The error has to survive instead.
func TestACPOnlyHarnessReportsDiscoveryFailure(t *testing.T) {
	boom := errors.New("workspace path must be absolute")
	for _, agentID := range []string{"agy"} {
		t.Run(agentID, func(t *testing.T) {
			discoverer := Discoverer{ACPOptions: map[string]ACPOptionListFunc{
				agentID: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
					return nil, boom
				},
			}}
			_, err := discoverer.Discover(context.Background(), ports.AgentModelDiscoveryRequest{
				AgentID: agentID, Binary: "/bin/agent",
			})
			if err == nil {
				t.Fatal("an ACP-only harness swallowed its discovery failure; the caller will cache an empty catalog as success")
			}
			if !errors.Is(err, boom) {
				t.Fatalf("err = %v, want it to wrap %v", err, boom)
			}
		})
	}
}
