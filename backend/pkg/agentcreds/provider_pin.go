package agentcreds

import (
	"context"
	"path/filepath"
	"strings"
)

// Provider pin support for per-role provider selection (issue #6156): a project
// role (worker / orchestrator / reviewer) may pin its Anthropic provider
// independently of the gateway the app- or project-scope settings resolve. The
// pin vocabulary lives in domain (ProviderDirect and validated base URLs); this
// file is the one place that turns a pin into the launch env entries every
// consumer — session spawn, relaunch, reviewer launch, and model-catalog
// discovery — applies, so a pinned role and its stamp can never disagree.

// ProviderPinDirect is the pin value that bypasses every configured gateway.
// It mirrors domain.ProviderDirect; duplicated as an unexported-looking
// literal to keep pkg/ free of an internal-domain import.
const ProviderPinDirect = "direct"

// providerPinShadowKeys are the env keys the direct pin shadows.
var providerPinShadowKeys = []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL"}

// ProviderPinEnv maps a role's provider pin to the env entries a launch must
// carry. An empty pin returns nil (follow the normal settings resolution). The
// direct pin returns empty strings for the provider keys — explicit launch env
// wins over settings files in ResolveClaudeSettings, so an empty value shadows
// the configured gateway. A base-URL pin returns the URL and the credential
// keys read from the matching gateway entry, project scope first (it overrides
// the app scope the same way the resolution chain does); a pin that matches no
// configured entry returns nil, degrading to the default resolution — the
// settings screen only offers real entries, so this only fires for a
// hand-edited config pointing at a gateway that no longer exists.
//
// projectDir is the registered project root, where the project-scope gateway
// entry lives; it may be empty when there is no project.
func ProviderPinEnv(ctx context.Context, projectDir, pin string) map[string]string {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return nil
	}
	if pin == ProviderPinDirect {
		env := make(map[string]string, len(providerPinShadowKeys))
		for _, key := range providerPinShadowKeys {
			env[key] = ""
		}
		return env
	}
	// A pinned URL must name a configured entry; its credentials travel with
	// the entry. Project scope wins over app scope, mirroring the resolver's
	// user < project ordering.
	paths := make([]string, 0, 2)
	if dir := strings.TrimSpace(projectDir); dir != "" {
		paths = append(paths, filepath.Join(dir, ".claude", "settings.json"))
	}
	if appDir, err := claudeConfigDir(ResolveOptions{}); err == nil {
		paths = append(paths, filepath.Join(appDir, "settings.json"))
	}
	for _, path := range paths {
		settings := readClaudeSettings(ctx, path)
		if settings.Env["ANTHROPIC_BASE_URL"] != pin {
			continue
		}
		return map[string]string{
			"ANTHROPIC_BASE_URL":         pin,
			"ANTHROPIC_AUTH_TOKEN":       settings.Env["ANTHROPIC_AUTH_TOKEN"],
			"ANTHROPIC_API_KEY":          settings.Env["ANTHROPIC_API_KEY"],
			"ANTHROPIC_MODEL":            settings.Env["ANTHROPIC_MODEL"],
			"ANTHROPIC_SMALL_FAST_MODEL": settings.Env["ANTHROPIC_SMALL_FAST_MODEL"],
		}
	}
	return nil
}
