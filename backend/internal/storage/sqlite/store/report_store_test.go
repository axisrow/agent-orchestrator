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
		AvailableAt: now.Add(domain.ReportSettlementWindow), RepeatCount: 1,
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
	projects, err := s.ListPendingDeliveryProjects(ctx)
	if err != nil || len(projects) != 1 || projects[0] != sess.ProjectID {
		t.Fatalf("pending projects=%v err=%v", projects, err)
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
}

func TestReportStoreRejectsInvalidRecord(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateReport(context.Background(), domain.ReportRecord{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

// TestReportStoreSupersedesUndeliveredOnNewPrompt pins fork issue #240: a new
// prompt on a worker session retires that session's undelivered reports
// terminally, so the delivery schedule can never present a previous task's
// report as fresh status. Claimed rows retire with the pending ones — a claim
// released by a failed cycle or a restart requeue would otherwise come back
// as a post-prompt delivery — and another session's pending report stays
// untouched. Retired rows keep valid durable shape.
func TestReportStoreSupersedesUndeliveredOnNewPrompt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	held := domain.ReportRecord{ID: "rpt_held", SessionID: sess.ID, ProjectID: sess.ProjectID, Message: "in flight", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, held); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimPendingReportBatch(ctx, sess.ProjectID, "flight-token", now); err != nil {
		t.Fatal(err)
	}
	// Rows created after the claim keep their own (empty) batch: they stay
	// pending for the next drain pass.
	old := domain.ReportRecord{ID: "rpt_old", SessionID: sess.ID, ProjectID: sess.ProjectID, Message: "phase-1 assessment done", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, old); err != nil {
		t.Fatal(err)
	}
	elsewhere := domain.ReportRecord{ID: "rpt_other", SessionID: other.ID, ProjectID: sess.ProjectID, Message: "another worker", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, elsewhere); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SupersedePendingReports(ctx, sess.ID, ""); err == nil {
		t.Fatal("expected the empty reason to be refused")
	}
	n, err := s.SupersedePendingReports(ctx, sess.ID, domain.ReportSupersededReason)
	if err != nil || n != 2 {
		t.Fatalf("retired=%d err=%v, want both of the session's undelivered reports", n, err)
	}
	for _, id := range []string{old.ID, held.ID} {
		got, ok, err := s.GetReport(ctx, id)
		if err != nil || !ok {
			t.Fatalf("%s ok=%v err=%v", id, ok, err)
		}
		if got.DeliveryState != domain.ReportStale || got.ClaimToken != "" || !got.ClaimedAt.IsZero() || got.LastError != domain.ReportSupersededReason {
			t.Fatalf("%s = %+v, want stale with the supersession reason and no claim", id, got)
		}
		if err = got.Validate(); err != nil {
			t.Fatalf("%s violates durable invariants: %v", id, err)
		}
	}
	if got, _, err := s.GetReport(ctx, elsewhere.ID); err != nil || got.DeliveryState != domain.ReportPending {
		t.Fatalf("other session row = %+v err=%v, want pending", got, err)
	}
	projects, err := s.ListPendingDeliveryProjects(ctx)
	if err != nil || len(projects) != 1 || projects[0] != sess.ProjectID {
		t.Fatalf("pending projects=%v err=%v, want only the other session's project", projects, err)
	}
}

// TestReportStoreTerminationRetiresWithSourceCause pins the fork issue #279
// terminate leg at the ledger: SupersedeSessionReports retires a session's
// pending and claimed rows terminally and journals the source_terminated
// cause class — a terminated source's fate is distinguishable in the journal
// from a prompt supersession, with no pending rows left for the drain.
func TestReportStoreTerminationRetiresWithSourceCause(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	pending := domain.ReportRecord{ID: "rpt_term_p", SessionID: sess.ID, ProjectID: sess.ProjectID, State: domain.ReportNeedsInput, Note: "waiting", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimPendingReportBatch(ctx, sess.ProjectID, "term-token", now); err != nil {
		t.Fatal(err)
	}
	n, err := s.SupersedeSessionReports(ctx, sess.ID, domain.DeliveryEventSourceTerminated, domain.ReportTerminatedReason)
	if err != nil || n != 1 {
		t.Fatalf("retired=%d err=%v, want the claimed report retired", n, err)
	}
	got, ok, err := s.GetReport(ctx, pending.ID)
	if err != nil || !ok {
		t.Fatalf("get retired report ok=%v err=%v", ok, err)
	}
	if got.DeliveryState != domain.ReportStale || got.LastError != domain.ReportTerminatedReason {
		t.Fatalf("row = %+v, want stale with the termination reason", got)
	}
	if err = got.Validate(); err != nil {
		t.Fatalf("retired row violates invariants: %v", got)
	}
	events, err := s.ListDeliveryEventsByDelivery(ctx, pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.ToState != string(domain.ReportStale) || last.Cause != domain.DeliveryEventSourceTerminated || last.Reason != domain.ReportTerminatedReason {
		t.Fatalf("terminal journal event = %+v, want stale/source_terminated/termination reason", last)
	}
	if projects, err := s.ListPendingDeliveryProjects(ctx); err != nil || len(projects) != 0 {
		t.Fatalf("pending projects=%v err=%v, want none", projects, err)
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
	if n, err := s.FailReportBatchTerminally(ctx, sess.ProjectID, "wrong", domain.DeliveryEventInjectFailed, "reason"); err != nil || n != 0 {
		t.Fatalf("wrong token failed %d rows, err=%v", n, err)
	}
	reason := "session: prompt text is held in the target's message queue"
	if n, err := s.FailReportBatchTerminally(ctx, sess.ProjectID, "queue-token", domain.DeliveryEventInjectFailed, reason); err != nil || n != 1 {
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
	if _, err := s.ClaimPendingReportBatch(ctx, sess.ProjectID, "old-token", now); err != nil {
		t.Fatalf("claim ok err=%v", err)
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
	if n, err := s.AcknowledgeReportBatch(ctx, sess.ProjectID, "old-token", now.Add(time.Second)); err != nil || n != 0 {
		t.Fatalf("stale acknowledge n=%d err=%v", n, err)
	}
	claimed, err := s.ClaimPendingReportBatch(ctx, sess.ProjectID, "new-token", now.Add(2*time.Second))
	if err != nil || len(claimed) != 1 || claimed[0].DeliveryAttempts != 2 {
		t.Fatalf("reclaim=%+v err=%v", claimed, err)
	}
	if n, err := s.AcknowledgeReportBatch(ctx, sess.ProjectID, "old-token", now.Add(3*time.Second)); err != nil || n != 0 {
		t.Fatalf("old token crossed fence n=%d err=%v", n, err)
	}
	if n, err := s.AcknowledgeReportBatch(ctx, sess.ProjectID, "new-token", now.Add(4*time.Second)); err != nil || n != 1 {
		t.Fatalf("new acknowledge n=%d err=%v", n, err)
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

// TestReportStoreExpiresStalePendingReports pins the ledger expiry: unread
// report rows created before the cutoff retire as stale with a readable cause,
// fresh pending rows and live claims are untouched, and the drain pass input
// drops the expired project rows accordingly.
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
	// A live claim rides out its own attempt even when old.
	create("rpt_claimed_fresh", now)
	if _, err := s.ClaimPendingReportBatch(ctx, "ao", "hold", now); err != nil {
		t.Fatal(err)
	}
	create("rpt_old", now.Add(-8*24*time.Hour))
	create("rpt_fresh", now)

	expired, err := s.ExpireStaleReportDeliveries(ctx, now.Add(-7*24*time.Hour))
	if err != nil || expired != 1 {
		t.Fatalf("expired = %d err = %v, want 1", expired, err)
	}
	got, ok, err := s.GetReport(ctx, "rpt_old")
	if err != nil || !ok || got.DeliveryState != domain.ReportStale || got.ClaimToken != "" || got.LastError == "" {
		t.Fatalf("old = %+v ok=%v err=%v, want stale with the readable expiry cause", got, ok, err)
	}
	if got, _, err = s.GetReport(ctx, "rpt_fresh"); err != nil || got.DeliveryState != domain.ReportPending {
		t.Fatalf("fresh = %+v err=%v, want pending untouched", got, err)
	}
	if got, _, err = s.GetReport(ctx, "rpt_claimed_fresh"); err != nil || got.DeliveryState != domain.ReportClaimed || got.ClaimToken != "hold" {
		t.Fatalf("claimed = %+v err=%v, want the live claim intact", got, err)
	}
	projects, err := s.ListPendingDeliveryProjects(ctx)
	if err != nil || len(projects) != 1 || projects[0] != "ao" {
		t.Fatalf("pending projects = %v err=%v", projects, err)
	}
}

func TestReportStoreListsStuckDeliveryCandidates(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	defer s.Close()
	seedProject(t, s, "ao")
	seedProject(t, s, "st")
	aoSess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	stSess := sampleRecord("st")
	stSess.Kind = domain.KindWorker
	stSess.ID = ""
	stSess, err = s.CreateSession(ctx, stSess)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	// Stale path: an old delivery of an archived project retires terminally and
	// must show whatever its age.
	old := domain.ReportRecord{ID: "rpt_old", SessionID: stSess.ID, ProjectID: stSess.ProjectID, Message: "old", CreatedAt: now.Add(-2 * time.Hour), DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}
	// Attempts path: a fresh delivery that keeps failing climbs past the
	// threshold without ever qualifying by age.
	fresh := domain.ReportRecord{ID: "rpt_fresh", SessionID: aoSess.ID, ProjectID: aoSess.ProjectID, Message: "fresh", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}
	for _, rec := range []domain.ReportRecord{old, fresh} {
		if _, err := s.CreateReport(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ClaimPendingReportBatch(ctx, stSess.ProjectID, "tok-old", now); err != nil {
		t.Fatal(err)
	}
	if n, err := s.FailReportBatchTerminally(ctx, stSess.ProjectID, "tok-old", domain.DeliveryEventRecipientGone, "project archived: no report recipient can exist (fork issue #208)"); err != nil || n == 0 {
		t.Fatalf("terminal fail n=%d err=%v", n, err)
	}
	for i := 0; i < 10; i++ {
		if _, err := s.ClaimPendingReportBatch(ctx, aoSess.ProjectID, "tok-fresh", now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReleaseReportBatch(ctx, aoSess.ProjectID, "tok-fresh", now, "retry"); err != nil {
			t.Fatal(err)
		}
	}
	// A claim in flight is undelivered work: the sweep must keep seeing the
	// past-threshold group while its claim is held, or a mid-delivery pass
	// would resolve the open incident and its source key would never re-open.
	if _, err := s.ClaimPendingReportBatch(ctx, aoSess.ProjectID, "tok-fresh", now); err != nil {
		t.Fatal(err)
	}
	inFlight, err := s.ListStuckDeliveryCandidates(ctx, 10, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(inFlight) != 2 || inFlight[0].ID != "rpt_fresh" || inFlight[0].DeliveryState != domain.ReportClaimed {
		t.Fatalf("in-flight candidates = %+v, want the claimed rpt_fresh first", inFlight)
	}
	if _, err := s.ReleaseReportBatch(ctx, aoSess.ProjectID, "tok-fresh", now, "retry"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListStuckDeliveryCandidates(ctx, 10, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	if len(ids) != 2 || ids[0] != "rpt_fresh" || ids[1] != "rpt_old" {
		t.Fatalf("candidate ids = %v, want [rpt_fresh rpt_old]", ids)
	}
	if rows[1].DeliveryState != domain.ReportStale || rows[1].LastError == "" {
		t.Fatalf("stale row = %+v", rows[1])
	}
	if rows[0].DeliveryAttempts < 10 {
		t.Fatalf("fresh row attempts = %d, want >= 10", rows[0].DeliveryAttempts)
	}
}
