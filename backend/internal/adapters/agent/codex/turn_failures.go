package codex

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

// codexTurnTailWindow bounds how much of a rollout's end is read to find its
// last turn boundary. A turn's end record is written last and is small, so it
// always sits in this window when it is the newest record that matters; a
// window with no boundary at all reports nothing.
const codexTurnTailWindow = 256 << 10

// codexTurnBoundaryTypes are the quoted event_msg payload types that start or
// end a turn. Matching them as substrings skips the JSON decode for the
// messages, reasoning and token counts that make up most of a rollout.
var codexTurnBoundaryTypes = [][]byte{
	[]byte(`"task_started"`), []byte(`"turn_started"`), []byte(`"turn_aborted"`),
	[]byte(`"task_complete"`), []byte(`"turn_complete"`),
}

// LastTurnEnd reads the end of a Codex rollout. Codex runs its Stop hook only
// on a turn whose model request succeeded (codex-rs core session/turn.rs):
// a turn that ends on a usage limit, an unsupported model or an expired
// sign-in fires no hook at all. And any hook is lost when it fires while the
// daemon is down. Codex still persists every turn end to the rollout as an
// event_msg task_complete, with an error when the turn failed — so a clean
// task_complete is the completed turn, readable even when its Stop hook never
// reached AO. An Esc interrupt fires no Stop either (only Codex 0.150+'s
// Interrupt hook, which AO does not rely on); Codex records it as a
// turn_aborted with reason interrupted.
//
// Only the newest turn boundary counts. A task_started after a turn end means
// a later turn is running, so the older end is history.
func (p *Plugin) LastTurnEnd(ctx context.Context, transcriptPath string) (ports.TurnEnd, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.TurnEnd{}, false, err
	}
	path := strings.TrimSpace(transcriptPath)
	name := filepath.Base(path)
	// The path comes from a hook payload. Read only what looks like a Codex
	// rollout so the daemon never parses an unrelated file named there.
	if !filepath.IsAbs(path) || !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
		return ports.TurnEnd{}, false, nil
	}
	tail, err := hookutil.ReadJSONLTail(path, codexTurnTailWindow)
	if errors.Is(err, fs.ErrNotExist) {
		return ports.TurnEnd{}, false, nil
	}
	if err != nil {
		return ports.TurnEnd{}, false, err
	}
	end, ok := lastCodexTurnEnd(tail)
	return end, ok, nil
}

// lastCodexTurnEnd walks the records newest first and stops at the first turn
// boundary. It reports the turn end only when that boundary is a turn end,
// carrying its kind: an error, an interrupt, or a normal completion.
func lastCodexTurnEnd(records []byte) (ports.TurnEnd, bool) {
	for len(records) > 0 {
		records = bytes.TrimRight(records, "\n")
		start := bytes.LastIndexByte(records, '\n') + 1
		line := records[start:]
		records = records[:start]
		end, boundary, ended := codexTurnBoundary(line)
		if boundary {
			return end, ended
		}
	}
	return ports.TurnEnd{}, false
}

// codexTurnBoundary classifies one rollout record. boundary is true for a
// turn start or end; ended is true only for a turn end. The turn ended at the
// record's own timestamp. A turn end without a readable one is not reported:
// dating it now could make an old end look newer than the current prompt and
// idle a turn that is still running.
func codexTurnBoundary(line []byte) (end ports.TurnEnd, boundary, ended bool) {
	// Cheap filter before decoding: large tool output records are never
	// turn boundaries, and neither is an event_msg of any other type.
	if !bytes.Contains(line, []byte(`"event_msg"`)) || !containsCodexTurnBoundaryType(line) {
		return ports.TurnEnd{}, false, false
	}
	var record struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			Type   string          `json:"type"`
			Reason string          `json:"reason"`
			Error  json.RawMessage `json:"error"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "event_msg" {
		return ports.TurnEnd{}, false, false
	}
	switch record.Payload.Type {
	case "task_started", "turn_started":
		// A newer turn is running.
		return ports.TurnEnd{}, true, false
	case "turn_aborted":
		// Esc. Other reasons (a turn replaced by a newer one, a review
		// ending) are not a turn the user stopped.
		if record.Payload.Reason != "interrupted" {
			return ports.TurnEnd{}, true, false
		}
		at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
		if err != nil {
			return ports.TurnEnd{}, true, false
		}
		return ports.TurnEnd{At: at, Interrupted: true}, true, true
	case "task_complete", "turn_complete":
	default:
		return ports.TurnEnd{}, false, false
	}
	errorJSON := bytes.TrimSpace(record.Payload.Error)
	if len(errorJSON) == 0 || bytes.Equal(errorJSON, []byte("null")) {
		at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
		if err != nil {
			return ports.TurnEnd{}, true, false
		}
		return ports.TurnEnd{At: at, Completed: true}, true, true
	}
	var detail struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(errorJSON, &detail)
	at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		return ports.TurnEnd{}, true, false
	}
	return ports.TurnEnd{At: at, Message: detail.Message}, true, true
}

func containsCodexTurnBoundaryType(line []byte) bool {
	for _, kind := range codexTurnBoundaryTypes {
		if bytes.Contains(line, kind) {
			return true
		}
	}
	return false
}
