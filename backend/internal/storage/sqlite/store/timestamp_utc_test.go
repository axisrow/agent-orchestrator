package store_test

// Regression coverage for the remaining issue #383 write paths: every
// caller-supplied instant must survive a write→read round trip even when it
// decodes into a nameless fixed zone (a client offset foreign to the daemon
// host). Poisoned rows failed every later read with "unsupported Scan,
// storing driver.Value type string into type *time.Time".
//
// Write paths that take their instant only from the daemon clock
// (time.Now() inside the store, e.g. the session-interface transition and
// most conversation lifecycle writes) cannot receive a nameless zone from a
// caller; they are normalized by the shared helper covered in
// timestamp_utc_internal_test.go.

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// namelessZone relabels the instant into the zone a hook client's foreign
// offset produces on JSON decode: fixed, numeric, and nameless.
func namelessZone(t time.Time) time.Time {
	return t.Truncate(time.Second).In(time.FixedZone("", 7*3600))
}

func TestProjectArchiveTimestampNamelessZoneRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "archive-zone")
	archivedAt := namelessZone(time.Date(2026, 11, 25, 1, 0, 25, 0, time.UTC))
	if ok, err := s.ArchiveProject(ctx, "archive-zone", archivedAt); err != nil || !ok {
		t.Fatalf("archive project: ok=%v err=%v", ok, err)
	}
	got, ok, err := s.GetProject(ctx, "archive-zone")
	if err != nil || !ok {
		t.Fatalf("get project: ok=%v err=%v", ok, err)
	}
	if !got.ArchivedAt.Equal(archivedAt) {
		t.Fatalf("archivedAt = %v, want %v", got.ArchivedAt, archivedAt)
	}
}

func TestSessionVersionSwitchFailedTimestampNamelessZoneRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "vsf-zone")
	sess, err := s.CreateSession(ctx, sampleRecord("vsf-zone"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	at := namelessZone(time.Now().UTC().Add(time.Minute))
	if err := s.SetSessionVersionSwitchFailed(ctx, sess.ID, "v-next", at); err != nil {
		t.Fatalf("record version switch failure: %v", err)
	}
	// A poisoned failed_at text form breaks every scan of the row, not just
	// the failed-at field.
	if _, ok, err := s.GetSession(ctx, sess.ID); err != nil || !ok {
		t.Fatalf("get session: ok=%v err=%v", ok, err)
	}
	if _, err := s.ListSessions(ctx, sess.ProjectID); err != nil {
		t.Fatalf("list sessions: %v", err)
	}
}

func TestMarkHarnessSessionsStaleNamelessSinceStillMatches(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "stale-zone")
	sess, err := s.CreateSession(ctx, sampleRecord("stale-zone"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	// Since is a comparison param: a nameless-zone text form would not
	// string-compare against the stored UTC rows and would silently match
	// nothing.
	n, err := s.MarkHarnessSessionsStale(ctx, domain.HarnessClaudeCode, "v-stale", namelessZone(time.Now().UTC().Add(-time.Minute)))
	if err != nil {
		t.Fatalf("mark sessions stale: %v", err)
	}
	if n != 1 {
		t.Fatalf("marked %d sessions, want 1 (session %s)", n, sess.ID)
	}
}

func TestConversationCompactedTimestampNamelessZoneRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "conv-zone")
	sess, err := s.CreateSession(ctx, sampleRecord("conv-zone"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := s.CreateConversation(ctx, "conv-zone-conversation", domain.ConversationScopeSession, "conv-zone", sess.ID, time.Now().UTC()); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	compactedAt := namelessZone(time.Now().UTC().Add(time.Minute))
	if err := s.MarkCompacted(ctx, "conv-zone-conversation", compactedAt); err != nil {
		t.Fatalf("mark compacted: %v", err)
	}
	conv, err := s.ConversationForSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("conversation for session: %v", err)
	}
	if conv.CompactedAt == nil || !conv.CompactedAt.Equal(compactedAt) {
		t.Fatalf("compactedAt = %v, want %v", conv.CompactedAt, compactedAt)
	}
}

func TestPRMergedTimestampNamelessZoneRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "pr-zone")
	sess, err := s.CreateSession(ctx, sampleRecord("pr-zone"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	pr := domain.PullRequest{
		URL: "https://pr-zone.example/pull/1", SessionID: sess.ID, Number: 1,
		CI: domain.CIPassing, SourceBranch: "feat/zone", TargetBranch: "main",
		UpdatedAt: now, ObservedAt: now,
	}
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("write pr: %v", err)
	}
	mergedAt := namelessZone(now.Add(time.Minute))
	if err := s.MarkPRMerged(ctx, pr.URL, "mergedsha", mergedAt); err != nil {
		t.Fatalf("mark pr merged: %v", err)
	}
	if err := s.TouchPRSeen(ctx, pr.URL, mergedAt); err != nil {
		t.Fatalf("touch pr seen: %v", err)
	}
	got, ok, err := s.GetPR(ctx, pr.URL)
	if err != nil || !ok {
		t.Fatalf("get pr: ok=%v err=%v", ok, err)
	}
	if !got.ObservedAt.Equal(mergedAt) {
		t.Fatalf("observedAt = %v, want %v", got.ObservedAt, mergedAt)
	}
}

func TestReviewRunDeliveredTimestampNamelessZoneRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "rev-zone")
	sess, err := s.CreateSession(ctx, sampleRecord("rev-zone"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.UpsertReview(ctx, domain.Review{
		ID: "rev-zone-1", SessionID: sess.ID, ProjectID: sess.ProjectID,
		Harness: domain.ReviewerClaudeCode, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert review: %v", err)
	}
	if err := s.InsertReviewRun(ctx, domain.ReviewRun{
		ID: "run-zone-1", ReviewID: "rev-zone-1", SessionID: sess.ID,
		Harness: domain.ReviewerClaudeCode, PRURL: "https://rev-zone.example/pull/1",
		TargetSHA: "shazone", Status: domain.ReviewRunRunning, CreatedAt: now,
	}); err != nil {
		t.Fatalf("insert review run: %v", err)
	}
	if ok, err := s.UpdateReviewRunResult(ctx, "run-zone-1", domain.ReviewRunComplete, domain.VerdictApproved, "ship it", "[]", "", true); err != nil || !ok {
		t.Fatalf("record result: ok=%v err=%v", ok, err)
	}
	deliveredAt := namelessZone(now.Add(time.Minute))
	if ok, err := s.MarkReviewRunDelivered(ctx, "run-zone-1", deliveredAt); err != nil || !ok {
		t.Fatalf("mark review run delivered: ok=%v err=%v", ok, err)
	}
	run, ok, err := s.GetReviewRun(ctx, "run-zone-1")
	if err != nil || !ok {
		t.Fatalf("get review run: ok=%v err=%v", ok, err)
	}
	if run.DeliveredAt == nil || !run.DeliveredAt.Equal(deliveredAt) {
		t.Fatalf("deliveredAt = %v, want %v", run.DeliveredAt, deliveredAt)
	}
}

func TestNotificationResolveTimestampNamelessZoneRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ntf-zone")
	sess, err := s.CreateSession(ctx, sampleRecord("ntf-zone"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, inserted, err := s.CreateNotification(ctx, domain.NotificationRecord{
		ID: "ntf-zone-1", SessionID: sess.ID, ProjectID: sess.ProjectID,
		Type: domain.NotificationNeedsInput, Title: "needs input",
		Status: domain.NotificationUnread, CreatedAt: time.Now().UTC(),
	}); err != nil || !inserted {
		t.Fatalf("create notification: inserted=%v err=%v", inserted, err)
	}
	resolved, err := s.ResolveSessionNotifications(ctx, sess.ID, domain.NotificationNeedsInput, namelessZone(time.Now().UTC().Add(time.Minute)))
	if err != nil {
		t.Fatalf("resolve notifications: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved %d rows, want 1", len(resolved))
	}
}

func TestReportClaimAndAcknowledgeNamelessZoneRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "rpt-zone")
	sess, err := s.CreateSession(ctx, sampleRecord("rpt-zone"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := s.CreateReport(ctx, domain.ReportRecord{
		ID: "rpt-zone-1", SessionID: sess.ID, ProjectID: sess.ProjectID,
		CreatedAt: now, DeliveryState: domain.ReportPending,
		AvailableAt: now.Add(domain.ReportSettlementWindow), RepeatCount: 1,
		Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: "zone-ref"}},
	}); err != nil {
		t.Fatalf("create report: %v", err)
	}
	claimAt := namelessZone(now.Add(2 * domain.ReportSettlementWindow))
	claimed, err := s.ClaimPendingReportBatch(ctx, sess.ProjectID, "token-zone", claimAt)
	if err != nil {
		t.Fatalf("claim reports: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d reports, want 1", len(claimed))
	}
	if n, err := s.AcknowledgeReportBatch(ctx, sess.ProjectID, "token-zone", namelessZone(claimAt.Add(time.Minute))); err != nil || n != 1 {
		t.Fatalf("acknowledge reports: n=%d err=%v", n, err)
	}
	if _, ok, err := s.GetReport(ctx, "rpt-zone-1"); err != nil || !ok {
		t.Fatalf("get report: ok=%v err=%v", ok, err)
	}
}
