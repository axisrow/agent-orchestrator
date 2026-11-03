package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// CreateReport atomically persists one validated pending report and all outputs.
func (s *Store) CreateReport(ctx context.Context, rec domain.ReportRecord) (domain.ReportRecord, error) {
	if err := rec.Validate(); err != nil {
		return domain.ReportRecord{}, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var row gen.Delivery
	err := s.inTx(ctx, "create report", func(q *gen.Queries) error {
		var err error
		row, err = q.CreateReport(ctx, gen.CreateReportParams{
			ID: rec.ID, SessionID: string(rec.SessionID), ProjectID: string(rec.ProjectID),
			State: string(rec.State), Note: rec.Note, Message: rec.Message,
			CreatedAt: rec.CreatedAt, AvailableAt: rec.AvailableAt,
			SettlementDeadline: nullTime(rec.SettlementDeadline), RepeatCount: rec.RepeatCount,
		})
		if err != nil {
			return err
		}
		for i, output := range rec.Outputs {
			if err := q.CreateReportOutput(ctx, gen.CreateReportOutputParams{
				ReportID: rec.ID, Position: int64(i), Kind: string(output.Kind),
				Reference: output.Reference, Label: output.Label,
			}); err != nil {
				return err
			}
		}
		return journalDeliveryEvents(ctx, q, []gen.Delivery{row},
			"", string(domain.ReportPending), domain.DeliveryEventEmitted, "", rec.CreatedAt)
	})
	if err != nil {
		return domain.ReportRecord{}, fmt.Errorf("create report %s: %w", rec.ID, err)
	}
	created := reportFromGen(row)
	created.Outputs = append([]domain.ReportOutput(nil), rec.Outputs...)
	return created, nil
}

// GetReport returns one report by durable ID with its ordered outputs.
func (s *Store) GetReport(ctx context.Context, id string) (domain.ReportRecord, bool, error) {
	row, err := s.qr.GetReport(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReportRecord{}, false, nil
	}
	if err != nil {
		return domain.ReportRecord{}, false, fmt.Errorf("get report %s: %w", id, err)
	}
	rec, err := s.reportWithOutputs(ctx, row)
	if err != nil {
		return domain.ReportRecord{}, false, err
	}
	return rec, true, nil
}

// ListReportsBySession returns a worker's reports in creation order.
func (s *Store) ListReportsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReportRecord, error) {
	rows, err := s.qr.ListReportsBySession(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	return s.reportsWithOutputs(ctx, rows)
}

// ListReportedPRURLs returns only a worker's structured created-PR outputs.
func (s *Store) ListReportedPRURLs(ctx context.Context, id domain.SessionID) ([]string, error) {
	urls, err := s.qr.ListReportedPRURLs(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("list reported PR URLs for %s: %w", id, err)
	}
	return urls, nil
}

// ListReportsByProject is the read-only persisted report projection. Consumers
// such as Project Summary do not participate in the delivery claim lifecycle.
func (s *Store) ListReportsByProject(ctx context.Context, id domain.ProjectID) ([]domain.ReportRecord, error) {
	rows, err := s.qr.ListReportsByProject(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("list project reports: %w", err)
	}
	return s.reportsWithOutputs(ctx, rows)
}

// ListPendingReportSchedule returns the earliest pending work regardless of
// availability so a coordinator can restore the original deadline after restart.
func (s *Store) ListPendingReportSchedule(ctx context.Context, limit int64) ([]domain.ReportRecord, error) {
	rows, err := s.qr.ListPendingReportSchedule(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list report schedule: %w", err)
	}
	return s.reportsWithOutputs(ctx, rows)
}

// ClaimPendingReportBatch atomically claims every pending report for a project.
// A due, attention, or piggyback trigger therefore includes earlier batch work.
func (s *Store) ClaimPendingReportBatch(ctx context.Context, projectID domain.ProjectID, token string, at time.Time) ([]domain.ReportRecord, error) {
	if projectID == "" || token == "" || at.IsZero() {
		return nil, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var rows []gen.Delivery
	err := s.inTx(ctx, "claim project report batch", func(q *gen.Queries) error {
		scheduled, err := q.ListPendingReportsByProjectSchedule(ctx, gen.ListPendingReportsByProjectScheduleParams{
			ProjectID: string(projectID), Limit: 1,
		})
		if err != nil || len(scheduled) == 0 {
			return err
		}
		batchID := scheduled[0].DeliveryBatchID
		if batchID == "" {
			batchID = "report-batch:" + scheduled[0].ID
			if err := q.AssignPendingReportsToBatch(ctx, gen.AssignPendingReportsToBatchParams{
				DeliveryBatchID: batchID, ProjectID: string(projectID),
			}); err != nil {
				return err
			}
		}
		rows, err = q.ClaimPendingReportsByBatch(ctx, gen.ClaimPendingReportsByBatchParams{
			ProjectID: string(projectID), DeliveryBatchID: batchID,
			ClaimToken: token, ClaimedAt: nullTime(at),
		})
		if err != nil {
			return err
		}
		return journalDeliveryEvents(ctx, q, rows,
			string(domain.ReportPending), string(domain.ReportClaimed), domain.DeliveryEventDeliveryAttempt, "", at)
	})
	if err != nil {
		return nil, fmt.Errorf("claim project report batch: %w", err)
	}
	return s.reportsWithOutputs(ctx, orderedReportRows(rows))
}

// runDeliveryBatchTransition runs one token-fenced batch transition and its
// journal append in a single transaction: the transition's returned rows feed
// the journal, so a journal failure rolls the transition back.
func (s *Store) runDeliveryBatchTransition(ctx context.Context, what string, at time.Time, transition func(q *gen.Queries) ([]gen.Delivery, error), fromState, toState, cause, reason string) ([]gen.Delivery, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var rows []gen.Delivery
	err := s.inTx(ctx, what, func(q *gen.Queries) error {
		var err error
		rows, err = transition(q)
		if err != nil {
			return err
		}
		return journalDeliveryEvents(ctx, q, rows, fromState, toState, cause, reason, at)
	})
	return rows, err
}

// AcknowledgeReportBatch completes only the project batch holding token.
func (s *Store) AcknowledgeReportBatch(ctx context.Context, projectID domain.ProjectID, token string, at time.Time) (int, error) {
	if projectID == "" || token == "" || at.IsZero() {
		return 0, domain.ErrInvalidReport
	}
	rows, err := s.runDeliveryBatchTransition(ctx, "acknowledge project report batch", at,
		func(q *gen.Queries) ([]gen.Delivery, error) {
			return q.AcknowledgeReportBatch(ctx, gen.AcknowledgeReportBatchParams{
				ProjectID: string(projectID), ClaimToken: token, AcknowledgedAt: nullTime(at),
			})
		},
		string(domain.ReportClaimed), string(domain.ReportAcknowledged), domain.DeliveryEventAcknowledged, "")
	if err != nil {
		return 0, fmt.Errorf("acknowledge project report batch: %w", err)
	}
	return len(rows), nil
}

// AcknowledgeAcceptedReportBatches settles shown pending reports of a project
// whose delivery batch id matches the recipient's accepted coordination id —
// the durable evidence of a semantic acceptance of that batch, possibly after
// the emit window closed. Batches the checkpoint does not name, never-shown
// reports, and rows claimed by an in-flight delivery attempt stay untouched.
func (s *Store) AcknowledgeAcceptedReportBatches(ctx context.Context, projectID domain.ProjectID, acceptedBatchID string, at time.Time) (int, error) {
	if projectID == "" || acceptedBatchID == "" || at.IsZero() {
		return 0, domain.ErrInvalidReport
	}
	rows, err := s.runDeliveryBatchTransition(ctx, "acknowledge accepted reports", at,
		func(q *gen.Queries) ([]gen.Delivery, error) {
			return q.AcknowledgeAcceptedPendingReports(ctx, gen.AcknowledgeAcceptedPendingReportsParams{
				ProjectID: string(projectID), DeliveryBatchID: acceptedBatchID, AcknowledgedAt: nullTime(at),
			})
		},
		string(domain.ReportPending), string(domain.ReportAcknowledged), domain.DeliveryEventAcknowledged, "")
	if err != nil {
		return 0, fmt.Errorf("acknowledge accepted reports for project %s: %w", projectID, err)
	}
	return len(rows), nil
}

// ReleaseReportBatch returns the matching project batch to pending without
// moving any report's original deadline later.
func (s *Store) ReleaseReportBatch(ctx context.Context, projectID domain.ProjectID, token string, at time.Time, lastError string) (int, error) {
	if projectID == "" || token == "" || at.IsZero() {
		return 0, domain.ErrInvalidReport
	}
	rows, err := s.runDeliveryBatchTransition(ctx, "release project report batch", at,
		func(q *gen.Queries) ([]gen.Delivery, error) {
			return q.ReleaseReportBatch(ctx, gen.ReleaseReportBatchParams{
				ProjectID: string(projectID), ClaimToken: token, AvailableAt: at, LastError: lastError,
			})
		},
		string(domain.ReportClaimed), string(domain.ReportPending), domain.DeliveryEventReleased, lastError)
	if err != nil {
		return 0, fmt.Errorf("release project report batch: %w", err)
	}
	return len(rows), nil
}

// DeferReportBatch coalesces a throttled urgent retry until it may request the
// next interruption. Unlike failure release, this intentionally moves its
// delivery availability later.
func (s *Store) DeferReportBatch(ctx context.Context, projectID domain.ProjectID, token string, at time.Time, lastError string) (int, error) {
	if projectID == "" || token == "" || at.IsZero() {
		return 0, domain.ErrInvalidReport
	}
	rows, err := s.runDeliveryBatchTransition(ctx, "defer project report batch", at,
		func(q *gen.Queries) ([]gen.Delivery, error) {
			return q.DeferReportBatch(ctx, gen.DeferReportBatchParams{
				ProjectID: string(projectID), ClaimToken: token, AvailableAt: at, LastError: lastError,
			})
		},
		string(domain.ReportClaimed), string(domain.ReportPending), domain.DeliveryEventDeferred, lastError)
	if err != nil {
		return 0, fmt.Errorf("defer project report batch: %w", err)
	}
	return len(rows), nil
}

// MarkReportBatchEmitted stamps the claimed batch holding token with the
// moment its content became visible to the client. The stamp fences like the
// ack path and is refreshed on each re-emission past the emit TTL.
func (s *Store) MarkReportBatchEmitted(ctx context.Context, projectID domain.ProjectID, token string, at time.Time) (int, error) {
	if projectID == "" || token == "" || at.IsZero() {
		return 0, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var rows []gen.Delivery
	err := s.inTx(ctx, "mark report batch emitted", func(q *gen.Queries) error {
		var err error
		rows, err = q.MarkReportBatchEmitted(ctx, gen.MarkReportBatchEmittedParams{
			ProjectID: string(projectID), ClaimToken: token, EmittedAt: nullTime(at),
		})
		if err != nil {
			return err
		}
		return journalDeliveryEvents(ctx, q, rows,
			string(domain.ReportClaimed), string(domain.ReportClaimed), domain.DeliveryEventEmitVisible, "", at)
	})
	if err != nil {
		return 0, fmt.Errorf("mark report batch emitted: %w", err)
	}
	return len(rows), nil
}

// SupersedePendingReports retires a worker's undelivered reports terminally
// when a new prompt reaches the session (fork issue #240): the reports
// describe the previous task, and delivering them after a fresh instruction
// would present stale work as current status. Pending and claimed rows both
// retire as stale — the ledger's terminal state — with the reason in
// last_error; a claim released by a failed cycle or restart requeue must not
// come back as a post-prompt delivery. The in-flight attempt's token-fenced
// ack and defer become no-ops, which the coordinator reports as a lost claim.
func (s *Store) SupersedePendingReports(ctx context.Context, sessionID domain.SessionID, reason string) (int, error) {
	if sessionID == "" || reason == "" {
		return 0, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	retired := 0
	err := s.inTx(ctx, "supersede pending reports", func(q *gen.Queries) error {
		// Split by prior state so the journal records the precise from-state
		// instead of an ambiguous "undelivered" for rows that retire from
		// either pending or claimed.
		pending, err := q.SupersedeSessionPendingReports(ctx, gen.SupersedeSessionPendingReportsParams{
			SessionID: string(sessionID), Reason: reason,
		})
		if err != nil {
			return err
		}
		if err := journalDeliveryEvents(ctx, q, pending,
			string(domain.ReportPending), string(domain.ReportStale), domain.DeliveryEventRepromptSuperseded, reason, time.Now().UTC()); err != nil {
			return err
		}
		claimed, err := q.SupersedeSessionClaimedReports(ctx, gen.SupersedeSessionClaimedReportsParams{
			SessionID: string(sessionID), Reason: reason,
		})
		if err != nil {
			return err
		}
		if err := journalDeliveryEvents(ctx, q, claimed,
			string(domain.ReportClaimed), string(domain.ReportStale), domain.DeliveryEventRepromptSuperseded, reason, time.Now().UTC()); err != nil {
			return err
		}
		retired = len(pending) + len(claimed)
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("supersede pending reports for %s: %w", sessionID, err)
	}
	return retired, nil
}

// FailReportBatchTerminally retires the claimed batch holding token as stale
// with the given reason — the delivery has no future attempt: the target held
// the batch text in its message queue (fork issue #205), so retrying would
// re-append the same content forever, or the project was archived (fork issue
// #208) and its report recipient can never exist again. Rows are fenced by the
// claim token like the ack path; a zero count means the claim was lost.
func (s *Store) FailReportBatchTerminally(ctx context.Context, projectID domain.ProjectID, token, lastError string) (int, error) {
	if projectID == "" || token == "" || lastError == "" {
		return 0, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	cause := domain.DeliveryEventInjectFailed
	if strings.Contains(strings.ToLower(lastError), "project archived") {
		// A retired batch whose recipient can never exist again (fork issue
		// #208) is a gone recipient, not a failed injection.
		cause = domain.DeliveryEventRecipientGone
	}
	var rows []gen.Delivery
	err := s.inTx(ctx, "fail queued project report batch", func(q *gen.Queries) error {
		var err error
		rows, err = q.FailClaimedReportBatch(ctx, gen.FailClaimedReportBatchParams{
			ProjectID: string(projectID), ClaimToken: token, LastError: lastError,
		})
		if err != nil {
			return err
		}
		return journalDeliveryEvents(ctx, q, rows,
			string(domain.ReportClaimed), string(domain.ReportStale), cause, lastError, time.Now().UTC())
	})
	if err != nil {
		return 0, fmt.Errorf("fail queued project report batch: %w", err)
	}
	return len(rows), nil
}

// ExpireStaleReportDeliveries is the delivery ledger's terminal expiry: report
// rows created before the cutoff that are still pending stop retrying forever
// and become stale — readable in projections, never claimed again. Claims held
// by an in-flight attempt are untouched: that attempt's own cycle fences them.
func (s *Store) ExpireStaleReportDeliveries(ctx context.Context, cutoff time.Time) (int, error) {
	if cutoff.IsZero() {
		return 0, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var rows []gen.Delivery
	err := s.inTx(ctx, "expire stale report deliveries", func(q *gen.Queries) error {
		var err error
		rows, err = q.ExpireStalePendingReports(ctx, cutoff)
		if err != nil {
			return err
		}
		return journalDeliveryEvents(ctx, q, rows,
			string(domain.ReportPending), string(domain.ReportStale), domain.DeliveryEventExpired, "", time.Now().UTC())
	})
	if err != nil {
		return 0, fmt.Errorf("expire stale report deliveries: %w", err)
	}
	return len(rows), nil
}

// ListPendingReports returns reports eligible for a later delivery batch.
func (s *Store) ListPendingReports(ctx context.Context, at time.Time, limit int64) ([]domain.ReportRecord, error) {
	rows, err := s.qr.ListPendingReports(ctx, gen.ListPendingReportsParams{AvailableAt: at, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list pending reports: %w", err)
	}
	return s.reportsWithOutputs(ctx, rows)
}

// ClaimReport atomically fences one pending report to a delivery attempt token.
func (s *Store) ClaimReport(ctx context.Context, id, token string, at time.Time) (domain.ReportRecord, bool, error) {
	return s.updateReportClaim(ctx, id, token, at, "claim",
		string(domain.ReportPending), string(domain.ReportClaimed), domain.DeliveryEventDeliveryAttempt, "",
		func(q *gen.Queries) (gen.Delivery, error) {
			return q.ClaimReport(ctx, gen.ClaimReportParams{ID: id, ClaimToken: token, ClaimedAt: nullTime(at)})
		})
}

// AcknowledgeReport completes only the delivery attempt holding the current token.
func (s *Store) AcknowledgeReport(ctx context.Context, id, token string, at time.Time) (domain.ReportRecord, bool, error) {
	return s.updateReportClaim(ctx, id, token, at, "acknowledge",
		string(domain.ReportClaimed), string(domain.ReportAcknowledged), domain.DeliveryEventAcknowledged, "",
		func(q *gen.Queries) (gen.Delivery, error) {
			return q.AcknowledgeReport(ctx, gen.AcknowledgeReportParams{ID: id, ClaimToken: token, AcknowledgedAt: nullTime(at)})
		})
}

// ReleaseReport returns the matching token's claim to pending with retry metadata.
func (s *Store) ReleaseReport(ctx context.Context, id, token string, availableAt time.Time, lastError string) (domain.ReportRecord, bool, error) {
	return s.updateReportClaim(ctx, id, token, availableAt, "release",
		string(domain.ReportClaimed), string(domain.ReportPending), domain.DeliveryEventReleased, lastError,
		func(q *gen.Queries) (gen.Delivery, error) {
			return q.ReleaseReport(ctx, gen.ReleaseReportParams{ID: id, ClaimToken: token, AvailableAt: availableAt, LastError: lastError})
		})
}

func (s *Store) updateReportClaim(ctx context.Context, id, token string, at time.Time, action, fromState, toState, cause, reason string, update func(*gen.Queries) (gen.Delivery, error)) (domain.ReportRecord, bool, error) {
	if id == "" || token == "" || at.IsZero() {
		return domain.ReportRecord{}, false, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var row gen.Delivery
	err := s.inTx(ctx, action+" report", func(q *gen.Queries) error {
		var err error
		row, err = update(q)
		if err != nil {
			return err
		}
		return journalDeliveryEvents(ctx, q, []gen.Delivery{row}, fromState, toState, cause, reason, at)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReportRecord{}, false, nil
	}
	if err != nil {
		return domain.ReportRecord{}, false, fmt.Errorf("%s report %s: %w", action, id, err)
	}
	rec, err := s.reportWithOutputs(ctx, row)
	return rec, err == nil, err
}

// RequeueClaimedReports atomically recovers claims owned by a prior daemon process.
func (s *Store) RequeueClaimedReports(ctx context.Context) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var rows []gen.Delivery
	err := s.inTx(ctx, "requeue claimed reports", func(q *gen.Queries) error {
		var err error
		rows, err = q.RequeueClaimedReports(ctx)
		if err != nil {
			return err
		}
		return journalDeliveryEvents(ctx, q, rows,
			string(domain.ReportClaimed), string(domain.ReportPending), domain.DeliveryEventRestartRequeue, "delivery claim recovered after daemon restart", time.Now().UTC())
	})
	if err != nil {
		return 0, fmt.Errorf("requeue claimed reports: %w", err)
	}
	return int64(len(rows)), nil
}

func (s *Store) reportWithOutputs(ctx context.Context, row gen.Delivery) (domain.ReportRecord, error) {
	outputs, err := s.qr.ListReportOutputs(ctx, row.ID)
	if err != nil {
		return domain.ReportRecord{}, fmt.Errorf("list report %s outputs: %w", row.ID, err)
	}
	rec := reportFromGen(row)
	rec.Outputs = make([]domain.ReportOutput, len(outputs))
	for i, output := range outputs {
		rec.Outputs[i] = domain.ReportOutput{Kind: domain.ReportOutputKind(output.Kind), Reference: output.Reference, Label: output.Label}
	}
	return rec, nil
}

func (s *Store) reportsWithOutputs(ctx context.Context, rows []gen.Delivery) ([]domain.ReportRecord, error) {
	out := make([]domain.ReportRecord, 0, len(rows))
	for _, row := range rows {
		rec, err := s.reportWithOutputs(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func orderedReportRows(rows []gen.Delivery) []gen.Delivery {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})
	return rows
}

func reportFromGen(r gen.Delivery) domain.ReportRecord {
	return domain.ReportRecord{
		ID: r.ID, SessionID: domain.SessionID(r.SessionID), ProjectID: domain.ProjectID(r.ProjectID),
		State: domain.ReportState(r.State), Note: r.Note, Message: r.Message, CreatedAt: r.CreatedAt,
		DeliveryState: domain.ReportDeliveryState(r.DeliveryState), AvailableAt: r.AvailableAt,
		SettlementDeadline: timeFromNull(r.SettlementDeadline), RepeatCount: r.RepeatCount,
		ClaimToken: r.ClaimToken, ClaimedAt: timeFromNull(r.ClaimedAt), DeliveryAttempts: r.DeliveryAttempts,
		AcknowledgedAt: timeFromNull(r.AcknowledgedAt), EmittedAt: timeFromNull(r.EmittedAt),
		LastError:       r.LastError,
		DeliveryBatchID: r.DeliveryBatchID,
	}
}

// ListStuckDeliveryCandidates returns the read-only visibility sweep's rows
// (fork issue #211): pending report deliveries past the stuck threshold plus
// every terminal stale row. Outputs are not loaded: grouping needs only the
// delivery facts, and the sweep runs every cycle. Rows are ordered by project,
// created_at, id so a group's first row is its oldest delivery.
func (s *Store) ListStuckDeliveryCandidates(ctx context.Context, minAttempts int64, ageCutoff time.Time) ([]domain.ReportRecord, error) {
	rows, err := s.qr.ListStuckDeliveryCandidates(ctx, gen.ListStuckDeliveryCandidatesParams{
		MinAttempts: minAttempts,
		AgeCutoff:   ageCutoff,
	})
	if err != nil {
		return nil, fmt.Errorf("list stuck delivery candidates: %w", err)
	}
	out := make([]domain.ReportRecord, len(rows))
	for i, row := range rows {
		out[i] = domain.ReportRecord{
			ID: row.ID, SessionID: domain.SessionID(row.SessionID), ProjectID: domain.ProjectID(row.ProjectID),
			DeliveryState: domain.ReportDeliveryState(row.DeliveryState), DeliveryAttempts: row.DeliveryAttempts,
			CreatedAt: row.CreatedAt, LastError: row.LastError,
		}
	}
	return out, nil
}

// journalDeliveryEvents appends one journal row per transitioned delivery.
// Callers run it inside the same transaction as the ledger transition, so a
// journal failure rolls the transition back: no ledger state change can
// escape its journal entry.
func journalDeliveryEvents(ctx context.Context, q *gen.Queries, rows []gen.Delivery, fromState, toState, cause, reason string, at time.Time) error {
	for _, row := range rows {
		if err := q.InsertDeliveryEvent(ctx, gen.InsertDeliveryEventParams{
			DeliveryID: row.ID, SessionID: row.SessionID, ProjectID: row.ProjectID,
			FromState: fromState, ToState: toState, Cause: cause, Reason: reason, CreatedAt: at,
		}); err != nil {
			return fmt.Errorf("journal %s event for delivery %s: %w", cause, row.ID, err)
		}
	}
	return nil
}

func deliveryEventFromGen(r gen.DeliveryEvent) domain.DeliveryEvent {
	return domain.DeliveryEvent{
		ID: r.ID, DeliveryID: r.DeliveryID, SessionID: domain.SessionID(r.SessionID),
		ProjectID: domain.ProjectID(r.ProjectID), FromState: r.FromState, ToState: r.ToState,
		Cause: r.Cause, Reason: r.Reason, CreatedAt: r.CreatedAt,
	}
}

// ListDeliveryEventsByDelivery returns a delivery's full journal chain in id
// order — the audit record of its fate (fork issue #242).
func (s *Store) ListDeliveryEventsByDelivery(ctx context.Context, id string) ([]domain.DeliveryEvent, error) {
	rows, err := s.qr.ListDeliveryEventsByDelivery(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list delivery events for %s: %w", id, err)
	}
	events := make([]domain.DeliveryEvent, len(rows))
	for i, row := range rows {
		events[i] = deliveryEventFromGen(row)
	}
	return events, nil
}

// ListDeliveryEventsSince returns journal events created at or after since
// (zero time reads everything), in id order. This is the metrics read path.
func (s *Store) ListDeliveryEventsSince(ctx context.Context, since time.Time) ([]domain.DeliveryEvent, error) {
	rows, err := s.qr.ListDeliveryEventsSince(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("list delivery events since %v: %w", since, err)
	}
	events := make([]domain.DeliveryEvent, len(rows))
	for i, row := range rows {
		events[i] = deliveryEventFromGen(row)
	}
	return events, nil
}
