package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestReportStorePersistsOutputsAndDeadlinesAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	want := domain.ReportRecord{
		ID: "rpt_1", SessionID: sess.ID, ProjectID: sess.ProjectID,
		Outputs: []domain.ReportOutput{
			{Kind: domain.ReportOutputArtifact, Reference: "opaque-ref", Label: "Result"},
			{Kind: domain.ReportOutputArtifact, Reference: "second"},
			{Kind: domain.ReportOutputPRCreated, Reference: "https://github.com/o/r/pull/1"},
		},
		CreatedAt: now, DeliveryState: domain.ReportPending,
		AvailableAt: now.Add(domain.ReportBatchFallback), RepeatCount: 1,
	}
	if _, err = s.CreateReport(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, ok, err := s.GetReport(ctx, want.ID)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.SessionID != sess.ID || got.ProjectID != sess.ProjectID || len(got.Outputs) != 3 || got.Outputs[0].Label != "Result" || got.Outputs[1].Reference != "second" || got.Outputs[2].Kind != domain.ReportOutputPRCreated || !got.AvailableAt.Equal(want.AvailableAt) {
		t.Fatalf("got=%+v", got)
	}
	if pending, err := s.ListPendingReports(ctx, now, 10); err != nil || len(pending) != 0 {
		t.Fatalf("early pending=%+v err=%v", pending, err)
	}
	urls, err := s.ListReportedPRURLs(ctx, sess.ID)
	if err != nil || len(urls) != 1 || urls[0] != "https://github.com/o/r/pull/1" {
		t.Fatalf("reported PR URLs=%v err=%v", urls, err)
	}
	events, err := s.EventsAfter(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var reportEvents int
	for _, event := range events {
		if event.SessionID == string(sess.ID) && event.Type == "session_updated" {
			reportEvents++
		}
	}
	if reportEvents != 1 {
		t.Fatalf("reported PR CDC events=%d, want 1", reportEvents)
	}
	pending, err := s.ListPendingReports(ctx, want.AvailableAt, 10)
	if err != nil || len(pending) != 1 || pending[0].ID != want.ID || len(pending[0].Outputs) != 3 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
}

func TestReportStoreRejectsInvalidRecord(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateReport(context.Background(), domain.ReportRecord{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestReportStoreClaimAcknowledgeAndTokenFence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.ReportRecord{ID: "rpt_lease", SessionID: sess.ID, ProjectID: sess.ProjectID, State: domain.ReportDone, Note: "done", CreatedAt: now.Add(-domain.ReportSettlementWindow), DeliveryState: domain.ReportPending, AvailableAt: now, SettlementDeadline: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, rec); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := s.ClaimReport(ctx, rec.ID, "lease-1", now)
	if err != nil || !ok || claimed.DeliveryAttempts != 1 || claimed.DeliveryState != domain.ReportClaimed {
		t.Fatalf("claimed=%+v ok=%v err=%v", claimed, ok, err)
	}
	if _, ok, err = s.ClaimReport(ctx, rec.ID, "lease-2", now); err != nil || ok {
		t.Fatalf("second claim ok=%v err=%v", ok, err)
	}
	if _, ok, err = s.AcknowledgeReport(ctx, rec.ID, "wrong", now.Add(time.Second)); err != nil || ok {
		t.Fatalf("wrong ack ok=%v err=%v", ok, err)
	}
	acked, ok, err := s.AcknowledgeReport(ctx, rec.ID, "lease-1", now.Add(time.Second))
	if err != nil || !ok || acked.DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("acked=%+v ok=%v err=%v", acked, ok, err)
	}
}

func TestReportStoreMarksBatchEmittedWithTokenFence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.ReportRecord{ID: "rpt_emit", SessionID: sess.ID, ProjectID: sess.ProjectID, State: domain.ReportNeedsInput, Note: "need", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, rec); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimPendingReportBatch(ctx, sess.ProjectID, "emit-token", now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	if n, err := s.MarkReportBatchEmitted(ctx, sess.ProjectID, "wrong", now.Add(time.Second)); err != nil || n != 0 {
		t.Fatalf("wrong token marked %d rows, err=%v", n, err)
	}
	if n, err := s.MarkReportBatchEmitted(ctx, sess.ProjectID, "emit-token", now.Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("marked=%d err=%v", n, err)
	}
	got, ok, err := s.GetReport(ctx, rec.ID)
	if err != nil || !ok || !got.EmittedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("got=%+v ok=%v err=%v", got, ok, err)
	}
	// A re-emission past the emit TTL refreshes the marker under the same token.
	if n, err := s.MarkReportBatchEmitted(ctx, sess.ProjectID, "emit-token", now.Add(2*time.Second)); err != nil || n != 1 {
		t.Fatalf("re-marked=%d err=%v", n, err)
	}
	if got, _, err = s.GetReport(ctx, rec.ID); err != nil || !got.EmittedAt.Equal(now.Add(2*time.Second)) {
		t.Fatalf("refreshed=%+v err=%v", got, err)
	}
}

// TestReportStoreFailsQueuedBatchTerminally pins the fork issue #205 ledger
// outcome: a batch whose target held its text in the message queue is retired
// stale under its claim token — never pending again — and a wrong token fails
// no rows.
func TestReportStoreFailsQueuedBatchTerminally(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.ReportRecord{ID: "rpt_queued", SessionID: sess.ID, ProjectID: sess.ProjectID, State: domain.ReportCheckpoint, Note: "context", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimPendingReportBatch(ctx, sess.ProjectID, "queue-token", now); err != nil {
		t.Fatal(err)
	}
	if n, err := s.FailReportBatchQueued(ctx, sess.ProjectID, "wrong", "reason"); err != nil || n != 0 {
		t.Fatalf("wrong token failed %d rows, err=%v", n, err)
	}
	reason := "session: prompt text is held in the target's message queue"
	if n, err := s.FailReportBatchQueued(ctx, sess.ProjectID, "queue-token", reason); err != nil || n != 1 {
		t.Fatalf("failed=%d err=%v", n, err)
	}
	got, ok, err := s.GetReport(ctx, rec.ID)
	if err != nil || !ok || got.DeliveryState != domain.ReportStale || got.LastError != reason || got.ClaimToken != "" {
		t.Fatalf("got=%+v ok=%v err=%v", got, ok, err)
	}
}

func TestReportStoreRequeuesInterruptedClaimOnRestartAndFencesOldToken(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.ReportRecord{ID: "rpt_restart", SessionID: sess.ID, ProjectID: sess.ProjectID, Message: "information", CreatedAt: now.Add(-time.Hour), DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimReport(ctx, rec.ID, "old-token", now); err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if count, err := s.RequeueClaimedReports(ctx); err != nil || count != 1 {
		t.Fatalf("restart recovery count=%d err=%v", count, err)
	}
	recovered, ok, err := s.GetReport(ctx, rec.ID)
	if err != nil || !ok || recovered.DeliveryState != domain.ReportPending || recovered.ClaimToken != "" || !recovered.ClaimedAt.IsZero() || recovered.DeliveryAttempts != 1 || !strings.Contains(recovered.LastError, "restart") {
		t.Fatalf("recovered=%+v ok=%v err=%v", recovered, ok, err)
	}
	if _, ok, err := s.AcknowledgeReport(ctx, rec.ID, "old-token", now.Add(time.Second)); err != nil || ok {
		t.Fatalf("stale acknowledge ok=%v err=%v", ok, err)
	}
	claimed, ok, err := s.ClaimReport(ctx, rec.ID, "new-token", now.Add(2*time.Second))
	if err != nil || !ok || claimed.DeliveryAttempts != 2 {
		t.Fatalf("reclaim=%+v ok=%v err=%v", claimed, ok, err)
	}
	if _, ok, err := s.AcknowledgeReport(ctx, rec.ID, "old-token", now.Add(3*time.Second)); err != nil || ok {
		t.Fatalf("old token crossed fence ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.AcknowledgeReport(ctx, rec.ID, "new-token", now.Add(4*time.Second)); err != nil || !ok {
		t.Fatalf("new acknowledge ok=%v err=%v", ok, err)
	}
}

func TestReportStoreDurableBatchIdentityExcludesLaterReportsOnRetry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	create := func(id string) {
		t.Helper()
		_, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: "ao", Message: id, CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	create("first")
	create("second")
	claimed, err := s.ClaimPendingReportBatch(ctx, "ao", "token-1", now)
	if err != nil || len(claimed) != 2 || claimed[0].DeliveryBatchID == "" || claimed[0].DeliveryBatchID != claimed[1].DeliveryBatchID {
		t.Fatalf("claimed = %+v, err = %v", claimed, err)
	}
	batchID := claimed[0].DeliveryBatchID
	if _, err := s.ReleaseReportBatch(ctx, "ao", "token-1", now, "accepted response lost"); err != nil {
		t.Fatal(err)
	}
	create("later")
	retried, err := s.ClaimPendingReportBatch(ctx, "ao", "token-2", now)
	if err != nil || len(retried) != 2 || retried[0].DeliveryBatchID != batchID {
		t.Fatalf("retried = %+v, err = %v", retried, err)
	}
	if _, err := s.AcknowledgeReportBatch(ctx, "ao", "token-2", now); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimPendingReportBatch(ctx, "ao", "token-3", now)
	if err != nil || len(next) != 1 || next[0].ID != "later" || next[0].DeliveryBatchID == batchID {
		t.Fatalf("next = %+v, err = %v", next, err)
	}
}

// TestReportStoreSettlesOnlyAcceptedBatches pins the acceptance-gated settle:
// only the shown pending batch whose delivery batch id matches the accepted
// correlation id settles; a mismatched id settles nothing (a truncated tail
// credits no batch id, fork issue #179), a never-shown report and a batch
// held by a live claim keep their state.
func TestReportStoreSettlesOnlyAcceptedBatches(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	shown := domain.ReportRecord{ID: "rpt_shown", SessionID: sess.ID, ProjectID: "ao", Message: "shown", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, shown); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimPendingReportBatch(ctx, "ao", "emit-token", now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	batchID := claimed[0].DeliveryBatchID
	if _, err := s.MarkReportBatchEmitted(ctx, "ao", "emit-token", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReleaseReportBatch(ctx, "ao", "emit-token", now, "acceptance lost"); err != nil {
		t.Fatal(err)
	}
	// A later report that has never been shown, still pending.
	unshown := domain.ReportRecord{ID: "rpt_unshown", SessionID: sess.ID, ProjectID: "ao", Message: "unshown", CreatedAt: now.Add(time.Second), DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, unshown); err != nil {
		t.Fatal(err)
	}
	// An acceptance naming a different id — a session send, or a truncated
	// tail that carried no opening tag and credits no batch — settles nothing.
	if n, err := s.AcknowledgeAcceptedReportBatches(ctx, "ao", "session-send:other", now.Add(time.Second)); err != nil || n != 0 {
		t.Fatalf("mismatched settle=%d err=%v, want 0", n, err)
	}
	if n, err := s.AcknowledgeAcceptedReportBatches(ctx, "ao", batchID, now.Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("settled=%d err=%v, want 1", n, err)
	}
	got, ok, err := s.GetReport(ctx, shown.ID)
	if err != nil || !ok || got.DeliveryState != domain.ReportAcknowledged || got.AcknowledgedAt.IsZero() || got.ClaimToken != "activity-ack" {
		t.Fatalf("shown=%+v ok=%v err=%v, want acknowledged", got, ok, err)
	}
	if got, ok, err = s.GetReport(ctx, unshown.ID); err != nil || !ok || got.DeliveryState != domain.ReportPending {
		t.Fatalf("unshown=%+v ok=%v err=%v, want pending", got, ok, err)
	}
	// A live claim is never touched by the acceptance settle.
	next, err := s.ClaimPendingReportBatch(ctx, "ao", "hold-token", now.Add(2*time.Second))
	if err != nil || len(next) != 1 || next[0].ID != unshown.ID {
		t.Fatalf("claimed=%+v err=%v", next, err)
	}
	if n, err := s.AcknowledgeAcceptedReportBatches(ctx, "ao", batchID, now.Add(3*time.Second)); err != nil || n != 0 {
		t.Fatalf("settled=%d err=%v, want 0 while the batch holds a claim", n, err)
	}
	if got, _, err = s.GetReport(ctx, unshown.ID); err != nil || got.DeliveryState != domain.ReportClaimed || got.ClaimToken != "hold-token" {
		t.Fatalf("held=%+v err=%v, want the claim intact", got, err)
	}
}

// TestReportStoreExpiresStalePendingReports pins the ledger expiry: report
// rows created before the cutoff stop retrying forever (stale), fresh pending
// rows and live claims are untouched, and stale rows leave the schedule.
func TestReportStoreExpiresStalePendingReports(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	create := func(id string, createdAt time.Time) {
		t.Helper()
		_, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: "ao", Message: id, CreatedAt: createdAt, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	create("rpt_old", now.Add(-8*24*time.Hour))
	create("rpt_fresh", now)
	// A live claim rides out its own attempt even when old.
	if _, ok, err := s.ClaimReport(ctx, "rpt_old", "hold", now); err != nil || !ok {
		t.Fatalf("claim old ok=%v err=%v", ok, err)
	}
	create("rpt_claimed_fresh", now)
	if _, ok, err := s.ClaimReport(ctx, "rpt_fresh", "fresh-tok", now); err != nil || !ok {
		t.Fatalf("claim fresh ok=%v err=%v", ok, err)
	}

	expired, err := s.ExpireStaleReportDeliveries(ctx, now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if expired != 0 {
		t.Fatalf("expired = %d, want 0: claims must survive the cutoff", expired)
	}
	if _, _, err := s.ReleaseReport(ctx, "rpt_old", "hold", now, "attempt lost"); err != nil {
		t.Fatal(err)
	}
	expired, err = s.ExpireStaleReportDeliveries(ctx, now.Add(-7*24*time.Hour))
	if err != nil || expired != 1 {
		t.Fatalf("expired = %d err = %v, want 1 after the claim released", expired, err)
	}
	got, ok, err := s.GetReport(ctx, "rpt_old")
	if err != nil || !ok || got.DeliveryState != domain.ReportStale || got.ClaimToken != "" {
		t.Fatalf("old = %+v ok=%v err=%v, want stale without a claim", got, ok, err)
	}
	if got, _, err = s.GetReport(ctx, "rpt_fresh"); err != nil || got.DeliveryState != domain.ReportClaimed {
		t.Fatalf("fresh = %+v err=%v, want claimed untouched", got, err)
	}
	if pending, err := s.ListPendingReports(ctx, now, 10); err != nil || len(pending) != 1 || pending[0].ID != "rpt_claimed_fresh" {
		t.Fatalf("pending = %+v err=%v, want only the fresh unclaimed row", pending, err)
	}
}

func TestReportStoreDeferredBatchYieldsScheduleToOtherProjects(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	seedProject(t, s, "other")
	refusing, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	healthy, err := s.CreateSession(ctx, sampleRecord("other"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, rec := range []domain.ReportRecord{
		{ID: "rpt_refused", SessionID: refusing.ID, ProjectID: "ao", Message: "stuck", CreatedAt: now.Add(-time.Minute), DeliveryState: domain.ReportPending, AvailableAt: now.Add(-time.Minute), RepeatCount: 1},
		{ID: "rpt_waiting", SessionID: healthy.ID, ProjectID: "other", Message: "done", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1},
	} {
		if _, err := s.CreateReport(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if claimed, err := s.ClaimPendingReportBatch(ctx, "ao", "token-1", now); err != nil || len(claimed) != 1 {
		t.Fatalf("claimed = %+v, err = %v", claimed, err)
	}
	if _, err := s.DeferReportBatch(ctx, "ao", "token-1", now.Add(5*time.Second), "composer is not empty"); err != nil {
		t.Fatal(err)
	}
	next, err := s.ListPendingReportSchedule(ctx, 1)
	if err != nil || len(next) != 1 || next[0].ID != "rpt_waiting" {
		t.Fatalf("next = %+v, err = %v; the refused batch kept the front of the schedule", next, err)
	}
	refused, ok, err := s.GetReport(ctx, "rpt_refused")
	if err != nil || !ok || refused.DeliveryState != domain.ReportPending || refused.LastError != "composer is not empty" || !refused.AvailableAt.Equal(now.Add(5*time.Second)) || refused.DeliveryBatchID == "" {
		t.Fatalf("refused = %+v ok=%v err=%v", refused, ok, err)
	}
}
