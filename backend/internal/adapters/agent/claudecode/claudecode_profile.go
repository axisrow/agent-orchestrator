// claudecode_profile.go holds the per-role env-profile surface of the Claude
// Code adapter: the MCP/plugin flags a project or role config contributes to
// the launch and restore argv, plus the transcript probe that decides whether
// a --resume target is real.
//
// It is deliberately a separate file from claudecode.go. Upstream rewrites the
// command-building functions regularly (most recently moving them wholesale
// into pkg/agentruntime), and every such rewrite used to collide with these
// additions line-by-line. Keeping them here leaves claudecode.go carrying only
// two ProviderArgs fields and one guard call.
//
// This shrinks textual conflicts, not the coupling: the file still depends on
// claudecode.go's Plugin and on agentruntime's ProviderArgs. If upstream moves
// or renames those, this file will not conflict — it will simply stop
// compiling after an otherwise clean rebase.
package claudecode

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// claudeProfileArgs renders the MCP and plugin flags a role's config
// contributes. Placement is agentruntime's job — see ProviderArgs.
//
// Upstream documents that field as "trusted host-owned flags"; these values
// come from an AO project/role config instead, so we lean on the wording
// slightly. It holds: callers validate the config (ports.AgentConfig.Validate)
// before building a command, the flags land in an argv array rather than a
// shell string, and the sibling codex adapter fills the same field the same
// way.
func claudeProfileArgs(cfg ports.AgentConfig) []string {
	var args []string
	appendMCPFlags(&args, cfg.MCP)
	appendPluginFlags(&args, cfg.PluginDirs)
	return args
}

// appendMCPFlags emits claude-code's per-session MCP flags. Each MCPConfig
// entry is passed to the repeatable --mcp-config as-is (a JSON string or a path
// to a JSON file — both accepted by the CLI). Strict adds --strict-mcp-config
// so the session ignores every other MCP source, isolating the worker. A nil
// MCPConfig emits nothing, so an unset config inherits the global MCP set as
// before. Strict alone (empty Configs) is valid: it means "no MCP at all".
func appendMCPFlags(cmd *[]string, mcp *domain.MCPConfig) {
	if mcp == nil {
		return
	}
	for _, c := range mcp.Configs {
		if c = strings.TrimSpace(c); c != "" {
			*cmd = append(*cmd, "--mcp-config", c)
		}
	}
	if mcp.Strict {
		*cmd = append(*cmd, "--strict-mcp-config")
	}
}

// appendPluginFlags emits --plugin-dir / --plugin-url for each entry. An
// http(s):// entry maps to --plugin-url (a fetched zip); any other value is
// treated as a local path and mapped to --plugin-dir. Both flags are repeatable,
// so one is emitted per entry. Empty/whitespace entries are skipped.
func appendPluginFlags(cmd *[]string, dirs []string) {
	for _, d := range dirs {
		if d = strings.TrimSpace(d); d == "" {
			continue
		}
		if isPluginURL(d) {
			*cmd = append(*cmd, "--plugin-url", d)
		} else {
			*cmd = append(*cmd, "--plugin-dir", d)
		}
	}
}

// isPluginURL reports whether s is an http(s) plugin URL rather than a local
// path, deciding --plugin-url vs --plugin-dir.
func isPluginURL(s string) bool {
	// URL schemes are case-insensitive (RFC 3986): HTTPS://example.com/p.zip is
	// a valid plugin URL, so parse and compare the scheme rather than matching a
	// lowercase prefix (which would route it to --plugin-dir as a local path).
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")
}

// claudeProjectSlug mirrors Claude Code's own transcript-directory naming:
// every "/" and "." in the absolute workspace path becomes "-". This is the
// same rule Claude Code uses to place a session's JSONL transcript under
// ~/.claude/projects/<slug>/<sessionID>.jsonl.
func claudeProjectSlug(workspacePath string) string {
	slug := strings.ReplaceAll(workspacePath, "/", "-")
	return strings.ReplaceAll(slug, ".", "-")
}

// claudeTranscriptPath returns the on-disk path of the transcript a
// `claude --resume <sessionID>` would need, given the workspace it would run
// in. It returns ok=false when workspacePath is empty — there's nothing to
// derive a slug from — so callers can treat "can't tell" differently from
// "checked, and it's missing".
func claudeTranscriptPath(workspacePath, sessionID string) (path string, ok bool, err error) {
	if workspacePath == "" {
		return "", false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("claude-code: resolve home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "projects", claudeProjectSlug(workspacePath), sessionID+".jsonl"), true, nil
}

// claudeTranscriptExists reports whether a --resume target actually has a
// transcript on disk. Without this check, GetRestoreCommand would report
// ok=true for any non-blank session id — including one this process
// synthesized itself (see claudeSessionUUID) that may never correspond to a
// real Claude session, e.g. after a reboot that dropped the SessionStart hook
// call, or a pruned ~/.claude/projects entry. `claude --resume` then exits 1
// with "No conversation found with session ID", and because ok=true short-
// circuits restoreArgv's fresh-launch fallback (session_manager/manager.go),
// the session is stranded rather than relaunched. If we can't tell (empty
// workspace path, or a stat error other than "not found"), we don't block —
// only a confirmed absence should force a fallback.
func claudeTranscriptExists(workspacePath, sessionID string) bool {
	path, ok, err := claudeTranscriptPath(workspacePath, sessionID)
	if !ok || err != nil {
		return true
	}
	_, err = os.Stat(path)
	return err == nil
}
