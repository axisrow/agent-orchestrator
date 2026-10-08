package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/gateway"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// GetGatewayEntry returns one scope's stored gateway record. A missing row —
// the state before the scope was ever configured — reports as (zero, false, nil).
func (s *Store) GetGatewayEntry(ctx context.Context, scope gateway.Scope, projectID string) (gateway.Entry, bool, error) {
	row, err := s.qr.GetGatewayEntry(ctx, gen.GetGatewayEntryParams{
		Scope:     string(scope),
		ProjectID: projectID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return gateway.Entry{}, false, nil
	}
	if err != nil {
		return gateway.Entry{}, false, fmt.Errorf("get gateway entry: %w", err)
	}
	return gateway.Entry{
		Scope:     gateway.Scope(row.Scope),
		ProjectID: row.ProjectID,
		BaseURL:   row.BaseURL,
		Token:     row.AuthToken,
		Model:     row.Model,
	}, true, nil
}

// UpsertGatewayEntry replaces one scope's gateway record.
func (s *Store) UpsertGatewayEntry(ctx context.Context, entry gateway.Entry, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.UpsertGatewayEntry(ctx, gen.UpsertGatewayEntryParams{
		Scope:     string(entry.Scope),
		ProjectID: entry.ProjectID,
		BaseURL:   entry.BaseURL,
		AuthToken: entry.Token,
		Model:     entry.Model,
		UpdatedAt: now,
	})
}

// ListGatewayEntries returns every stored gateway record.
func (s *Store) ListGatewayEntries(ctx context.Context) ([]gateway.Entry, error) {
	rows, err := s.qr.ListGatewayEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list gateway entries: %w", err)
	}
	entries := make([]gateway.Entry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, gateway.Entry{
			Scope:     gateway.Scope(row.Scope),
			ProjectID: row.ProjectID,
			BaseURL:   row.BaseURL,
			Token:     row.AuthToken,
			Model:     row.Model,
		})
	}
	return entries, nil
}
