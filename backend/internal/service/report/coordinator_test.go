package report

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCoordinatorDrainsUnreadIntoOneBatchImmediately(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}}
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, NewToken: func() string { return "claim" }, RetryDelay: 42 * time.Second})
	ctx := context.Background()

	// An empty mailbox idles the drain.
	if delay, err := c.RunDue(ctx); err != nil || delay >= 0 {
		t.Fatalf("empty mailbox delay = %v, err = %v", delay, err)
	}

	// Two reports land unread minutes apart; the first idle drain takes both
	// in one batch, without waiting any settle window.
	store.add(reportAt("one", "worker", now, now, "first"))
	store.add(reportAt("two", "worker", now.Add(20*time.Minute), now.Add(20*time.Minute), "second"))
	now = now.Add(time.Hour)
	if delay, err := c.RunDue(ctx); err != nil || delay >= 0 {
		t.Fatalf("drain delay = %v, err = %v", delay, err)
	}
	if len(delivery.submissions) != 1 || !strings.Contains(delivery.submissions[0].message, "first") || !strings.Contains(delivery.submissions[0].message, "second") {
		t.Fatalf("submission = %#v, want one shared batch", delivery.submissions)
	}
	if store.acked != 2 {
		t.Fatalf("acknowledged = %d, want 2", store.acked)
	}

	// A done report is no exception: delivery is immediate, the settle window
	// survives only as the row's deadline fact.
	done := reportAt("done-1", "worker", now, now, "finished")
	done.State = domain.ReportDone
	store.add(done)
	if delay, err := c.RunDue(ctx); err != nil || delay >= 0 {
		t.Fatalf("done-report drain delay = %v, err = %v", delay, err)
	}
	if len(delivery.submissions) != 2 || !strings.Contains(delivery.submissions[1].message, "finished") {
		t.Fatalf("done submission = %#v", delivery.submissions)
	}
	if store.acked != 3 {
		t.Fatalf("acknowledged = %d, want 3", store.acked)
	}
	// The drained mailbox idles the drain again.
	if delay, err := c.RunDue(ctx); err != nil || delay >= 0 {
		t.Fatalf("post-drain delay = %v, err = %v", delay, err)
	}
}

// Pull semantics leave a temporarily recipient-less project unread — absence
// claims nothing and never terminalizes — while other projects drain in the
// same pass, and delivery resumes once a replacement orchestrator is back.
func TestCoordinatorNoOrchestratorLeavesUnreadWithoutStarvingOthers(t *testing.T) {
	now := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.add(reportAt("need", "worker", now, now, "decision"))
	other := reportAt("other", "worker2", now, now, "other project")
	other.ProjectID = "q"
	store.add(other)
	store.sessions = []domain.SessionRecord{{ID: "orch-q", ProjectID: "q", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}}
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, RetryDelay: 42 * time.Second})
	ctx := context.Background()

	if delay, err := c.RunDue(ctx); err != nil || delay != 42*time.Second {
		t.Fatalf("pass delay = %v, err = %v, want the drain cadence", delay, err)
	}
	if len(delivery.submissions) != 1 || delivery.submissions[0].target != "orch-q" {
		t.Fatalf("starved submissions = %#v", delivery.submissions)
	}
	if rec := store.reports[0]; rec.DeliveryState != domain.ReportPending || rec.DeliveryAttempts != 0 || rec.LastError != "" {
		t.Fatalf("absent-recipient record = %+v, want untouched unread", rec)
	}

	// The orchestrator returns and the mailbox drains at the next pass.
	store.sessions = append(store.sessions, domain.SessionRecord{ID: "replacement", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, CreatedAt: now.Add(time.Second), Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}})
	if _, err := c.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 2 || delivery.submissions[1].target != "replacement" {
		t.Fatalf("resumed submissions = %#v", delivery.submissions)
	}
	if store.acked != 2 {
		t.Fatalf("acked = %d, want 2", store.acked)
	}
}

// Pull semantics leave a mid-turn or blocked-on-a-decision recipient unread:
// no claim, no write, nothing in flight. A waiting-input recipient is a safe
// inject point.
func TestCoordinatorLeavesUnreadWhileRecipientBusy(t *testing.T) {
	now := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		state domain.ActivityState
	}{
		{"active", domain.ActivityActive},
		{"blocked", domain.ActivityBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newCoordinatorStore(now)
			store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI, Activity: domain.Activity{State: tc.state, LastActivityAt: now}}}
			store.add(reportAt("busy", "worker", now, now, "context"))
			delivery := &coordinatorDelivery{}
			c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, RetryDelay: 42 * time.Second})
			delay, err := c.RunDue(context.Background())
			if err != nil || delay != 42*time.Second {
				t.Fatalf("delay = %v, err = %v, want the drain cadence", delay, err)
			}
			if len(delivery.submissions) != 0 || len(delivery.interrupts) != 0 {
				t.Fatalf("writes to a busy recipient: %v / %v", delivery.submissions, delivery.interrupts)
			}
			if rec := store.reports[0]; rec.DeliveryState != domain.ReportPending || rec.DeliveryAttempts != 0 || rec.LastError != "" {
				t.Fatalf("record = %+v, want untouched unread", rec)
			}
			// The turn ends; the next drain pass delivers.
			store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}}
			if delay, err := c.RunDue(context.Background()); err != nil || delay >= 0 {
				t.Fatalf("post-wake delay = %v, err = %v", delay, err)
			}
			if len(delivery.submissions) != 1 || store.acked != 1 {
				t.Fatalf("wake delivery: submissions = %d, acked = %d", len(delivery.submissions), store.acked)
			}
		})
	}
}

// Stuck reports ride the normal submit path in every session mode — the fork
// has no chat-mode interrupt lane to rate-limit (removed with upstream #7435).
func TestCoordinatorNeverInterruptsTerminalOrchestrator(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeTUI, "", domain.SessionModeChat} {
		now := time.Date(2026, 10, 28, 2, 10, 0, 0, time.UTC)
		store := newCoordinatorStore(now)
		store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: mode, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}}
		first := reportAt("stuck-1", "worker", now, now, "cannot continue")
		first.State = domain.ReportStuck
		store.add(first)
		delivery := &coordinatorDelivery{}
		c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, NewToken: func() string { return "claim" }})
		if _, err := c.RunDue(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(delivery.interrupts) != 0 || len(delivery.submissions) != 1 {
			t.Fatalf("mode %q: interrupts = %v, submissions = %d", mode, delivery.interrupts, len(delivery.submissions))
		}
		repeat := reportAt("stuck-2", "worker", now.Add(time.Minute), now.Add(time.Minute), "still cannot continue")
		repeat.State = domain.ReportStuck
		store.add(repeat)
		if _, err := c.RunDue(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(delivery.interrupts) != 0 || len(delivery.submissions) != 2 {
			t.Fatalf("mode %q repeat: interrupts = %v, submissions = %d", mode, delivery.interrupts, len(delivery.submissions))
		}
	}
}

func TestReportSessionLinkUsesDisplayName(t *testing.T) {
	got := reportSessionLink(domain.ReportRecord{
		SessionID: "worker/1", ProjectID: "proj 1", SessionDisplayName: "Backend [worker]",
	})
	want := `[Backend \[worker\]](ao://sessions/proj%201/worker%2F1)`
	if got != want {
		t.Fatalf("link = %q, want %q", got, want)
	}
}

func TestCoordinatorAcceptanceAndPiggybackFencing(t *testing.T) {
	now := time.Date(2026, 9, 14, 13, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("info", "worker", now, now.Add(time.Hour), "context"))
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: &coordinatorDelivery{}, Now: func() time.Time { return now }, NewToken: func() string { return "piggyback-token" }})
	batch, err := c.PreparePiggyback(context.Background(), "orch")
	if err != nil || len(batch.Reports) != 1 {
		t.Fatalf("prepare = %#v, err = %v", batch, err)
	}
	combined := batch.AppendToUserMessage("hello")
	if !strings.HasPrefix(combined, "hello\n\n<ao-worker-reports>\n") ||
		!strings.HasSuffix(combined, "\n</ao-worker-reports>") {
		t.Fatalf("piggyback text = %q", combined)
	}
	if !strings.Contains(combined, "[checkpoint] [worker](ao://sessions/p/worker)") {
		t.Fatalf("piggyback link = %q", combined)
	}
	if store.acked != 0 {
		t.Fatal("claimed report acknowledged before acceptance")
	}
	if err := c.AcceptPiggyback(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if store.acked != 1 {
		t.Fatalf("acknowledged = %d", store.acked)
	}
	if count, _ := store.AcknowledgeReportBatch(context.Background(), "p", "stale-token", now); count != 0 {
		t.Fatalf("stale token acknowledged %d reports", count)
	}
}

// TestCoordinatorPiggybackLostClaimDoesNotReplayBatch reproduces the issue #101
// replay cycle: a long turn's claim is stolen past the lease window (daemon
// restart requeue), the concurrent drain path redelivers the batch, and the
// chat turn's late acknowledgment must not report success. The acknowledged
// batch must not be injected into a following user message again.
func TestCoordinatorPiggybackLostClaimDoesNotReplayBatch(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}}
	store.add(reportAt("info", "worker", now, now, "context"))
	delivery := &coordinatorDelivery{}
	tokens := []string{"chat-token", "due-token"}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, NewToken: func() string { return tokens[0] }})

	batch, err := c.PreparePiggyback(context.Background(), "orch")
	if err != nil || len(batch.Reports) != 1 || batch.Token != "chat-token" {
		t.Fatalf("prepare = %#v, err = %v", batch, err)
	}
	injected := batch.AppendToUserMessage("hello")
	if !strings.Contains(injected, "context") {
		t.Fatalf("expected the batch injected into the turn: %q", injected)
	}

	// The lease window lapses: a daemon restart requeues the stolen claim.
	store.requeue()

	// The concurrent drain pass claims with a fresh token and delivers once.
	tokens[0] = "due-token"
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 1 {
		t.Fatalf("deliveries = %d, want exactly one", len(delivery.submissions))
	}

	// The acknowledged batch cannot be injected into a following turn.
	next, err := c.PreparePiggyback(context.Background(), "orch")
	if err != nil || len(next.Reports) != 0 {
		t.Fatalf("replayed prepare = %#v, err = %v", next, err)
	}
	if again := next.AppendToUserMessage("next turn"); again != "next turn" {
		t.Fatalf("replayed injection = %q", again)
	}

	// The late acknowledgment of the stolen claim must not be a silent success.
	if err := c.AcceptPiggyback(context.Background(), batch); err == nil {
		t.Fatal("lost-claim acknowledgment succeeded silently")
	}
	if c.LostAcknowledgements() != 1 {
		t.Fatalf("lost acknowledgements = %d, want 1", c.LostAcknowledgements())
	}
	if c.Redeliveries() != 1 {
		t.Fatalf("redeliveries = %d, want 1", c.Redeliveries())
	}
}

// TestCoordinatorBusyClaimBlocksRunDue pins the no-crash path: while a piggyback
// turn holds its claim, a concurrent drain pass delivers nothing, and the
// healthy acknowledgment still succeeds.
func TestCoordinatorBusyClaimBlocksRunDue(t *testing.T) {
	now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("info", "worker", now, now, "context"))
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, NewToken: func() string { return "chat-token" }})

	batch, err := c.PreparePiggyback(context.Background(), "orch")
	if err != nil || len(batch.Reports) != 1 {
		t.Fatalf("prepare = %#v, err = %v", batch, err)
	}
	delay, err := c.RunDue(context.Background())
	if err != nil || delay >= 0 {
		t.Fatalf("RunDue while claimed should find nothing due: delay = %v, err = %v", delay, err)
	}
	if len(delivery.submissions) != 0 || c.Redeliveries() != 0 {
		t.Fatalf("claimed batch redelivered: %v", delivery.submissions)
	}
	if err := c.AcceptPiggyback(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if remaining, err := c.RunDue(context.Background()); err != nil || remaining >= 0 {
		t.Fatalf("RunDue after ack should stay idle: delay = %v, err = %v", remaining, err)
	}
}

// TestCoordinatorFailedInjectWaitsForNextWakeFromIdle pins the safe-point
// drain (fork issue #242): one inject per idle period. A submit whose semantic
// acceptance failed spent this idle period's chance — the rows stay unread and
// no later pass re-pastes while the recipient stays idle. The next busy->idle
// transition re-arms the session and the mailbox delivers then.
func TestCoordinatorFailedInjectWaitsForNextWakeFromIdle(t *testing.T) {
	now := time.Date(2026, 10, 21, 15, 0, 0, 0, time.UTC)
	idle := domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI, Activity: idle}}
	stuck := reportAt("stuck", "worker", now, now, "cannot continue")
	stuck.State = domain.ReportStuck
	store.add(stuck)
	delivery := &coordinatorDelivery{submitErr: errors.New("composer is not empty")}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, NewToken: func() string { return "claim" }})
	// The refusal releases the rows unread; the failure reason stays visible.
	_, _ = c.RunDue(context.Background())
	if store.reports[0].DeliveryState != domain.ReportPending || store.reports[0].LastError == "" {
		t.Fatalf("released batch = %+v", store.reports[0])
	}
	// Later passes while the recipient stays idle neither claim nor write:
	// zero in-flight churn between safe points.
	for i := 0; i < 3; i++ {
		now = now.Add(time.Minute)
		if delay, err := c.RunDue(context.Background()); err != nil || delay != c.retryDelay {
			t.Fatalf("pass %d delay = %v, err = %v", i, delay, err)
		}
	}
	if len(delivery.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1: a failed inject re-pasted within one idle period", len(delivery.submissions))
	}
	if store.reports[0].DeliveryAttempts != 1 {
		t.Fatalf("attempts = %d, want frozen at the single failed attempt", store.reports[0].DeliveryAttempts)
	}

	// A busy pass re-arms the session: the next wake-from-idle injects again.
	now = now.Add(time.Minute)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}}}
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	delivery.submitErr = nil
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI, Activity: idle}}
	if delay, err := c.RunDue(context.Background()); err != nil || delay >= 0 {
		t.Fatalf("post-wake delay = %v, err = %v", delay, err)
	}
	if len(delivery.submissions) != 2 || store.acked != 1 {
		t.Fatalf("wake delivery: submissions = %d, acked = %d", len(delivery.submissions), store.acked)
	}
}

// TestCoordinatorFailedInjectLogsEvidence keeps the fail-loud contract of
// fork issue #179: a failed inject warns with its durable batch id and the
// released rows keep the failure reason in last_error.
func TestCoordinatorFailedInjectLogsEvidence(t *testing.T) {
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}}
	store.add(reportAt("dup", "worker", now, now, "context"))
	delivery := &coordinatorDelivery{submitErr: errors.New("prompt submission was not observed")}
	var logs bytes.Buffer
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, Log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("first attempt should surface the submit error")
	}
	if got := logs.String(); !strings.Contains(got, "report: batch delivery failed") || !strings.Contains(got, "report-batch:dup") {
		t.Fatalf("delivery failure evidence = %q", got)
	}
	for _, rec := range store.reports {
		if rec.DeliveryState != domain.ReportPending || rec.LastError == "" {
			t.Fatalf("record = %+v, want unread with the failure reason kept", rec)
		}
	}
}

// TestCoordinatorSettlesOnlyMatchedAcceptance pins the settle contract of
// fork issues #159 and #179: the only settlement evidence is the recipient's
// durable coordination checkpoint naming a batch's own correlation id — a
// semantic acceptance of THIS batch whose evidence arrived after the inject
// (a TUI queued the prompt mid-turn and submitted it at the turn boundary).
// Plain prompt activity, a truncated tail (which credits no coordination id),
// and a coordination checkpoint naming some other id settle nothing: the batch
// stays unread and the next safe-point drain settles it instead of re-emitting
// accepted content.
func TestCoordinatorSettlesOnlyMatchedAcceptance(t *testing.T) {
	now := time.Date(2026, 10, 26, 7, 42, 0, 0, time.UTC)
	idle := domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}
	store := newCoordinatorStore(now)
	orch := domain.SessionRecord{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI, Activity: idle}
	store.sessions = []domain.SessionRecord{orch}
	store.add(reportAt("loop", "worker", now, now, "context"))
	delivery := &coordinatorDelivery{submitErr: errors.New("prompt submission was not observed")}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	ctx := context.Background()
	if _, err := c.RunDue(ctx); err == nil {
		t.Fatal("first attempt should surface the submit error")
	}
	// Minutes later the drain stays silent: this idle period already had its
	// inject, and the rows simply wait unread.
	now = now.Add(2 * time.Minute)
	delivery.submitErr = nil
	if delay, err := c.RunDue(ctx); err != nil || delay <= 0 {
		t.Fatalf("silent drain delay = %v, err = %v", delay, err)
	}
	if len(delivery.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1", len(delivery.submissions))
	}
	// Plain prompt activity — a human prompt (the truncated-tail signature: no
	// opening tag, no coordination credit) — settles nothing: the claim-time
	// replay check needs a coordination checkpoint, so the safe-point drain
	// re-injects (the recipient never accepted this batch). A turn happened in
	// between, so the busy pass re-arms the session first.
	orch.Metadata = domain.SessionMetadata{ConversationCheckpointState: domain.ConversationCheckpointPrompt, ConversationCheckpointTurnID: "tail-turn-id"}
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, Metadata: orch.Metadata}}
	if _, err := c.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	orch.Activity = idle
	store.sessions = []domain.SessionRecord{orch}
	if delay, err := c.RunDue(ctx); err != nil || delay >= 0 {
		t.Fatalf("re-inject drain delay = %v, err = %v", delay, err)
	}
	if store.reports[0].DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("record = %+v, want delivered by the safe-point re-inject", store.reports[0])
	}
	if len(delivery.submissions) != 2 {
		t.Fatalf("submissions = %d, want 2", len(delivery.submissions))
	}

	// A coordination checkpoint naming some OTHER batch's id settles nothing
	// either: the correlation-id match is the only acceptance proof. A fresh
	// batch whose inject failed waits unread; once the checkpoint names THIS
	// batch's own id — from a superseded runtime generation, as a daemon
	// restart rotates the launch id without touching the checkpoint (fork
	// issue #184) — the safe-point drain settles it without re-emitting.
	store.add(reportAt("second", "worker", now, now, "more context"))
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}}}
	if _, err := c.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	orch.Activity = idle
	store.sessions = []domain.SessionRecord{orch}
	orch.Metadata = domain.SessionMetadata{
		ConversationCheckpointState:      domain.ConversationCheckpointCoordination,
		ConversationCheckpointGeneration: "live-launch",
		ConversationCheckpointTurnID:     "report-batch:rpt_some_other_batch",
		RuntimeLaunchID:                  "live-launch",
	}
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI, Activity: idle, Metadata: orch.Metadata}}
	delivery.submitErr = errors.New("prompt submission was not observed")
	if _, err := c.RunDue(ctx); err == nil {
		t.Fatal("second batch first attempt should surface the submit error")
	}
	if store.reports[1].DeliveryState != domain.ReportPending {
		t.Fatalf("record = %+v, want the second batch still unread", store.reports[1])
	}
	orch.Metadata = domain.SessionMetadata{
		ConversationCheckpointState:      domain.ConversationCheckpointCoordination,
		ConversationCheckpointGeneration: "stale-launch",
		ConversationCheckpointTurnID:     store.reports[1].DeliveryBatchID,
		RuntimeLaunchID:                  "live-launch",
	}
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, Metadata: orch.Metadata}}
	if _, err := c.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	orch.Activity = idle
	store.sessions = []domain.SessionRecord{orch}
	if delay, err := c.RunDue(ctx); err != nil || delay >= 0 {
		t.Fatalf("post-settle drain delay = %v, err = %v, want an idle schedule", delay, err)
	}
	if store.reports[1].DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("record = %+v, want the second batch acknowledged", store.reports[1])
	}
	if len(delivery.submissions) != 3 {
		t.Fatalf("submissions = %d, want 3: settled batches were re-emitted", len(delivery.submissions))
	}
}

// TestCoordinatorExpiresStalePendingReports pins the delivery-ledger expiry:
// a report that sat unread past ReportExpiryAge leaves the mailbox in the
// same pass — the drain never claims it again — while fresh reports deliver
// normally, and claimed rows ride out their own attempt.
func TestCoordinatorExpiresStalePendingReports(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}}
	ancient := reportAt("ancient", "worker", now.Add(-8*24*time.Hour), now.Add(-8*24*time.Hour), "context")
	ancient.EmittedAt = now.Add(-8 * 24 * time.Hour)
	fresh := reportAt("fresh", "worker", now, now, "later context")
	store.add(ancient, fresh)
	claimedOld := reportAt("claimed-old", "worker", now.Add(-8*24*time.Hour), now, "in flight")
	claimedOld.ClaimToken, claimedOld.ClaimedAt, claimedOld.DeliveryState = "hold", now, domain.ReportClaimed
	store.add(claimedOld)
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	stateByID := func(id string) domain.ReportRecord {
		t.Helper()
		for _, rec := range store.reports {
			if rec.ID == id {
				return rec
			}
		}
		t.Fatalf("report %s vanished from the store", id)
		return domain.ReportRecord{}
	}
	if rec := stateByID("ancient"); rec.DeliveryState != domain.ReportStale {
		t.Fatalf("ancient = %+v, want stale", rec)
	}
	if rec := stateByID("claimed-old"); rec.DeliveryState != domain.ReportClaimed {
		t.Fatalf("claimed in flight = %+v, want untouched", rec)
	}
	if rec := stateByID("fresh"); rec.DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("fresh = %+v, want delivered normally", rec)
	}
	// The stale row never returns to the mailbox.
	store.requeue()
	if delay, err := c.RunDue(context.Background()); err != nil || delay >= 0 {
		t.Fatalf("post-expiry delay = %v, err = %v, want an idle schedule", delay, err)
	}
	if len(delivery.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1: a stale report re-emitted", len(delivery.submissions))
	}
}

// TestCoordinatorExhaustedBatchStillReemits pins the pull answer to a worn
// batch: even a batch with a long delivery history is just unread work — the
// drain attempts it at the next safe point exactly like a fresh one.
func TestCoordinatorExhaustedBatchStillReemits(t *testing.T) {
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}}
	worn := reportAt("worn", "worker", now, now, "context")
	worn.DeliveryAttempts = 7
	worn.EmittedAt = now.Add(-time.Hour)
	store.add(worn)
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	if delay, err := c.RunDue(context.Background()); err != nil || delay >= 0 {
		t.Fatalf("drain delay = %v, err = %v", delay, err)
	}
	if len(delivery.submissions) != 1 || delivery.submissions[0].key != "report-batch:worn" {
		t.Fatalf("submissions = %#v, want one drain injection of batch %q", delivery.submissions, "report-batch:worn")
	}
	if store.acked != 1 {
		t.Fatalf("acked = %d, want 1", store.acked)
	}
}

// TestCoordinatorQueuedPromptFailsBatchTerminally pins the fork issue #205
// contract: a submit refused because the target TUI held the batch text in its
// message queue retires the batch terminally — it never returns to the
// mailbox, so no later pass re-appends the same content to the queue — and the
// WARN log names the distinguishable queue-held reason.
func TestCoordinatorQueuedPromptFailsBatchTerminally(t *testing.T) {
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}}
	store.add(reportAt("queued", "worker", now, now, "context"))
	delivery := &coordinatorDelivery{submitErr: fmt.Errorf("session: %w: prompt submission was not observed; batch text is queued in the session", ports.ErrPromptHeldQueued)}
	var logs bytes.Buffer
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, Log: slog.New(slog.NewTextHandler(&logs, nil))})
	ctx := context.Background()
	if _, err := c.RunDue(ctx); err == nil {
		t.Fatal("first attempt should surface the queue-held error")
	}
	rec := store.reports[0]
	if rec.DeliveryState != domain.ReportStale || rec.LastError == "" || !strings.Contains(rec.LastError, "message queue") {
		t.Fatalf("record = %+v, want terminal stale with the queue-held reason", rec)
	}
	if len(store.failCauses) != 1 || store.failCauses[0] != domain.DeliveryEventInjectFailed {
		t.Fatalf("retire causes = %v, want inject_failed", store.failCauses)
	}
	if got := logs.String(); !strings.Contains(got, "report: batch delivery failed") || !strings.Contains(got, "message queue") {
		t.Fatalf("delivery failure evidence = %q", got)
	}
	// Later passes never re-append the batch: the stale row leaves the mailbox
	// entirely, even after a daemon-restart requeue sweep.
	now = now.Add(6 * time.Hour)
	store.requeue()
	if delay, err := c.RunDue(ctx); err != nil || delay >= 0 {
		t.Fatalf("post-failure delay = %v, err = %v, want an idle schedule", delay, err)
	}
	if len(delivery.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1: a queue-held batch was re-emitted", len(delivery.submissions))
	}
}

// TestCoordinatorArchivedProjectRetiresPendingBatch pins fork issue #208: a
// report whose project the owner archived has a recipient that can never exist
// again, so the batch retires terminally on the first drain pass — stale with
// the reason in last_error, zero retries — and the WARN names the reason.
func TestCoordinatorArchivedProjectRetiresPendingBatch(t *testing.T) {
	now := time.Date(2026, 11, 1, 13, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.archived = true
	store.add(reportAt("orphan", "worker", now, now, "context"))
	var logs bytes.Buffer
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: &coordinatorDelivery{}, Now: func() time.Time { return now }, Log: slog.New(slog.NewTextHandler(&logs, nil))})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec := store.reports[0]; rec.DeliveryState != domain.ReportStale || !strings.Contains(rec.LastError, "archived") {
		t.Fatalf("record = %+v, want terminal stale with the archived reason", store.reports[0])
	}
	if len(store.failCauses) != 1 || store.failCauses[0] != domain.DeliveryEventRecipientGone {
		t.Fatalf("retire causes = %v, want recipient_gone", store.failCauses)
	}
	if got := logs.String(); !strings.Contains(got, "project archived") {
		t.Fatalf("retirement evidence = %q", got)
	}
	// Stale rows leave the mailbox: later passes neither claim nor retry them.
	now = now.Add(time.Hour)
	if delay, err := c.RunDue(context.Background()); err != nil || delay >= 0 {
		t.Fatalf("post-retirement delay = %v, err = %v, want an idle schedule", delay, err)
	}
	if rec := store.reports[0]; rec.DeliveryAttempts != 1 {
		t.Fatalf("attempts = %d, want frozen at the single retirement attempt", rec.DeliveryAttempts)
	}
}

// TestCoordinatorExpiryAgeIsConfigurable pins the configurable ledger expiry
// (fork issue #242): a custom age retires unread rows at its own cutoff while
// the default stays untouched for coordinators without one.
func TestCoordinatorExpiryAgeIsConfigurable(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}}
	old := reportAt("old", "worker", now.Add(-48*time.Hour), now.Add(-48*time.Hour), "context")
	recent := reportAt("recent", "worker", now.Add(-2*time.Hour), now.Add(-2*time.Hour), "fresher context")
	store.add(old, recent)
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, ExpiryAge: 24 * time.Hour})
	if delay, err := c.RunDue(context.Background()); err != nil || delay >= 0 {
		t.Fatalf("drain delay = %v, err = %v", delay, err)
	}
	if store.reports[0].DeliveryState != domain.ReportStale {
		t.Fatalf("old = %+v, want stale at the custom cutoff", store.reports[0])
	}
	if store.reports[1].DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("recent = %+v, want delivered inside the window", store.reports[1])
	}
	if NewCoordinator(CoordinatorDeps{}).expiryAge != ReportExpiryAge {
		t.Fatalf("default expiry age = %v, want %v", NewCoordinator(CoordinatorDeps{}).expiryAge, ReportExpiryAge)
	}
}

func TestCoordinatorEmissionLogsEvidence(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 30, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}}
	store.add(reportAt("evidence", "worker", now, now, "context"))
	var logs bytes.Buffer
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: &coordinatorDelivery{}, Now: func() time.Time { return now }, Log: slog.New(slog.NewTextHandler(&logs, nil))})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"report: batch emitted", "target=orch", "reports=1", "attempts=1"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("emission evidence missing %q: %q", want, logs.String())
		}
	}
}

// TestCoordinatorLostAckReinjectsAtNextSafePoint pins the documented
// crash-window trade-off (fork issue #242): a successful inject whose
// acknowledgment lost the claim (the fence dropped it, #104) reports the loss
// loudly, and after the restart requeue puts the rows back into the mailbox
// the next safe-point drain deliberately re-injects them exactly once —
// the chance marker is spent at the inject, not at the acknowledgment.
func TestCoordinatorLostAckReinjectsAtNextSafePoint(t *testing.T) {
	now := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}}
	store.add(reportAt("fenced", "worker", now, now, "context"))
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	// The inject went out, the fence dropped the ack: loud loss, not silence.
	store.fenceAcks = true
	if _, err := c.RunDue(context.Background()); !errors.Is(err, ErrReportBatchLost) {
		t.Fatalf("RunDue err = %v, want ErrReportBatchLost", err)
	}
	if len(delivery.submissions) != 1 || c.LostAcknowledgements() != 1 {
		t.Fatalf("submissions = %d, lost acks = %d, want one inject and one loud loss", len(delivery.submissions), c.LostAcknowledgements())
	}
	// A daemon restart requeues the claimed rows; the recovered mailbox
	// re-injects at the next safe point and the acknowledgment lands.
	store.fenceAcks = false
	store.requeue()
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 2 || store.acked != 1 {
		t.Fatalf("re-injection: submissions = %d, acked = %d, want exactly one more inject, acknowledged", len(delivery.submissions), store.acked)
	}
	// The drained mailbox idles.
	if delay, err := c.RunDue(context.Background()); err != nil || delay >= 0 {
		t.Fatalf("post-recovery delay = %v, err = %v, want an idle mailbox", delay, err)
	}
}

type coordinatorStore struct {
	mu        sync.Mutex
	now       time.Time
	reports   []domain.ReportRecord
	sessions  []domain.SessionRecord
	archived  bool
	acked     int
	fenceAcks bool
	// failCauses records the journal cause each terminal retire passed, in
	// call order, so coordinator tests pin the caller-named cause.
	failCauses []string
}

func newCoordinatorStore(now time.Time) *coordinatorStore {
	return &coordinatorStore{now: now}
}

func reportAt(id string, worker domain.SessionID, created, available time.Time, note string) domain.ReportRecord {
	return domain.ReportRecord{ID: id, SessionID: worker, ProjectID: "p", State: domain.ReportCheckpoint, Note: note, CreatedAt: created, AvailableAt: available, DeliveryState: domain.ReportPending, RepeatCount: 1}
}

func (s *coordinatorStore) add(records ...domain.ReportRecord) {
	s.reports = append(s.reports, records...)
}
func (s *coordinatorStore) ListSessions(_ context.Context, projectID domain.ProjectID) ([]domain.SessionRecord, error) {
	var out []domain.SessionRecord
	for _, rec := range s.sessions {
		if rec.ProjectID == projectID {
			out = append(out, rec)
		}
	}
	return out, nil
}
func (s *coordinatorStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	rec := domain.ProjectRecord{ID: id}
	if s.archived {
		rec.ArchivedAt = s.now
	}
	return rec, true, nil
}
func (s *coordinatorStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	for _, rec := range s.sessions {
		if rec.ID == id {
			return rec, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}

// ListPendingDeliveryProjects mirrors the pull drain's read: every project
// currently holding unread rows, once each, in stable id order.
func (s *coordinatorStore) ListPendingDeliveryProjects(context.Context) ([]domain.ProjectID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[domain.ProjectID]bool{}
	var out []domain.ProjectID
	for _, rec := range s.reports {
		if rec.DeliveryState != domain.ReportPending || seen[rec.ProjectID] {
			continue
		}
		seen[rec.ProjectID] = true
		out = append(out, rec.ProjectID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func (s *coordinatorStore) ClaimPendingReportBatch(_ context.Context, project domain.ProjectID, token string, at time.Time) ([]domain.ReportRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	batchID := ""
	for i := range s.reports {
		if s.reports[i].ProjectID == project && s.reports[i].DeliveryState == domain.ReportPending {
			batchID = s.reports[i].DeliveryBatchID
			if batchID == "" {
				batchID = "report-batch:" + s.reports[i].ID
			}
			break
		}
	}
	var claimed []domain.ReportRecord
	for i := range s.reports {
		if s.reports[i].ProjectID == project && s.reports[i].DeliveryState == domain.ReportPending {
			if s.reports[i].DeliveryBatchID != "" && s.reports[i].DeliveryBatchID != batchID {
				continue
			}
			s.reports[i].DeliveryBatchID = batchID
			s.reports[i].DeliveryAttempts++
			s.reports[i].DeliveryState, s.reports[i].ClaimToken, s.reports[i].ClaimedAt = domain.ReportClaimed, token, at
			claimed = append(claimed, s.reports[i])
		}
	}
	sortReports(claimed)
	return claimed, nil
}

// requeue mirrors the daemon-startup RequeueClaimedReports recovery: every
// claimed report returns to pending with a cleared token while its durable
// delivery_batch_id survives.
func (s *coordinatorStore) requeue() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.reports {
		if s.reports[i].DeliveryState == domain.ReportClaimed {
			s.reports[i].DeliveryState, s.reports[i].ClaimToken, s.reports[i].ClaimedAt = domain.ReportPending, "", time.Time{}
		}
	}
}
func (s *coordinatorStore) AcknowledgeReportBatch(_ context.Context, project domain.ProjectID, token string, at time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fenceAcks {
		return 0, nil
	}
	count := 0
	for i := range s.reports {
		if s.reports[i].ProjectID == project && s.reports[i].DeliveryState == domain.ReportClaimed && s.reports[i].ClaimToken == token {
			s.reports[i].DeliveryState, s.reports[i].AcknowledgedAt = domain.ReportAcknowledged, at
			count++
		}
	}
	s.acked += count
	return count, nil
}
func (s *coordinatorStore) ReleaseReportBatch(_ context.Context, project domain.ProjectID, token string, _ time.Time, reason string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for i := range s.reports {
		if s.reports[i].ProjectID == project && s.reports[i].DeliveryState == domain.ReportClaimed && s.reports[i].ClaimToken == token {
			s.reports[i].DeliveryState, s.reports[i].ClaimToken, s.reports[i].LastError = domain.ReportPending, "", reason
			count++
		}
	}
	return count, nil
}
func (s *coordinatorStore) FailReportBatchTerminally(_ context.Context, project domain.ProjectID, token, cause, reason string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for i := range s.reports {
		if s.reports[i].ProjectID == project && s.reports[i].DeliveryState == domain.ReportClaimed && s.reports[i].ClaimToken == token {
			s.reports[i].DeliveryState, s.reports[i].ClaimToken, s.reports[i].LastError = domain.ReportStale, "", reason
			s.failCauses = append(s.failCauses, cause)
			count++
		}
	}
	return count, nil
}

func (s *coordinatorStore) ExpireStaleReportDeliveries(_ context.Context, cutoff time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for i := range s.reports {
		if s.reports[i].DeliveryState == domain.ReportPending && s.reports[i].CreatedAt.Before(cutoff) {
			s.reports[i].DeliveryState = domain.ReportStale
			s.reports[i].LastError = "expired unread past the delivery ledger window"
			count++
		}
	}
	return count, nil
}

func sortReports(records []domain.ReportRecord) {
	for i := 1; i < len(records); i++ {
		for j := i; j > 0 && (records[j].AvailableAt.Before(records[j-1].AvailableAt) || (records[j].AvailableAt.Equal(records[j-1].AvailableAt) && records[j].ID < records[j-1].ID)); j-- {
			records[j], records[j-1] = records[j-1], records[j]
		}
	}
}

type coordinatorSubmission struct {
	target       domain.SessionID
	message, key string
}
type coordinatorDelivery struct {
	submissions []coordinatorSubmission
	interrupts  []domain.SessionID
	submitErr   error
}

func (d *coordinatorDelivery) Submit(_ context.Context, target domain.SessionID, message, key string) error {
	// A failed submit still showed its content on the visible channel (the
	// pane write precedes the acceptance wait), so it counts as an emission.
	d.submissions = append(d.submissions, coordinatorSubmission{target, message, key})
	return d.submitErr
}
func (d *coordinatorDelivery) Interrupt(_ context.Context, target domain.SessionID) error {
	d.interrupts = append(d.interrupts, target)
	return nil
}
