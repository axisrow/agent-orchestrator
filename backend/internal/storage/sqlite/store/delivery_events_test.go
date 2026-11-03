package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// journalChain reads a delivery's journal chain as compact "cause from→to"
// strings in id order — the fate-reconstruction read path the audit contract
// promises (fork issue #242).
func journalChain(t *testing.T, s *sqlite.Store, id string) []string {
	t.Helper()
	events, err := s.ListDeliveryEventsByDelivery(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Cause+" "+event.FromState+"→"+event.ToState)
	}
	return out
}

func journalRecord(id string, session domain.SessionID, project domain.ProjectID, now time.Time) domain.ReportRecord {
	return domain.ReportRecord{
		ID: id, SessionID: session, ProjectID: project,
		State: domain.ReportDone, Note: "finished",
		CreatedAt: now, DeliveryState: domain.ReportPending,
		AvailableAt:        now.Add(domain.ReportSettlementWindow),
		SettlementDeadline: now.Add(domain.ReportSettlementWindow), RepeatCount: 1,
	}
}

// TestDeliveryJournalRecordsTheDeliveryChain walks a report through the
// production batch API and asserts the journal holds its full chain with the
// precise from/to states, and that the windowed metrics read filters by time.
func TestDeliveryJournalRecordsTheDeliveryChain(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := journalRecord("rpt_chain", sess.ID, sess.ProjectID, now)
	if _, err = s.CreateReport(ctx, rec); err != nil {
		t.Fatal(err)
	}
	chain := journalChain(t, s, "rpt_chain")
	if len(chain) != 1 || chain[0] != "emitted →pending" {
		t.Fatalf("chain after create = %v", chain)
	}

	claimed, err := s.ClaimPendingReportBatch(ctx, sess.ProjectID, "tok-1", now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %d reports err=%v", len(claimed), err)
	}
	// The visible emission and its suppression backoff are journal transitions
	// too: emit_visible stamps the at-most-once marker, deferred parks the
	// claim back to pending with the suppression reason.
	if n, err := s.MarkReportBatchEmitted(ctx, sess.ProjectID, "tok-1", now); err != nil || n != 1 {
		t.Fatalf("mark emitted = %d err=%v", n, err)
	}
	if n, err := s.DeferReportBatch(ctx, sess.ProjectID, "tok-1", now.Add(5*time.Second), "report batch already emitted; repeat suppressed until emit TTL"); err != nil || n != 1 {
		t.Fatalf("defer = %d err=%v", n, err)
	}
	if _, err = s.ClaimPendingReportBatch(ctx, sess.ProjectID, "tok-2", now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcknowledgeReportBatch(ctx, sess.ProjectID, "tok-2", now.Add(11*time.Second)); err != nil {
		t.Fatal(err)
	}
	chain = journalChain(t, s, "rpt_chain")
	want := []string{
		"emitted →pending",
		"delivery_attempt pending→claimed",
		"emit_visible claimed→claimed",
		"deferred claimed→pending",
		"delivery_attempt pending→claimed",
		"acknowledged claimed→acknowledged",
	}
	if len(chain) != len(want) {
		t.Fatalf("chain = %v, want %v", chain, want)
	}
	for i := range want {
		if chain[i] != want[i] {
			t.Fatalf("chain[%d] = %q, want %q", i, chain[i], want[i])
		}
	}
	events, err := s.ListDeliveryEventsByDelivery(ctx, "rpt_chain")
	if err != nil || events[3].Reason != "report batch already emitted; repeat suppressed until emit TTL" {
		t.Fatalf("deferred reason = %+v err=%v", events, err)
	}

	all, err := s.ListDeliveryEventsSince(ctx, time.Time{})
	if err != nil || len(all) != 6 {
		t.Fatalf("since zero = %d events err=%v", len(all), err)
	}
	later, err := s.ListDeliveryEventsSince(ctx, now.Add(12*time.Second))
	if err != nil || len(later) != 0 {
		t.Fatalf("since +12s = %d events err=%v", len(later), err)
	}
	if all[0].DeliveryID != "rpt_chain" || all[0].SessionID != sess.ID || all[0].ProjectID != sess.ProjectID {
		t.Fatalf("event identity = %+v", all[0])
	}
}

// TestDeliveryJournalTerminalOutcomesCarryCauses retires reports through every
// terminal path and asserts each journal chain ends with the precise cause:
// a terminal outcome never reads as "just disappeared".
func TestDeliveryJournalTerminalOutcomesCarryCauses(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	expired := journalRecord("rpt_expired", sess.ID, sess.ProjectID, now.Add(-8*24*time.Hour))
	if _, err = s.CreateReport(ctx, expired); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ExpireStaleReportDeliveries(ctx, now.Add(-7*24*time.Hour)); err != nil || n != 1 {
		t.Fatalf("expire = %d err=%v", n, err)
	}
	chain := journalChain(t, s, "rpt_expired")
	if len(chain) != 2 || chain[1] != "expired pending→stale" {
		t.Fatalf("expired chain = %v", chain)
	}

	superseded := journalRecord("rpt_superseded", sess.ID, sess.ProjectID, now)
	if _, err = s.CreateReport(ctx, superseded); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SupersedePendingReports(ctx, sess.ID, domain.ReportSupersededReason); err != nil || n != 1 {
		t.Fatalf("supersede = %d err=%v", n, err)
	}
	chain = journalChain(t, s, "rpt_superseded")
	if len(chain) != 2 || chain[1] != "reprompt_superseded pending→stale" {
		t.Fatalf("superseded chain = %v", chain)
	}
	events, err := s.ListDeliveryEventsByDelivery(ctx, "rpt_superseded")
	if err != nil || events[1].Reason != domain.ReportSupersededReason {
		t.Fatalf("superseded reason = %+v err=%v", events, err)
	}

	claimed := journalRecord("rpt_gone", sess.ID, sess.ProjectID, now)
	claimed.AvailableAt = now
	claimed.SettlementDeadline = time.Time{}
	claimed.State = ""
	claimed.Note = ""
	claimed.Message = "context spill"
	if _, err = s.CreateReport(ctx, claimed); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimReport(ctx, "rpt_gone", "tok-gone", now); err != nil || !ok {
		t.Fatalf("claim gone ok=%v err=%v", ok, err)
	}
	if n, err := s.FailReportBatchTerminally(ctx, sess.ProjectID, "tok-gone", domain.DeliveryEventRecipientGone, "project archived: no report recipient can exist (fork issue #208)"); err != nil || n != 1 {
		t.Fatalf("fail archived = %d err=%v", n, err)
	}
	chain = journalChain(t, s, "rpt_gone")
	if len(chain) != 3 || chain[2] != "recipient_gone claimed→stale" {
		t.Fatalf("recipient-gone chain = %v", chain)
	}

	queued := journalRecord("rpt_queued", sess.ID, sess.ProjectID, now)
	queued.AvailableAt = now
	queued.SettlementDeadline = time.Time{}
	queued.State = ""
	queued.Note = ""
	queued.Message = "context spill"
	if _, err = s.CreateReport(ctx, queued); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimReport(ctx, "rpt_queued", "tok-queued", now); err != nil || !ok {
		t.Fatalf("claim queued ok=%v err=%v", ok, err)
	}
	if n, err := s.FailReportBatchTerminally(ctx, sess.ProjectID, "tok-queued", domain.DeliveryEventInjectFailed, "the prompt text is held in the target's message queue; flush the queue before delivering"); err != nil || n != 1 {
		t.Fatalf("fail queued = %d err=%v", n, err)
	}
	chain = journalChain(t, s, "rpt_queued")
	if len(chain) != 3 || chain[2] != "inject_failed claimed→stale" {
		t.Fatalf("inject-failed chain = %v", chain)
	}
	events, err = s.ListDeliveryEventsByDelivery(ctx, "rpt_queued")
	if err != nil || events[2].Reason == "" {
		t.Fatalf("inject-failed reason = %+v err=%v", events, err)
	}

	deferred := journalRecord("rpt_deferred", sess.ID, sess.ProjectID, now)
	deferred.AvailableAt = now
	deferred.SettlementDeadline = time.Time{}
	deferred.State = ""
	deferred.Note = ""
	deferred.Message = "context spill"
	if _, err = s.CreateReport(ctx, deferred); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimReport(ctx, "rpt_deferred", "tok-deferred", now); err != nil || !ok {
		t.Fatalf("claim deferred ok=%v err=%v", ok, err)
	}
	if _, _, err = s.ReleaseReport(ctx, "rpt_deferred", "tok-deferred", now.Add(time.Minute), "boom"); err != nil {
		t.Fatal(err)
	}
	chain = journalChain(t, s, "rpt_deferred")
	if len(chain) != 3 || chain[1] != "delivery_attempt pending→claimed" || chain[2] != "released claimed→pending" {
		t.Fatalf("released chain = %v", chain)
	}
	events, err = s.ListDeliveryEventsByDelivery(ctx, "rpt_deferred")
	if err != nil || events[2].Reason != "boom" {
		t.Fatalf("released reason = %+v err=%v", events, err)
	}
}

// TestDeliveryJournalRecordsRestartRequeue recovers a daemon-restart claim and
// asserts the requeue is journaled per row, not silent.
func TestDeliveryJournalRecordsRestartRequeue(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := journalRecord("rpt_requeue", sess.ID, sess.ProjectID, now)
	rec.AvailableAt = now
	rec.SettlementDeadline = time.Time{}
	rec.State = ""
	rec.Note = ""
	rec.Message = "context spill"
	if _, err = s.CreateReport(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimReport(ctx, "rpt_requeue", "tok-restart", now); err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if n, err := s.RequeueClaimedReports(ctx); err != nil || n != 1 {
		t.Fatalf("requeue = %d err=%v", n, err)
	}
	chain := journalChain(t, s, "rpt_requeue")
	if len(chain) != 3 || chain[2] != "restart_requeue claimed→pending" {
		t.Fatalf("requeue chain = %v", chain)
	}
}
