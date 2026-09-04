// Package processenv builds child-process environments shared by Chat drivers.
package processenv

import (
	"os"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/envfilter"
)

// Merge overlays session-specific values on the daemon environment and returns
// the KEY=VALUE form expected by os/exec. Sorting makes launches deterministic
// enough to inspect and compare in tests and process diagnostics.
//
// Parent-session identity markers (see envfilter) are dropped: if this daemon
// happens to have been started from inside a Claude Code session, its own
// CLAUDECODE/CLAUDE_CODE_SESSION_ID/etc must not leak into a worker's process,
// or the worker's own claude-code misidentifies itself as a child session.
func Merge(overlay map[string]string) []string {
	merged := make(map[string]string, len(os.Environ())+len(overlay))
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if envfilter.IsParentSessionMarker(key) {
			continue
		}
		merged[key] = value
	}
	for key, value := range overlay {
		merged[key] = value
	}

	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+merged[key])
	}
	return out
}
