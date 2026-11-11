package controllers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

const cancelQueuedTurnPath = "/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/cancel"
const reorderQueuedTurnsPath = "/api/v1/sessions/{sessionId}/conversation/queue/reorder"

// ReorderQueuedConversationTurnsRequest rewrites the durable queue order.
type ReorderQueuedConversationTurnsRequest struct {
	TurnIDs []string `json:"turnIds"`
}

func (c *ConversationsController) cancelQueuedTurn(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", cancelQueuedTurnPath)
		return
	}
	err := c.Svc.CancelQueuedTurn(
		r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")),
		chi.URLParam(r, "turnId"),
	)
	if err != nil {
		writeQueuedTurnMutationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ConversationsController) reorderQueuedTurns(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", reorderQueuedTurnsPath)
		return
	}
	var req ReorderQueuedConversationTurnsRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if len(req.TurnIDs) == 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_QUEUE_REORDER_INVALID", "queued turn order is invalid", nil)
		return
	}
	err := c.Svc.ReorderQueuedTurns(
		r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")),
		req.TurnIDs,
	)
	if err != nil {
		writeQueuedTurnMutationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeQueuedTurnMutationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrQueuedTurnNotAvailable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_TURN_NOT_QUEUED", "that message is no longer queued", nil)
	case errors.Is(err, chatsvc.ErrInvalidQueuedTurnOrder):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_QUEUE_REORDER_INVALID", "queued turn order is invalid", nil)
	default:
		writeConversationError(w, r, err)
	}
}
