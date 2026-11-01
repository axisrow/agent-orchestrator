package claudecode

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.QueuedPromptDetector = (*Plugin)(nil)

// DetectQueuedPrompt reports whether Claude's current surface shows typed
// input held in its message queue — the "queued / ctrl+x ctrl+s to send now"
// state where text waits instead of having been submitted as a prompt (fork
// issue #205). Only the footer below the current composer is eligible:
// transcript prose can quote the same words without holding anything, so a
// surface without a visible composer never proves a queue.
func (p *Plugin) DetectQueuedPrompt(output string) bool {
	lines := terminalui.PlainTerminalLines(output)
	for _, line := range claudeFooterBelowComposer(lines) {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "to send now") || strings.Contains(lower, "edit queued messages") {
			return true
		}
	}
	return false
}

// claudeFooterBelowComposer returns the provider-owned footer lines rendered
// below the current composer's lower border — where Claude paints queue state
// and shortcut hints. Transcript above the border is never returned.
func claudeFooterBelowComposer(lines []string) []string {
	prompt := -1
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		line = strings.TrimPrefix(line, "│")
		if strings.HasPrefix(strings.TrimSpace(line), "❯") {
			prompt = i
			break
		}
	}
	if prompt < 0 {
		return nil
	}
	lowerBorder := -1
	for i := prompt + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if !claudeHorizontalRule(lines[i]) {
			return nil
		}
		lowerBorder = i
		break
	}
	if lowerBorder < 0 {
		return nil
	}
	return lines[lowerBorder+1:]
}
