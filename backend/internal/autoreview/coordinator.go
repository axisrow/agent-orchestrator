// Package autoreview decides when the daemon may automatically review a
// worker's current pull-request heads.
package autoreview

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
)

const (
	// DefaultIdleThreshold is how long a worker must remain idle before an
	// automatic review may start.
	DefaultIdleThreshold = time.Minute
	// DefaultSweepInterval is the cadence for reevaluating live sessions.
	DefaultSweepInterval = time.Minute
	// autoReviewFailedRetryLimit bounds retries for the same current PR head.
	autoReviewFailedRetryLimit = 3
)

// Store provides the durable session, project, PR, and review facts used by
// the coordinator.
type Store interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListAllSessions(context.Context) ([]domain.SessionRecord, error)
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
	ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error)
	ListReviewRunsBySession(context.Context, domain.SessionID) ([]domain.ReviewRun, error)
}

// Trigger starts a source-tagged automatic review through the normal review
// engine.
type Trigger interface {
	TriggerAuto(context.Context, domain.SessionID, domain.ReviewerHarness) (reviewcore.TriggerResult, error)
}

// PushTrigger is the optional trigger capability the push-driven path uses:
// a pass planned against locally observed pushed heads, without the sweep's
// idle gate. The daemon's review service implements it.
type PushTrigger interface {
	TriggerAutoPush(context.Context, domain.SessionID, domain.ReviewerHarness, map[string]string) (reviewcore.TriggerResult, error)
}

// Result describes whether an evaluation started a review and why it skipped
// or triggered.
type Result struct {
	Triggered bool
	Reason    string
}

// Coordinator evaluates auto-review policy and owns the periodic sweep.
type Coordinator struct {
	store         Store
	reviews       Trigger
	clock         func() time.Time
	idleThreshold time.Duration
	sweepInterval time.Duration
	logger        *slog.Logger

	// Push nudges: one evaluation goroutine per session, re-running while new
	// pushes arrive. pendingHeads holds branch→remote-tip facts not yet
	// consumed; pushedAt keeps the first unconsumed push instant for the
	// push→trigger delay log.
	rootCtx      context.Context
	nudgeMu      sync.Mutex
	pendingHeads map[domain.SessionID]map[string]string
	nudging      map[domain.SessionID]bool
	pushedAt     map[domain.SessionID]time.Time
}

// Config customizes coordinator timing and logging.
type Config struct {
	Clock         func() time.Time
	IdleThreshold time.Duration
	SweepInterval time.Duration
	Logger        *slog.Logger
}

// New constructs an auto-review coordinator.
func New(store Store, reviews Trigger, cfg Config) *Coordinator {
	c := &Coordinator{store: store, reviews: reviews, clock: cfg.Clock, idleThreshold: cfg.IdleThreshold, sweepInterval: cfg.SweepInterval, logger: cfg.Logger}
	if c.clock == nil {
		c.clock = time.Now
	}
	if c.idleThreshold <= 0 {
		c.idleThreshold = DefaultIdleThreshold
	}
	if c.sweepInterval <= 0 {
		c.sweepInterval = DefaultSweepInterval
	}
	if c.logger == nil {
		c.logger = slog.Default()
	}
	return c
}

// EvaluateSession evaluates one worker against the current project, activity,
// PR, and review-run facts.
func (c *Coordinator) EvaluateSession(ctx context.Context, id domain.SessionID) (Result, error) {
	return c.evaluate(ctx, id, nil, false)
}

// Nudge schedules a push-driven evaluation of one worker: the daemon observed
// the worker's branch land on the remote (branch at remote tip sha). A push is
// itself the freshness proof, so the sweep's idle gate does not apply and the
// trigger plans against the locally observed head — no SCM observer round trip
// sits between the push and the review. Evaluations are single-flight per
// session; pushes arriving mid-evaluation rerun it. The periodic sweep stays
// the fallback for pushes this daemon cannot observe.
func (c *Coordinator) Nudge(sessionID domain.SessionID, branch, sha string) {
	c.nudgeMu.Lock()
	if c.pendingHeads == nil {
		c.pendingHeads = map[domain.SessionID]map[string]string{}
	}
	if c.nudging == nil {
		c.nudging = map[domain.SessionID]bool{}
	}
	if c.pushedAt == nil {
		c.pushedAt = map[domain.SessionID]time.Time{}
	}
	heads := c.pendingHeads[sessionID]
	if heads == nil {
		heads = map[string]string{}
		c.pendingHeads[sessionID] = heads
	}
	if branch != "" && sha != "" {
		heads[branch] = sha
	}
	if _, started := c.nudging[sessionID]; started {
		c.nudgeMu.Unlock()
		return
	}
	c.nudging[sessionID] = true
	if _, ok := c.pushedAt[sessionID]; !ok {
		c.pushedAt[sessionID] = c.clock()
	}
	ctx := c.rootCtx
	if ctx == nil {
		ctx = context.Background()
	}
	c.nudgeMu.Unlock()
	go c.nudgeLoop(ctx, sessionID)
}

func (c *Coordinator) nudgeLoop(ctx context.Context, id domain.SessionID) {
	for {
		c.nudgeMu.Lock()
		heads := c.pendingHeads[id]
		delete(c.pendingHeads, id)
		pushAt := c.pushedAt[id]
		c.nudgeMu.Unlock()
		c.evaluatePush(ctx, id, heads, pushAt)
		c.nudgeMu.Lock()
		if len(c.pendingHeads[id]) == 0 {
			delete(c.nudging, id)
			delete(c.pushedAt, id)
			c.nudgeMu.Unlock()
			return
		}
		c.nudgeMu.Unlock()
	}
}

func (c *Coordinator) evaluatePush(ctx context.Context, id domain.SessionID, heads map[string]string, pushAt time.Time) {
	result, err := c.evaluate(ctx, id, heads, true)
	if err != nil {
		c.logger.Error("auto-review: push-driven evaluation failed", "session_id", id, "err", err)
		return
	}
	if result.Triggered {
		c.logger.Info("auto-review: push-driven review started",
			"session_id", id,
			"push_to_trigger_ms", c.clock().Sub(pushAt).Milliseconds())
		return
	}
	c.logger.Debug("auto-review: push-driven evaluation skipped", "session_id", id, "reason", result.Reason)
}

// evaluate is the shared eligibility body. With push set, the idle gate does
// not apply and the trigger plans against the pushed local heads.
func (c *Coordinator) evaluate(ctx context.Context, id domain.SessionID, heads map[string]string, push bool) (Result, error) {
	session, ok, err := c.store.GetSession(ctx, id)
	if err != nil || !ok {
		if err == nil {
			return Result{Reason: "session_not_found"}, nil
		}
		return Result{}, err
	}
	gate := sessionGate(session, c.clock(), c.idleThreshold)
	if push {
		gate = pushSessionGate(session)
	}
	if gate != "" {
		return Result{Reason: gate}, nil
	}
	project, ok, err := c.store.GetProject(ctx, string(session.ProjectID))
	if err != nil || !ok {
		if err == nil {
			return Result{Reason: "missing_reviewer_harness"}, nil
		}
		return Result{}, err
	}
	harness := effectiveReviewerHarness(session, project.Config)
	if harness == "" {
		return Result{Reason: "missing_reviewer_harness"}, nil
	}
	prs, err := c.store.ListPRsBySession(ctx, id)
	if err != nil {
		return Result{}, err
	}
	reviewcore.ApplyLocalHeads(prs, heads)
	runs, err := c.store.ListReviewRunsBySession(ctx, id)
	if err != nil {
		return Result{}, err
	}
	harnessRuns := make([]domain.ReviewRun, 0, len(runs))
	for _, run := range runs {
		if run.Harness == harness || run.Harness == "" {
			harnessRuns = append(harnessRuns, run)
		}
	}
	states := reviewcore.Plan(prs, harnessRuns)
	if len(states) == 0 {
		return Result{Reason: "no_pr"}, nil
	}
	eligible := false
	hasRunning := false
	reason := "planner_ineligible"
	for _, state := range states {
		switch state.Status {
		case reviewcore.ReviewStateRunning:
			hasRunning = true
			reason = "review_running"
		case reviewcore.ReviewStateUpToDate:
			reason = "already_approved"
		case reviewcore.ReviewStateChangesRequested:
			reason = "changes_requested_same_sha"
		case reviewcore.ReviewStateIneligible:
			reason = ineligibleReason(prs, state.PRURL)
		case reviewcore.ReviewStateNeedsReview:
			if blocked := existingHeadReason(harnessRuns, state.PRURL, state.TargetSHA); blocked != "" {
				reason = blocked
				continue
			}
			eligible = true
		}
	}
	if !eligible && !hasRunning {
		return Result{Reason: reason}, nil
	}
	var result reviewcore.TriggerResult
	if push {
		pushTrigger, ok := c.reviews.(PushTrigger)
		if !ok {
			return Result{Reason: "push_trigger_unavailable"}, nil
		}
		result, err = pushTrigger.TriggerAutoPush(ctx, id, harness, heads)
	} else {
		result, err = c.reviews.TriggerAuto(ctx, id, harness)
	}
	if err != nil {
		return Result{}, fmt.Errorf("trigger auto review for %s: %w", id, err)
	}
	if result.SkipReason != "" {
		return Result{Reason: result.SkipReason}, nil
	}
	if !result.Created {
		return Result{Reason: triggerResultReason(prs, result, harness)}, nil
	}
	return Result{Triggered: true, Reason: "triggered"}, nil
}

func triggerResultReason(prs []domain.PullRequest, result reviewcore.TriggerResult, harness domain.ReviewerHarness) string {
	for _, state := range result.Reviews {
		if state.Status == reviewcore.ReviewStateRunning {
			return "review_running"
		}
	}
	runs := make([]domain.ReviewRun, 0, len(result.Runs))
	for _, run := range result.Runs {
		if run.Harness == harness || run.Harness == "" {
			runs = append(runs, run)
		}
	}
	for _, pr := range prs {
		if blocked := existingHeadReason(runs, pr.URL, pr.HeadSHA); blocked != "" {
			return blocked
		}
	}
	return "planner_up_to_date"
}

func existingHeadReason(runs []domain.ReviewRun, prURL, targetSHA string) string {
	// The newest cancel for this SHA decides its veto: a reviewer kill is an
	// explicit force-restart — the user destroyed a (typically hung) pane
	// expecting a fresh pass — so it must not block auto-review for this SHA.
	// User cancels and worker teardowns keep their veto (#3338).
	newestCancelled := -1
	for i := range runs {
		run := &runs[i]
		if run.PRURL != prURL || run.TargetSHA != targetSHA || run.Status != domain.ReviewRunCancelled {
			continue
		}
		if newestCancelled < 0 || run.CreatedAt.After(runs[newestCancelled].CreatedAt) {
			newestCancelled = i
		}
	}
	if newestCancelled >= 0 && runs[newestCancelled].Body != domain.ReviewRunCancelledByKill {
		return "cancelled_same_sha"
	}
	failedAutoRuns := 0
	for _, run := range runs {
		if run.PRURL != prURL || run.TargetSHA != targetSHA {
			continue
		}
		if run.Status == domain.ReviewRunRunning {
			return "review_running"
		}
		if run.Status == domain.ReviewRunCancelled {
			continue
		}
		if run.Verdict == domain.VerdictApproved {
			return "already_approved"
		}
		if run.Verdict == domain.VerdictChangesRequested {
			return "changes_requested_same_sha"
		}
		if run.Status == domain.ReviewRunFailed && run.TriggerSource == domain.ReviewTriggerAuto {
			failedAutoRuns++
		}
	}
	if failedAutoRuns >= autoReviewFailedRetryLimit {
		return "failed_same_sha_retry_limit"
	}
	return ""
}

func effectiveReviewerHarness(session domain.SessionRecord, config domain.ProjectConfig) domain.ReviewerHarness {
	if session.ReviewerHarness != "" {
		return session.ReviewerHarness
	}
	return config.ResolveReviewerHarness(session.Harness)
}

func ineligibleReason(prs []domain.PullRequest, url string) string {
	for _, pr := range prs {
		if pr.URL != url {
			continue
		}
		switch {
		case pr.Draft:
			return "draft_pr"
		case pr.Merged:
			return "merged_pr"
		case pr.Closed:
			return "closed_pr"
		case pr.HeadSHA == "":
			return "missing_head_sha"
		default:
			return "planner_ineligible"
		}
	}
	return "planner_ineligible"
}

func sessionGate(session domain.SessionRecord, now time.Time, threshold time.Duration) string {
	if !session.AutoReviewEnabled {
		return "disabled"
	}
	if session.Kind != domain.KindWorker {
		return "not_worker"
	}
	if session.IsTerminated {
		return "terminated"
	}
	if session.Activity.State != domain.ActivityIdle {
		return "not_idle"
	}
	if session.Activity.LastActivityAt.IsZero() || now.Sub(session.Activity.LastActivityAt) < threshold {
		return "idle_threshold_not_met"
	}
	return ""
}

// pushSessionGate is the eligibility gate for a push-driven evaluation. The
// push is the freshness proof, so the sweep's idle and idle-threshold checks
// do not apply; enabled, kind, and liveness stay enforced.
func pushSessionGate(session domain.SessionRecord) string {
	if !session.AutoReviewEnabled {
		return "disabled"
	}
	if session.Kind != domain.KindWorker {
		return "not_worker"
	}
	if session.IsTerminated {
		return "terminated"
	}
	return ""
}

// Sweep evaluates every known session, isolating per-session failures.
func (c *Coordinator) Sweep(ctx context.Context) error {
	sessions, err := c.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if reason := sessionGate(session, c.clock(), c.idleThreshold); reason != "" {
			continue
		}
		if _, err := c.EvaluateSession(ctx, session.ID); err != nil {
			c.logger.Error("auto-review: evaluate session failed", "session_id", session.ID, "err", err)
		}
	}
	return nil
}

// Start evaluates persisted session and PR facts on the configured cadence
// until ctx is cancelled. The context also bounds push-driven evaluations.
func (c *Coordinator) Start(ctx context.Context) <-chan struct{} {
	c.nudgeMu.Lock()
	c.rootCtx = ctx
	c.nudgeMu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(c.sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.Sweep(ctx); err != nil {
					c.logger.Error("auto-review: sweep failed", "err", err)
				}
			}
		}
	}()
	return done
}
