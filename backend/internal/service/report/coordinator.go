package report

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ReportExpiryAge is the delivery ledger's default terminal expiry: a report
// still unread a week after creation is retired as stale instead of polling
// the drain forever (fork issue #242). The daemon makes it configurable via
// AO_DELIVERY_EXPIRY_DAYS; claimed rows ride out their own attempt first.
const ReportExpiryAge = 7 * 24 * time.Hour

// ErrReportDeliveryModeUnsupported reports a target without semantic delivery.
var ErrReportDeliveryModeUnsupported = errors.New("report delivery requires a semantic conversation")

// DeliveryStore is the durable report scheduling and routing boundary.
type DeliveryStore interface {
	ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListPendingDeliveryProjects(context.Context) ([]domain.ProjectID, error)
	ClaimPendingReportBatch(context.Context, domain.ProjectID, string, time.Time) ([]domain.ReportRecord, error)
	AcknowledgeReportBatch(context.Context, domain.ProjectID, string, time.Time) (int, error)
	ReleaseReportBatch(context.Context, domain.ProjectID, string, time.Time, string) (int, error)
	FailReportBatchTerminally(ctx context.Context, projectID domain.ProjectID, token, cause, lastError string) (int, error)
	ExpireStaleReportDeliveries(context.Context, time.Time) (int, error)
}

// Delivery submits accepted semantic context and interrupts active work.
type Delivery interface {
	Submit(context.Context, domain.SessionID, string, string) error
	Interrupt(context.Context, domain.SessionID) error
}

// Coordinator delivers durable report batches to active orchestrators.
type Coordinator struct {
	store      DeliveryStore
	delivery   Delivery
	log        *slog.Logger
	now        func() time.Time
	newToken   func() string
	retryDelay time.Duration
	expiryAge  time.Duration
	wake       chan struct{}
	mu         sync.Mutex
	// injectedIdle records the sessions that already had their safe-point
	// inject in the current idle period (fork issue #242): the drain attempts
	// one injection per wake-from-idle, and a failed attempt waits for the
	// recipient's next turn boundary instead of re-pasting on every pass.
	// In-memory on purpose: the chance is loop hygiene, not durable delivery
	// state — a daemon restart re-arms every session once (the startup drain).
	injectedIdle map[domain.SessionID]struct{}
	// Telemetry for the issue #101 replay cycle: claims that re-deliver a batch
	// (delivery attempt > 1) and acknowledgments whose token no longer holds it.
	redeliveries, lostAcknowledgements atomic.Int64
}

// CoordinatorDeps configures durable scheduling and delivery.
type CoordinatorDeps struct {
	Store      DeliveryStore
	Delivery   Delivery
	Now        func() time.Time
	NewToken   func() string
	RetryDelay time.Duration
	// ExpiryAge is how long an unread report stays deliverable before the
	// ledger retires it as stale (fork issue #242). Zero uses ReportExpiryAge.
	ExpiryAge time.Duration
	// Log receives delivery evidence lines (emissions, anomalies).
	// A nil Log discards, keeping tests and embedders quiet by default.
	Log *slog.Logger
}

// NewCoordinator constructs an idle report delivery coordinator.
func NewCoordinator(d CoordinatorDeps) *Coordinator {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.NewToken == nil {
		d.NewToken = uuid.NewString
	}
	if d.RetryDelay <= 0 {
		d.RetryDelay = 5 * time.Second
	}
	if d.ExpiryAge <= 0 {
		d.ExpiryAge = ReportExpiryAge
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	return &Coordinator{store: d.Store, delivery: d.Delivery, log: d.Log, now: d.Now, newToken: d.NewToken, retryDelay: d.RetryDelay, expiryAge: d.ExpiryAge, wake: make(chan struct{}, 1), injectedIdle: map[domain.SessionID]struct{}{}}
}

// Wake asks the coordinator to inspect durable scheduling state.
func (c *Coordinator) Wake() {
	if c == nil {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Start runs delivery until ctx is cancelled and returns its completion signal.
func (c *Coordinator) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() { defer close(done); c.run(ctx) }()
	return done
}

func (c *Coordinator) run(ctx context.Context) {
	for {
		delay, err := c.RunDue(ctx)
		if err != nil {
			c.log.Warn("report: delivery cycle failed", "err", err)
			delay = c.retryDelay
		}
		if delay < 0 {
			delay = time.Hour
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-c.wake:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}

// RunDue performs one pull drain pass and returns how long the caller should
// wait before the next pass. Tests call it directly with fake time.
func (c *Coordinator) RunDue(ctx context.Context) (time.Duration, error) {
	if c == nil || c.store == nil || c.delivery == nil {
		return -1, errors.New("report coordinator dependencies are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Ledger housekeeping runs once per pass, before draining: expired reports
	// leave the mailbox instead of resurfacing every pass forever. A failure
	// never blocks delivery — it only postpones expiry.
	if expired, err := c.store.ExpireStaleReportDeliveries(ctx, c.now().UTC().Add(-c.expiryAge)); err != nil {
		c.log.Warn("report: stale expiry failed", "err", err)
	} else if expired > 0 {
		c.log.Info("report: expired stale deliveries", "reports", expired)
	}
	projects, err := c.store.ListPendingDeliveryProjects(ctx)
	if err != nil {
		return c.retryDelay, err
	}
	if len(projects) == 0 {
		return -1, nil
	}
	// One drain attempt per project holding unread rows. A skip (busy or
	// temporarily recipient-less project) is not an error: the row simply
	// stays unread until the next pass finds a safe point.
	unreadRemains := false
	for _, projectID := range projects {
		skipped, err := c.deliverProject(ctx, projectID)
		if err != nil {
			return c.retryDelay, err
		}
		unreadRemains = unreadRemains || skipped
	}
	if unreadRemains {
		// Keep the drain cadence so a recipient waking from idle is noticed
		// within seconds. Wake() shortens this on new emits.
		return c.retryDelay, nil
	}
	return -1, nil
}

// ErrReportBatchLost reports an acknowledgment whose claim token no longer
// holds the batch: a daemon restart requeue or another path already owns it.
// Treating it as success silently re-delivers the batch on the next turn.
var ErrReportBatchLost = errors.New("report batch claim no longer holds the reports")

// ackBatch completes only the delivery attempt holding token. A zero-row
// acknowledgment means the claim was lost and must not pass as success.
func (c *Coordinator) ackBatch(ctx context.Context, projectID domain.ProjectID, token string) error {
	rows, err := c.store.AcknowledgeReportBatch(ctx, projectID, token, c.now().UTC())
	if err != nil {
		return err
	}
	if rows == 0 {
		c.lostAcknowledgements.Add(1)
		c.log.Warn("report: acknowledgment claim lost", "project", projectID, "token", token)
		return fmt.Errorf("%w: project %s token %s", ErrReportBatchLost, projectID, token)
	}
	return nil
}

// deliverProject runs one drain attempt for a project's unread reports under
// pull semantics (fork issue #242): a recipient that is mid-turn or blocked is
// left unread — nothing is claimed, nothing is in flight, nothing retries.
// It reports whether the project still holds unread rows after the attempt.
// A recipient that can never exist (archived project, fork issue #208) is
// terminalized on first sight: stale with the recipient_gone cause, zero
// retries, one bell event from the terminal sweep. A temporarily absent
// orchestrator (daemon restart, session being replaced) also just leaves the
// rows unread — absence is never a terminal outcome; the configurable ledger
// expiry is the only exit for a recipient that never returns.
func (c *Coordinator) deliverProject(ctx context.Context, projectID domain.ProjectID) (bool, error) {
	if c.projectArchived(ctx, projectID) {
		batch, err := c.claim(ctx, projectID)
		if err != nil || len(batch.Reports) == 0 {
			return false, err
		}
		reason := "project archived: no report recipient can exist (fork issue #208)"
		if _, failErr := c.store.FailReportBatchTerminally(ctx, projectID, batch.Token, domain.DeliveryEventRecipientGone, reason); failErr != nil {
			return true, failErr
		}
		c.log.Warn("report: batch retired terminally; project archived", "project", projectID, "batch", batch.IdempotencyKey(), "reports", len(batch.Reports), "attempts", batch.MaxDeliveryAttempts())
		return false, nil
	}
	target, ok, err := c.activeOrchestrator(ctx, projectID)
	if err != nil {
		return true, err
	}
	if !ok {
		return true, nil
	}
	if !deliverableActivity(target.Activity.State) {
		// Leaving the idle period re-arms the session's safe-point chance.
		delete(c.injectedIdle, target.ID)
		return true, nil
	}
	if _, used := c.injectedIdle[target.ID]; used {
		// This idle period already had its injection: a failed attempt waits
		// for the recipient's next turn boundary instead of re-pasting on
		// every pass.
		return true, nil
	}
	batch, err := c.claim(ctx, projectID)
	if err != nil || len(batch.Reports) == 0 {
		return false, err
	}
	// The replay path consults the recipient's durable acceptance before
	// re-delivering: a checkpoint that names this batch's correlation id is
	// the semantic acceptance of exactly this content — its delivery already
	// happened, so a restart or a lost ack must replay the acknowledgment,
	// never re-emit the content (fork issue #184). A checkpoint can only name
	// a batch that was submitted, so a first delivery never matches.
	if accepted, err := c.settleAcceptedBatch(ctx, target, batch); err != nil {
		// Every other post-claim failure releases the claim; a transient store
		// error must not strand the rows claimed and invisible to scheduling.
		return true, c.release(ctx, batch, err)
	} else if accepted {
		delete(c.injectedIdle, target.ID)
		return false, nil
	}
	// Stuck reports ride the normal submit path in every session mode: the
	// fork runs terminal orchestrators only, so there is no chat-mode
	// interrupt lane to rate-limit (upstream #7435's chat branch removed).
	//
	// Delivery evidence: a duplicate emission shows up as a second line with
	// the same batch key and attempts > 1. The inject is going out: the idle
	// period's safe-point chance is spent, whatever its outcome — the safe
	// point itself is the drain's at-most-once mechanism (fork issue #242),
	// so no emit quiet window gates it.
	c.log.Info("report: batch emitted", "batch", batch.IdempotencyKey(), "project", batch.ProjectID, "target", target.ID, "reports", len(batch.Reports), "attempts", batch.MaxDeliveryAttempts())
	c.injectedIdle[target.ID] = struct{}{}
	if err := c.delivery.Submit(ctx, target.ID, batch.Render(), batch.IdempotencyKey()); err != nil {
		// Fail loud (fork issue #179): an emission whose acceptance did not
		// match is a failed delivery. It is never acked here — the batch backs
		// off by the retry delay with last_error set instead of rejoining the
		// head of the queue (#7435) — but the attempt itself names its batch in
		// the log.
		c.log.Warn("report: batch delivery failed", "batch", batch.IdempotencyKey(), "project", batch.ProjectID, "target", target.ID, "attempts", batch.MaxDeliveryAttempts(), "err", err)
		if errors.Is(err, ports.ErrPromptHeldQueued) {
			// The target TUI queued the batch text instead of submitting a
			// prompt (fork issue #205). Retrying would re-emit the same
			// batch onto that queue forever; retire it terminally instead.
			// The already-queued copies need a manual flush (ctrl+x ctrl+s in
			// the session); new reports flow again once the queue is clear.
			if _, failErr := c.store.FailReportBatchTerminally(ctx, batch.ProjectID, batch.Token, domain.DeliveryEventInjectFailed, err.Error()); failErr != nil {
				return true, errors.Join(err, failErr)
			}
			return false, err
		}
		// Any other refusal: the rows return to the mailbox unread and wait
		// for the next safe-point drain.
		return true, c.release(ctx, batch, err)
	}
	// Delivered: the mailbox emptied, so the session's safe-point chance is
	// spent — a later emit while still idle is a new event with a fresh one.
	delete(c.injectedIdle, target.ID)
	return false, c.ackBatch(ctx, batch.ProjectID, batch.Token)
}

// deliverableActivity reports whether a recipient's current activity is a safe
// inject point (fork issue #242): idle, or parked at an empty prompt waiting
// for its next instruction. Mid-turn and blocked-on-a-decision surfaces are
// left unread — writing to them queues or swallows the batch, which the
// mailbox has no reason to risk.
func deliverableActivity(state domain.ActivityState) bool {
	return state == domain.ActivityIdle || state == domain.ActivityWaitingInput
}

// projectArchived reports whether the owner closed the project (fork issue
// #208). A lookup failure reads as live: a transient store error must not
// retire reports a later cycle could still deliver.
func (c *Coordinator) projectArchived(ctx context.Context, projectID domain.ProjectID) bool {
	project, ok, err := c.store.GetProject(ctx, string(projectID))
	if err != nil {
		c.log.Warn("report: project lookup failed; assuming live", "project", projectID, "err", err)
		return false
	}
	return ok && !project.ArchivedAt.IsZero()
}

func (c *Coordinator) activeOrchestrator(ctx context.Context, projectID domain.ProjectID) (domain.SessionRecord, bool, error) {
	recs, err := c.store.ListSessions(ctx, projectID)
	if err != nil {
		return domain.SessionRecord{}, false, err
	}
	var selected domain.SessionRecord
	for _, rec := range recs {
		if rec.Kind != domain.KindOrchestrator || rec.IsTerminated || rec.Activity.State == domain.ActivityExited {
			continue
		}
		if selected.ID == "" || rec.CreatedAt.After(selected.CreatedAt) {
			selected = rec
		}
	}
	return selected, selected.ID != "", nil
}

// ActiveOrchestrator returns the project's current delivery target: its newest
// live orchestrator session. Merge notices (fork issue #243) reuse the same
// selection rule as report dispatch so both machines address one recipient.
func (c *Coordinator) ActiveOrchestrator(ctx context.Context, projectID domain.ProjectID) (domain.SessionRecord, bool, error) {
	return c.activeOrchestrator(ctx, projectID)
}

// PreparedBatch is a token-fenced durable batch awaiting acceptance.
type PreparedBatch struct {
	ProjectID domain.ProjectID
	Token     string
	ID        string
	Reports   []domain.ReportRecord
}

func (c *Coordinator) claim(ctx context.Context, projectID domain.ProjectID) (PreparedBatch, error) {
	token := c.newToken()
	reports, err := c.store.ClaimPendingReportBatch(ctx, projectID, token, c.now().UTC())
	for i := range reports {
		session, found, lookupErr := c.store.GetSession(ctx, reports[i].SessionID)
		if lookupErr == nil && found {
			reports[i].SessionDisplayName = strings.TrimSpace(session.DisplayName)
		}
		if reports[i].DeliveryAttempts > 1 {
			// A batch whose delivery attempt exceeds one has been claimed before:
			// a release retry, or a stolen claim recovered after a daemon restart.
			c.redeliveries.Add(1)
		}
	}
	batchID := ""
	if len(reports) > 0 {
		batchID = reports[0].DeliveryBatchID
	}
	return PreparedBatch{ProjectID: projectID, Token: token, ID: batchID, Reports: reports}, err
}

// Redeliveries counts claims that re-deliver a batch after a previous attempt.
func (c *Coordinator) Redeliveries() int64 {
	if c == nil {
		return 0
	}
	return c.redeliveries.Load()
}

// LostAcknowledgements counts acknowledgments whose claim token no longer holds
// the batch, so the caller believed a delivery completed that did not.
func (c *Coordinator) LostAcknowledgements() int64 {
	if c == nil {
		return 0
	}
	return c.lostAcknowledgements.Load()
}

// PreparePiggyback claims pending reports for the active Chat orchestrator.
// The caller must acknowledge only after its user message is durably accepted.
func (c *Coordinator) PreparePiggyback(ctx context.Context, target domain.SessionID) (PreparedBatch, error) {
	if c == nil || c.store == nil {
		return PreparedBatch{}, nil
	}
	rec, ok, err := c.store.GetSession(ctx, target)
	if err != nil || !ok {
		return PreparedBatch{}, err
	}
	if rec.Kind != domain.KindOrchestrator || rec.IsTerminated || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return PreparedBatch{}, nil
	}
	batch, err := c.claim(ctx, rec.ProjectID)
	if err != nil || len(batch.Reports) == 0 {
		return batch, err
	}
	// An accepted batch rides along as its acknowledgment, never as appended
	// content twice: the checkpoint naming this batch id is the acceptance of
	// content already in the conversation (fork issue #184).
	if accepted, err := c.settleAcceptedBatch(ctx, rec, batch); err != nil {
		// Release before the send fails: a stranded claim would sit invisible
		// to scheduling until the next daemon restart.
		return PreparedBatch{}, c.release(ctx, batch, err)
	} else if accepted {
		return PreparedBatch{}, nil
	}
	return batch, nil
}

// settleAcceptedBatch retires the claimed batch when the target orchestrator's
// durable coordination checkpoint names its correlation id. It is the
// dispatcher-side half of the restart dedup (fork issue #184): an acceptance
// whose settle probe a restart preceded must not turn into a re-emission —
// the relaunched runtime rotates the launch id, so the leftover checkpoint is
// the only surviving acceptance evidence. The claimed rows ack under their own
// token fence; a checkpoint can only name a batch whose prompt was submitted,
// so a first delivery never matches.
func (c *Coordinator) settleAcceptedBatch(ctx context.Context, target domain.SessionRecord, batch PreparedBatch) (bool, error) {
	if batch.ID == "" || target.Metadata.ConversationCheckpointState != domain.ConversationCheckpointCoordination ||
		target.Metadata.ConversationCheckpointTurnID != batch.ID {
		return false, nil
	}
	if err := c.ackBatch(ctx, batch.ProjectID, batch.Token); err != nil {
		return false, err
	}
	c.log.Info("report: replayed batch settled by durable acceptance", "project", batch.ProjectID, "target", target.ID, "batch", batch.ID, "reports", len(batch.Reports))
	return true, nil
}

// AcceptPiggyback acknowledges reports after the combined user turn is accepted.
func (c *Coordinator) AcceptPiggyback(ctx context.Context, batch PreparedBatch) error {
	if len(batch.Reports) == 0 {
		return nil
	}
	return c.ackBatch(ctx, batch.ProjectID, batch.Token)
}

// ReleasePiggyback makes a batch retryable when the combined turn was refused.
func (c *Coordinator) ReleasePiggyback(ctx context.Context, batch PreparedBatch, cause error) error {
	if len(batch.Reports) == 0 {
		return nil
	}
	return c.release(ctx, batch, cause)
}

func (c *Coordinator) release(ctx context.Context, batch PreparedBatch, cause error) error {
	_, releaseErr := c.store.ReleaseReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC(), cause.Error())
	if releaseErr != nil {
		c.log.Warn("report: batch release failed", "batch", batch.IdempotencyKey(), "project", batch.ProjectID, "err", releaseErr)
		return errors.Join(cause, releaseErr)
	}
	return cause
}

const (
	chatReportOpen  = "<ao-worker-reports>"
	chatReportClose = "</ao-worker-reports>"
)

// AppendToUserMessage places reports after the user's text in one semantic turn.
// The envelope lets AO's Chat UI hide daemon-authored context while preserving
// the exact prompt accepted by the provider for retry and recovery.
func (b PreparedBatch) AppendToUserMessage(message string) string {
	if len(b.Reports) == 0 {
		return message
	}
	return message + "\n\n" + chatReportOpen + "\n" + b.Render() + "\n" + chatReportClose
}

// IdempotencyKey returns the stable durable delivery identity.
func (b PreparedBatch) IdempotencyKey() string {
	if b.ID != "" {
		return b.ID
	}
	ids := make([]string, len(b.Reports))
	for i := range b.Reports {
		ids[i] = b.Reports[i].ID
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\x00")))
	return "report-batch:" + hex.EncodeToString(sum[:16])
}

// MaxDeliveryAttempts returns the batch's highest durable attempt count, for
// delivery evidence lines: a visible re-emission logs attempts > 1.
func (b PreparedBatch) MaxDeliveryAttempts() int64 {
	attempts := int64(0)
	for _, r := range b.Reports {
		if r.DeliveryAttempts > attempts {
			attempts = r.DeliveryAttempts
		}
	}
	return attempts
}

// Render formats compact attributed report context for an orchestrator.
func (b PreparedBatch) Render() string {
	var out strings.Builder
	out.WriteString("Reports since your previous turn:")
	for _, report := range coalesceReports(b.Reports) {
		state := string(report.State)
		if state == "" {
			state = "information"
		}
		fmt.Fprintf(&out, "\n\n[%s] %s", state, reportSessionLink(report))
		if report.RepeatCount > 1 {
			fmt.Fprintf(&out, " (repeated %d times)", report.RepeatCount)
		}
		text := report.Note
		if text == "" {
			text = report.Message
		}
		if text != "" {
			out.WriteString("\n")
			out.WriteString(text)
		}
		for _, output := range report.Outputs {
			fmt.Fprintf(&out, "\n%s: %s", output.Kind, output.Reference)
		}
	}
	return out.String()
}

func reportDisplayNameOrID(r domain.ReportRecord) string {
	if name := strings.TrimSpace(r.SessionDisplayName); name != "" {
		return name
	}
	return string(r.SessionID)
}

// reportSessionLink is the markdown attribution a standalone report shows.
// Piggybacked copies stay inside the hidden user-message envelope.
func reportSessionLink(r domain.ReportRecord) string {
	label := strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "\n", " ").Replace(reportDisplayNameOrID(r))
	href := "ao://sessions/" + url.PathEscape(string(r.ProjectID)) + "/" + url.PathEscape(string(r.SessionID))
	return "[" + label + "](" + href + ")"
}

func coalesceReports(in []domain.ReportRecord) []domain.ReportRecord {
	out := make([]domain.ReportRecord, 0, len(in))
	indexes := map[string]int{}
	for _, report := range in {
		key := reportKey(report)
		if report.State == domain.ReportStuck {
			if i, ok := indexes[key]; ok {
				out[i].RepeatCount += report.RepeatCount
				continue
			}
			indexes[key] = len(out)
		}
		out = append(out, report)
	}
	return out
}

func reportKey(report domain.ReportRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\x00%s\x00%s\x00%s", report.SessionID, report.State, report.Note, report.Message)
	for _, output := range report.Outputs {
		fmt.Fprintf(&b, "\x00%s\x00%s\x00%s", output.Kind, output.Reference, output.Label)
	}
	return b.String()
}
