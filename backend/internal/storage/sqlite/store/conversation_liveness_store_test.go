package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ListConversationLivenessForSessions is the Chat-side sign of life for
// stalled-turn detection: the newest write to any message or activity row of
// the session's conversation.
func TestListConversationLivenessForSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "live")

	// No Truncate: real chat writes bind time.Now() with its monotonic
	// reading, and the MAX() aggregate carries that tail back as text.
	base := time.Now().UTC()
	mkSession := func(id string) domain.SessionID {
		rec := sampleRecord("live")
		rec.ID = domain.SessionID(id)
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatalf("create session %s: %v", id, err)
		}
		return created.ID
	}
	chatting := mkSession("sess-chat")
	quiet := mkSession("sess-quiet")
	_ = mkSession("sess-bare")

	conversation, err := s.CreateConversation(ctx, "conv-live", domain.ConversationScopeSession, "live", chatting, base)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if created, err := s.AppendUserMessage(ctx, conversation.ID, chatting, "gen-1",
		domain.ConversationMessage{ID: "m1", Text: "start", Origin: domain.MessageOriginHuman},
		"turn-1", base); err != nil || !created {
		t.Fatalf("append first message: created=%v err=%v", created, err)
	}
	// A later write must win over the older one.
	if created, err := s.AppendUserMessage(ctx, conversation.ID, chatting, "gen-1",
		domain.ConversationMessage{ID: "m2", Text: "delta", Origin: domain.MessageOriginProvider},
		"turn-2", base.Add(time.Minute)); err != nil || !created {
		t.Fatalf("append second message: created=%v err=%v", created, err)
	}

	live, err := s.ListConversationLivenessForSessions(ctx, []domain.SessionID{chatting, quiet})
	if err != nil {
		t.Fatalf("list conversation liveness: %v", err)
	}
	if got := live[chatting]; !got.Equal(base.Add(time.Minute)) {
		t.Fatalf("chatting liveness = %v, want the newest message write %v", got, base.Add(time.Minute))
	}
	if _, ok := live[quiet]; ok {
		t.Fatalf("quiet session has no conversation rows, got liveness %v", live[quiet])
	}
	if _, ok := live["sess-bare"]; ok {
		t.Fatalf("bare session has no conversation, got liveness %v", live["sess-bare"])
	}

	if out, err := s.ListConversationLivenessForSessions(ctx, nil); err != nil || len(out) != 0 {
		t.Fatalf("empty ids: out=%v err=%v, want empty map", out, err)
	}
}
