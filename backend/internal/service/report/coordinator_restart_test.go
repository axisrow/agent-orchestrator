package report

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// restartDelivery captures submissions so a restart test can count how often
// the same batch content reaches the orchestrator.
type restartDelivery struct {
	submissions []string
	failSubmits bool
}

func (d *restartDelivery) Submit(_ context.Context, _ domain.SessionID, message, _ string) error {
	d.submissions = append(d.submissions, message)
	if d.failSubmits {
		return errors.New("prompt submission was not observed")
	}
	return nil
}

func (d *restartDelivery) Interrupt(_ context.Context, _ domain.SessionID) error { return nil }

type restartFixture struct {
	store      *sqlite.Store
	project    domain.ProjectID
	worker     domain.SessionID
	orch       domain.SessionID
	delivery   *restartDelivery
	now        time.Time
	newTokenID int
}

func newRestartFixture(t *testing.T) *restartFixture {
	t.Helper()
	s, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "ao", Path: "/tmp/ao", RegisteredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	worker, err := s.CreateSession(ctx, domain.SessionRecord{ProjectID: "ao", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	orch, err := s.CreateSession(ctx, domain.SessionRecord{ProjectID: "ao", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, Harness: domain.HarnessClaudeCode, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	return &restartFixture{store: s, project: "ao", worker: worker.ID, orch: orch.ID, delivery: &restartDelivery{}, now: now}
}

func (f *restartFixture) newCoordinator() *Coordinator {
	f.newTokenID++
	token := "tok-" + strconv.Itoa(f.newTokenID)
	now := f.now
	return NewCoordinator(CoordinatorDeps{Store: f.store, Delivery: f.delivery, NewToken: func() string { return token }, Now: func() time.Time { return now }})
}

func (f *restartFixture) addReport(t *testing.T, id string) {
	t.Helper()
	_, err := f.store.CreateReport(context.Background(), domain.ReportRecord{
		ID: id, SessionID: f.worker, ProjectID: f.project, State: domain.ReportNeedsInput,
		Note: "worker note " + id, CreatedAt: f.now, DeliveryState: domain.ReportPending,
		AvailableAt: f.now, RepeatCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// requeue is the daemon-startup recovery verbatim.
func (f *restartFixture) requeue(t *testing.T) {
	t.Helper()
	if _, err := f.store.RequeueClaimedReports(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A batch delivered and acknowledged before the restart must not reach the
// orchestrator again after the replay path re-runs (issue #184, acceptance 1).
func TestRestartAcknowledgedBatchIsNotRedelivered(t *testing.T) {
	f := newRestartFixture(t)
	defer f.store.Close()
	ctx := context.Background()
	f.addReport(t, "rpt_ack")

	if _, err := f.newCoordinator().RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.delivery.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1", len(f.delivery.submissions))
	}
	f.requeue(t)
	if _, err := f.newCoordinator().RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.delivery.submissions) != 1 {
		t.Fatalf("post-restart submissions = %d, want 1 (duplicate replay)", len(f.delivery.submissions))
	}
}

// A batch whose delivery the restart interrupted (claim + emission landed, the
// acknowledgment never did) is delivered exactly once after start: silent
// inside the emit quiet window, one re-emission past it (issue #184, acceptance 2).
func TestRestartInterruptedBatchReplaysExactlyOnce(t *testing.T) {
	f := newRestartFixture(t)
	defer f.store.Close()
	ctx := context.Background()
	f.addReport(t, "rpt_lost")

	// Crash window: claim and emission are durable, the ack never lands.
	now := f.now
	c := f.newCoordinator()
	batch, err := c.PreparePiggyback(ctx, f.orch)
	if err != nil || len(batch.Reports) != 1 {
		t.Fatalf("prepare = %#v, err = %v", batch, err)
	}
	if visible, err := c.EmitVisible(ctx, batch); err != nil || !visible {
		t.Fatalf("emit visible = %v, err = %v", visible, err)
	}
	f.requeue(t)

	// Fresh daemon, same store: inside the quiet window the batch stays silent.
	if _, err := f.newCoordinator().RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.delivery.submissions) != 0 {
		t.Fatalf("post-restart submissions inside quiet window = %d, want 0", len(f.delivery.submissions))
	}
	// Past the quiet window exactly one re-emission carries the batch. The
	// in-window suppression cycle claimed once (attempts 1 -> 2), so its own
	// escalated quiet window (1h) is what now has to elapse.
	now = now.Add(61 * time.Minute)
	f.now = now
	if _, err := f.newCoordinator().RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.delivery.submissions) != 1 {
		t.Fatalf("post-quiet submissions = %d, want 1 (exactly-once replay)", len(f.delivery.submissions))
	}
	if got := f.delivery.submissions[0]; got == "" || got != batch.Render() {
		t.Fatalf("replayed content mismatch")
	}
	// The replayed batch is acknowledged by its successful delivery: further
	// cycles, restart recovery included, never re-deliver it.
	f.requeue(t)
	if _, err := f.newCoordinator().RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.delivery.submissions) != 1 {
		t.Fatalf("submissions after replayed-batch restart = %d, want 1", len(f.delivery.submissions))
	}
}

// A batch the recipient already accepted survives a daemon restart as accepted:
// its durable coordination checkpoint names the batch id even though the
// relaunch rotated the runtime launch id, so the post-restart replay settles
// it from the ledger instead of re-emitting accepted content
// (issue #184, acceptance 1 for the settle-missed case).
func TestRestartAcceptedBatchSettlesInsteadOfReemitting(t *testing.T) {
	f := newRestartFixture(t)
	defer f.store.Close()
	ctx := context.Background()
	f.addReport(t, "rpt_accepted")

	// The emission landed but the submit's acceptance wait timed out: the
	// batch is shown, pending, and unacknowledged.
	failing := &restartDelivery{failSubmits: true}
	f.delivery = failing
	if _, err := f.newCoordinator().RunDue(ctx); err == nil {
		t.Fatal("first attempt should surface the submit failure")
	}
	rec, _, err := f.store.GetReport(ctx, "rpt_accepted")
	if err != nil || rec.DeliveryState != domain.ReportPending || rec.EmittedAt.IsZero() {
		t.Fatalf("shown batch = %+v, err = %v, want pending with an emission marker", rec, err)
	}
	batchID := rec.DeliveryBatchID

	// The recipient's runtime submitted the queued prompt and its checkpoint
	// named the batch; the daemon died before any settle probe ran.
	orchRec, ok, err := f.store.GetSession(ctx, f.orch)
	if err != nil || !ok {
		t.Fatalf("orchestrator lookup ok=%v err=%v", ok, err)
	}
	orchRec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointCoordination
	orchRec.Metadata.ConversationCheckpointGeneration = "launch-a"
	orchRec.Metadata.ConversationCheckpointTurnID = batchID
	orchRec.Metadata.RuntimeLaunchID = "launch-a"
	if err := f.store.UpdateSession(ctx, orchRec); err != nil {
		t.Fatal(err)
	}
	f.requeue(t)

	// Restart relaunches the runtime under a new launch id; the leftover
	// checkpoint is the only surviving acceptance evidence. Past the emit
	// quiet window the replay consults it and settles instead of re-emitting.
	now := f.now.Add(61 * time.Minute)
	f.now = now
	orchRec, _, _ = f.store.GetSession(ctx, f.orch)
	orchRec.Metadata.RuntimeLaunchID = "launch-b"
	if err := f.store.UpdateSession(ctx, orchRec); err != nil {
		t.Fatal(err)
	}
	f.delivery = &restartDelivery{}
	if _, err := f.newCoordinator().RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.delivery.submissions) != 0 {
		t.Fatalf("post-restart submissions = %d, want 0: accepted content was re-emitted", len(f.delivery.submissions))
	}
	rec, _, err = f.store.GetReport(ctx, "rpt_accepted")
	if err != nil || rec.DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("settled batch = %+v, err = %v, want acknowledged", rec, err)
	}
}

// The chat piggyback channel obeys the same restart dedup: a batch accepted
// before the restart is claimed and settled by the next user message instead
// of being appended to it a second time (issue #184).
func TestRestartAcceptedBatchDoesNotRideAlongTwice(t *testing.T) {
	f := newRestartFixture(t)
	defer f.store.Close()
	ctx := context.Background()
	f.addReport(t, "rpt_rider")

	c := f.newCoordinator()
	batch, err := c.PreparePiggyback(ctx, f.orch)
	if err != nil || len(batch.Reports) != 1 {
		t.Fatalf("prepare = %#v, err = %v", batch, err)
	}
	if visible, err := c.EmitVisible(ctx, batch); err != nil || !visible {
		t.Fatalf("emit visible = %v, err = %v", visible, err)
	}
	// The turn was refused after the content was shown: pending, emitted.
	// ReleasePiggyback returns the cause itself by contract.
	if err := c.ReleasePiggyback(ctx, batch, errors.New("turn refused")); err == nil {
		t.Fatal("release should report the refusal cause")
	}
	rec, _, err := f.store.GetReport(ctx, "rpt_rider")
	if err != nil || rec.DeliveryState != domain.ReportPending {
		t.Fatalf("released batch = %+v, err = %v, want pending", rec, err)
	}
	batchID := rec.DeliveryBatchID

	// The retry's queued prompt was accepted; its checkpoint named the batch.
	// The daemon then restarted and the relaunch rotated the launch id.
	orchRec, ok, err := f.store.GetSession(ctx, f.orch)
	if err != nil || !ok {
		t.Fatalf("orchestrator lookup ok=%v err=%v", ok, err)
	}
	orchRec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointCoordination
	orchRec.Metadata.ConversationCheckpointGeneration = "launch-a"
	orchRec.Metadata.ConversationCheckpointTurnID = batchID
	orchRec.Metadata.RuntimeLaunchID = "launch-b"
	if err := f.store.UpdateSession(ctx, orchRec); err != nil {
		t.Fatal(err)
	}
	f.requeue(t)

	batch, err = f.newCoordinator().PreparePiggyback(ctx, f.orch)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Reports) != 0 {
		t.Fatalf("piggyback batch = %d reports, want 0: accepted content rides along again", len(batch.Reports))
	}
	rec, _, err = f.store.GetReport(ctx, "rpt_rider")
	if err != nil || rec.DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("settled batch = %+v, err = %v, want acknowledged", rec, err)
	}
}
