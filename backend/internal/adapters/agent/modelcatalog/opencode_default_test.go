package modelcatalog

import (
	"os"
	"path/filepath"
	"testing"
)

func modelJSON(model string) string { return `{"model": "` + model + `"}` }

// newOpenCodeFixture points every config root discovery consults at an empty
// temp dir and returns a plain working directory for the session.
func newOpenCodeFixture(t *testing.T) string {
	t.Helper()
	home := isolateHome(t)
	pkg := filepath.Join(t.TempDir(), "pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(home, ".config", "opencode", "opencode.json"), modelJSON("global/a"))
	return pkg
}

// v2 also reads the launch worktree's ancestors above its git root (e.g.
// <AO_DATA_DIR>/opencode.json), which discovery cannot see, so only layers
// that outrank project config are trusted.
func TestOpenCodeV2DefaultTrustsOnlyLayersAboveProjectConfig(t *testing.T) {
	pkg := newOpenCodeFixture(t)
	if got := configuredDefaultModel("opencode-v2", pkg, nil); got != "" {
		t.Errorf("v2 global model = %q, want unresolved", got)
	}
	if got := configuredDefaultModel("opencode-v2", pkg, map[string]string{"OPENCODE_CONFIG_CONTENT": modelJSON("inline/e")}); got != "inline/e" {
		t.Errorf("v2 OPENCODE_CONFIG_CONTENT = %q, want inline/e", got)
	}
	managed := t.TempDir()
	openCodeManagedConfigDirs = func() []string { return []string{managed} }
	writeConfig(t, filepath.Join(managed, "opencode.json"), modelJSON("managed/f"))
	if got := configuredDefaultModel("opencode-v2", pkg, map[string]string{"OPENCODE_CONFIG_CONTENT": modelJSON("inline/e")}); got != "managed/f" {
		t.Errorf("v2 managed config = %q, want managed/f", got)
	}
}

func TestOpenCodeDefaultLeavesUncertainWinnersUnresolved(t *testing.T) {
	pkg := newOpenCodeFixture(t)

	// AO replaces OPENCODE_CONFIG at TUI launch but keeps it for ACP.
	custom := filepath.Join(t.TempDir(), "custom.json")
	writeConfig(t, custom, modelJSON("custom/g"))
	if got := configuredDefaultModel("opencode-v2", pkg, map[string]string{"OPENCODE_CONFIG": custom}); got != "" {
		t.Errorf("winning OPENCODE_CONFIG = %q, want unresolved", got)
	}

	configDir := t.TempDir()
	writeConfig(t, filepath.Join(configDir, "opencode.json"), modelJSON("dir/h"))
	if got := configuredDefaultModel("opencode-v2", pkg, map[string]string{"OPENCODE_CONFIG_DIR": configDir}); got != "" {
		t.Errorf("OPENCODE_CONFIG_DIR model = %q, want unresolved", got)
	}

	// Sibling global files that disagree have no documented merge order.
	home, _ := os.UserHomeDir()
	writeConfig(t, filepath.Join(home, ".config", "opencode", "opencode.jsonc"), modelJSON("global/other"))
	if got := configuredDefaultModel("opencode-v2", pkg, nil); got != "" {
		t.Errorf("conflicting global configs = %q, want unresolved", got)
	}
}

func TestOpenCodeDefaultWithoutGitRootIsUnresolved(t *testing.T) {
	home := isolateHome(t)
	writeConfig(t, filepath.Join(home, ".config", "opencode", "opencode.json"), modelJSON("global/a"))
	workDir := t.TempDir()
	if got := configuredDefaultModel("opencode-v2", workDir, nil); got != "" {
		t.Errorf("opencode-v2 without a git root = %q, want unresolved", got)
	}
}
