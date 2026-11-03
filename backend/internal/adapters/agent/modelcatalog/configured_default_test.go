package modelcatalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// isolateHome points every config root discovery consults at an empty temp dir.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "OPENCODE_CONFIG", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG_CONTENT"} {
		t.Setenv(key, "")
	}
	managed := openCodeManagedConfigDirs
	openCodeManagedConfigDirs = func() []string { return nil }
	t.Cleanup(func() { openCodeManagedConfigDirs = managed })
	return home
}

func TestApplyConfiguredDefault(t *testing.T) {
	listed := func() []ports.AgentModelInfo {
		return []ports.AgentModelInfo{{ID: "a/one", Label: "one"}, {ID: "b/two", Label: "two"}}
	}

	got := applyConfiguredDefault(listed(), "B/TWO")
	if got[0].IsDefault || !got[1].IsDefault || len(got) != 2 {
		t.Errorf("case-insensitive match = %#v", got)
	}

	got = applyConfiguredDefault(listed(), "c/three")
	if len(got) != 3 || got[2].ID != "c/three" || !got[2].IsDefault {
		t.Errorf("unlisted configured model should be appended as default: %#v", got)
	}

	reported := listed()
	reported[0].IsDefault = true
	got = applyConfiguredDefault(reported, "b/two")
	if !got[0].IsDefault || got[1].IsDefault {
		t.Errorf("a CLI-reported default must win over config: %#v", got)
	}

	for _, configured := range []string{"", "  ", "default", "Default"} {
		if got := applyConfiguredDefault(listed(), configured); len(got) != 2 || got[0].IsDefault || got[1].IsDefault {
			t.Errorf("placeholder %q must not select a default: %#v", configured, got)
		}
	}
}

func TestConfiguredDefaultModelSources(t *testing.T) {
	home := isolateHome(t)

	writeConfig(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"defaultProvider": "anthropic", "defaultModel": "claude-opus-4-6"}`)
	if got := configuredDefaultModel("pi", nil); got != "anthropic/claude-opus-4-6" {
		t.Errorf("pi = %q", got)
	}

	if got := configuredDefaultModel("omp", nil); got != "" {
		t.Errorf("agent without a config source = %q, want empty", got)
	}
}

func TestStripJSONCKeepsStringsAndDropsTrailingCommas(t *testing.T) {
	got := parseJSONCModelKey([]byte(`{
		/* block */ "url": "https://example.com//x", // line
		"model": "a/b",
	}`))
	if got != "a/b" {
		t.Errorf("model = %q, want a/b", got)
	}
}

// OpenCode 2's project config layers are always unknown to discovery, so no
// global model ever becomes a trusted default and the fingerprint stays
// binary-only.
func TestConfiguredDefaultFingerprintV2StaysBinaryOnly(t *testing.T) {
	home := isolateHome(t)
	workDir := t.TempDir()
	if got := discoveryConfigInputs(context.Background(), "opencode-v2", workDir, nil); got != "" {
		t.Fatalf("no configured default must keep the binary-only fingerprint, got %q", got)
	}
	writeConfig(t, filepath.Join(home, ".config", "opencode", "opencode.json"), `{"model": "openai/gpt-5.4"}`)
	if got := discoveryConfigInputs(context.Background(), "opencode-v2", workDir, nil); got != "" {
		t.Fatalf("v2 global model must not feed the fingerprint, got %q", got)
	}
}
