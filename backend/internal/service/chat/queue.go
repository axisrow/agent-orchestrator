package chat

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// ErrInvalidQueuedTurnOrder refuses a queue reorder that does not match the
// current undispatched queue exactly.
var ErrInvalidQueuedTurnOrder = errors.New("invalid queued turn order")

// CancelQueuedTurn removes one undispatched queue item without stopping the
// running turn or cancelling later queue items.
func (s *Service) CancelQueuedTurn(
	ctx context.Context,
	id domain.SessionID,
	turnID string,
) error {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return err
	}
	return controller.CancelQueuedTurn(ctx, turnID)
}

// ReorderQueuedTurns rewrites the durable queue order for undispatched turns.
func (s *Service) ReorderQueuedTurns(
	ctx context.Context,
	id domain.SessionID,
	turnIDs []string,
) error {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return err
	}
	return controller.ReorderQueuedTurns(ctx, turnIDs)
}

// CancelQueuedTurn drops one queue row that has not yet dispatched.
func (c *Controller) CancelQueuedTurn(ctx context.Context, turnID string) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.handoffActive() {
		return ErrControllerHandoff
	}
	return c.store.CancelQueuedTurnByID(ctx, c.conversation.ID, turnID, c.now())
}

// ReorderQueuedTurns permutes undispatched queue rows without changing their text.
func (c *Controller) ReorderQueuedTurns(ctx context.Context, turnIDs []string) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.handoffActive() {
		return ErrControllerHandoff
	}
	if err := c.store.ReorderQueuedTurns(ctx, c.conversation.ID, turnIDs); err != nil {
		if errors.Is(err, store.ErrInvalidQueuedTurnOrder) {
			return ErrInvalidQueuedTurnOrder
		}
		return err
	}
	return nil
}
