package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.TranscriptTurnEndDetector = (*Plugin)(nil)

// claudeTurnTailWindow bounds how much of a transcript's end is read to find
// its newest user record. An interrupt marker is small and written last, so
// it always sits in this window when it is the newest record that matters.
const claudeTurnTailWindow = 256 << 10

// claudeInterruptMarkers are the user messages Claude Code records when Esc
// interrupts a turn (verified against Claude Code 2.1.287 and 2.1.302).
var claudeInterruptMarkers = []string{
	"[Request interrupted by user]",
	"[Request interrupted by user for tool use]",
}

// LastTurnEnd reports the newest Claude Code turn end: an Esc interrupt, or a
// completed turn whose Stop (or StopFailure) hook never reached AO. Claude
// fires no hook when Esc interrupts a turn, whether it is streaming a reply,
// running a tool or waiting at a permission prompt: not Stop, not StopFailure,
// not the interrupted tool's PostToolUse. And any hook is lost when it fires
// while the daemon is down — a turn whose transcript shows its final assistant
// message then stayed active forever. Claude records both ends in the main
// conversation: the interrupt as a user message holding only a marker, and a
// completed turn as the assistant message the API returned stop_reason
// end_turn on.
//
// Only the newest main-conversation user or assistant record counts: a prompt,
// tool result or continuing assistant message after it means a later turn is
// running. A tool result is read as "a turn is running" even when it follows a
// completed turn: a background task can inject one after the end, and idling a
// session on injected output would lie about work that may genuinely follow.
func (p *Plugin) LastTurnEnd(ctx context.Context, transcriptPath string) (ports.TurnEnd, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.TurnEnd{}, false, err
	}
	path := strings.TrimSpace(transcriptPath)
	// The path comes from a hook payload. Read only what looks like a Claude
	// conversation transcript so the daemon never parses an unrelated file.
	if !filepath.IsAbs(path) || filepath.Ext(path) != ".jsonl" {
		return ports.TurnEnd{}, false, nil
	}
	tail, err := hookutil.ReadJSONLTail(path, claudeTurnTailWindow)
	if errors.Is(err, fs.ErrNotExist) {
		return ports.TurnEnd{}, false, nil
	}
	if err != nil {
		return ports.TurnEnd{}, false, err
	}
	end, ok := lastClaudeTurnEnd(tail)
	return end, ok, nil
}

// lastClaudeTurnEnd walks the records newest first and stops at the first
// main-conversation user or assistant record. An assistant record decides by
// its stop_reason: end_turn is the completed turn, anything else (tool_use,
// max_tokens) means the turn continued past it.
func lastClaudeTurnEnd(records []byte) (ports.TurnEnd, bool) {
	for len(records) > 0 {
		records = bytes.TrimRight(records, "\n")
		start := bytes.LastIndexByte(records, '\n') + 1
		line := records[start:]
		records = records[:start]
		if end, assistant, decided := claudeAssistantRecord(line); assistant {
			return end, decided
		}
		interrupt, user := claudeUserRecord(line)
		if user {
			return interrupt, !interrupt.At.IsZero()
		}
	}
	return ports.TurnEnd{}, false
}

// claudeAssistantRecord classifies one transcript record as a
// main-conversation assistant message. assistant is true when the record is
// one; decided is true when that message ends the turn (stop_reason end_turn)
// and end carries the completion. An assistant message with any other
// stop_reason — a tool call, a truncated reply — means the turn went on, so
// nothing is reported about it.
func claudeAssistantRecord(line []byte) (end ports.TurnEnd, assistant, decided bool) {
	// Bookkeeping records make up most of a transcript; skip them before decoding.
	if !bytes.Contains(line, []byte(`"assistant"`)) {
		return ports.TurnEnd{}, false, false
	}
	var record struct {
		Type        string `json:"type"`
		IsSidechain bool   `json:"isSidechain"`
		Timestamp   string `json:"timestamp"`
		Message     struct {
			StopReason string `json:"stop_reason"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "assistant" || record.IsSidechain {
		return ports.TurnEnd{}, false, false
	}
	if record.Message.StopReason != "end_turn" {
		return ports.TurnEnd{}, true, false
	}
	at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		return ports.TurnEnd{}, true, false
	}
	return ports.TurnEnd{At: at, Completed: true}, true, true
}

// claudeUserRecord classifies one transcript record. user is true for a
// main-conversation user record; interrupt.At is set only when it is an
// interrupt marker with a readable timestamp. A marker without one is not
// reported: dating it now could make an old interrupt look newer than the
// current prompt and idle a turn that is still running.
func claudeUserRecord(line []byte) (interrupt ports.TurnEnd, user bool) {
	// Assistant output, tool progress and bookkeeping records make up most of
	// a transcript; skip them before decoding.
	if !bytes.Contains(line, []byte(`"user"`)) {
		return ports.TurnEnd{}, false
	}
	var record struct {
		Type        string `json:"type"`
		IsSidechain bool   `json:"isSidechain"`
		Timestamp   string `json:"timestamp"`
		Message     struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "user" || record.IsSidechain {
		return ports.TurnEnd{}, false
	}
	if !IsInterruptMarker(record.Message.Content) {
		return ports.TurnEnd{}, true
	}
	at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		return ports.TurnEnd{}, true
	}
	return ports.TurnEnd{At: at, Interrupted: true}, true
}

// IsInterruptMarker reports whether a user message's content is exactly an
// interrupt marker. Claude Code writes the marker as a one-element text
// block array; a typed prompt is recorded as a plain string, so a prompt that
// is exactly the marker text is not an interrupt. The marker's
// interruptedMessageId cannot be required: Claude sets it only when Esc
// cancelled a known API message (2.1.302 omits it before the first response).
func IsInterruptMarker(content json.RawMessage) bool {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil || len(blocks) != 1 || blocks[0].Type != "text" {
		return false
	}
	for _, marker := range claudeInterruptMarkers {
		if blocks[0].Text == marker {
			return true
		}
	}
	return false
}
