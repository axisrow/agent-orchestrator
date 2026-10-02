package agentcreds

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeGatewaySettings(t *testing.T, dir, baseURL, token string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := "{}"
	if baseURL != "" {
		payload = `{"env":{"ANTHROPIC_BASE_URL":"` + baseURL + `","ANTHROPIC_AUTH_TOKEN":"` + token + `","ANTHROPIC_MODEL":"gateway-model"}}`
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProviderPinEnvEmptyPin(t *testing.T) {
	if got := ProviderPinEnv(context.Background(), t.TempDir(), ""); got != nil {
		t.Fatalf("ProviderPinEnv(\"\") = %v, want nil", got)
	}
}

func TestProviderPinEnvDirectShadowsProviderKeys(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	got := ProviderPinEnv(context.Background(), t.TempDir(), ProviderPinDirect)
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL"} {
		value, ok := got[key]
		if !ok || value != "" {
			t.Fatalf("direct pin %s = (%q,%v), want an empty shadow", key, value, ok)
		}
	}
}

func TestProviderPinEnvMatchesConfiguredEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	writeGatewaySettings(t, filepath.Join(home, ".claude"), "https://app.example", "app-token")
	project := t.TempDir()
	got := ProviderPinEnv(context.Background(), project, "https://app.example")
	if got["ANTHROPIC_BASE_URL"] != "https://app.example" || got["ANTHROPIC_AUTH_TOKEN"] != "app-token" || got["ANTHROPIC_MODEL"] != "gateway-model" {
		t.Fatalf("app-scope pin = %v", got)
	}
}

// Keys the matched entry does not set must be omitted, not emptied: the pin
// overlay runs after the project env, and an empty value would clear a
// project-configured model or credential the entry never had.
func TestProviderPinEnvOmitsUnsetEntryKeys(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"env":{"ANTHROPIC_BASE_URL":"https://bare.example"}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ProviderPinEnv(context.Background(), t.TempDir(), "https://bare.example")
	if got["ANTHROPIC_BASE_URL"] != "https://bare.example" {
		t.Fatalf("pin = %v, want the base URL set", got)
	}
	for _, key := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
		if value, ok := got[key]; ok {
			t.Fatalf("pin carries %s=%q, want the key omitted", key, value)
		}
	}
}

func TestProviderPinEnvProjectScopeWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	writeGatewaySettings(t, filepath.Join(home, ".claude"), "https://same.example", "app-token")
	project := t.TempDir()
	writeGatewaySettings(t, filepath.Join(project, ".claude"), "https://same.example", "project-token")
	got := ProviderPinEnv(context.Background(), project, "https://same.example")
	if got["ANTHROPIC_AUTH_TOKEN"] != "project-token" {
		t.Fatalf("pin token = %q, want the project scope to win", got["ANTHROPIC_AUTH_TOKEN"])
	}
}

func TestProviderPinEnvUnknownEntryDegradesToDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	writeGatewaySettings(t, filepath.Join(home, ".claude"), "https://other.example", "tok")
	if got := ProviderPinEnv(context.Background(), t.TempDir(), "https://gone.example"); got != nil {
		t.Fatalf("unknown pin = %v, want nil", got)
	}
}
