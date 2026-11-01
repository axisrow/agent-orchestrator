package claudecode

import (
	"strings"
	"testing"
)

// claudeQueuedScreen reproduces the live pane shape of a Claude Code session
// holding typed input in its message queue: the composer box stays visible and
// the provider paints its queue state in the footer below the composer's lower
// border (fork issue #205).
func claudeQueuedScreen(footer string) string {
	rule := strings.Repeat("─", 48)
	return "⏺ Read 12 files\n" +
		"\n" +
		"✻ Worked for 8s\n" +
		"\n" +
		rule + "\n" +
		"│ ❯\n" +
		rule + "\n" +
		footer
}

func TestDetectQueuedPromptFindsQueueFooter(t *testing.T) {
	plugin := &Plugin{}
	tests := []struct {
		name   string
		footer string
	}{
		{
			name:   "send-now hint",
			footer: "  queued  Reports since your previous turn:…  (ctrl+x ctrl+s to send now)\n  ? for shortcuts\n",
		},
		{
			name:   "edit hint",
			footer: "  Press up to edit queued messages\n  ? for shortcuts\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !plugin.DetectQueuedPrompt(claudeQueuedScreen(tt.footer)) {
				t.Fatalf("DetectQueuedPrompt = false, want true for footer %q", tt.footer)
			}
		})
	}
}

func TestDetectQueuedPromptIgnoresTranscriptQuotes(t *testing.T) {
	plugin := &Plugin{}
	// The same phrases inside the transcript above the composer never prove a
	// queue — an orchestrator session can be reading prose that quotes them.
	transcript := "The daemon refused to write: queued (ctrl+x ctrl+s to send now).\n" +
		"Press up to edit queued messages is what the TUI showed.\n" +
		"✻ Worked for 8s\n\n" +
		strings.Repeat("─", 48) + "\n" +
		"│ ❯\n" +
		strings.Repeat("─", 48) + "\n" +
		"  ? for shortcuts\n"
	if plugin.DetectQueuedPrompt(transcript) {
		t.Fatal("DetectQueuedPrompt = true, want false for transcript quotes")
	}
	if plugin.DetectQueuedPrompt("") {
		t.Fatal("DetectQueuedPrompt = true, want false for an empty capture")
	}
	// No visible composer: the footer heuristic must not fire.
	if plugin.DetectQueuedPrompt("ctrl+x ctrl+s to send now\n") {
		t.Fatal("DetectQueuedPrompt = true, want false without a composer frame")
	}
}
