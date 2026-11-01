package report

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Stuck delivery visibility thresholds (fork issue #211). A pending group is
// stuck once its attempts pass StuckAttemptThreshold or it has gone
// StuckAgeThreshold without success — age being the time since its oldest
// pending row was created, which is a conservative lower bound on how long the
// group has gone undelivered.
const (
	StuckAttemptThreshold = 10
	StuckAgeThreshold     = time.Hour
	// StuckEscalationAge re-opens one incident: a group still stuck a day later
	// earns exactly one follow-up entry, then silence until the cause changes
	// or the incident ends.
	StuckEscalationAge = 24 * time.Hour
	// StuckDeliverySweepInterval is the visibility sweep cadence. The sweep is
	// two bounded reads; crossing latency of a minute is far below the hour
	// threshold it exists to surface.
	StuckDeliverySweepInterval = time.Minute
)

// StuckDeliveryGroup is one (project, cause-class) group of stuck deliveries.
// Pending groups are live retries; stale groups are terminal — retired rows
// that will never be delivered and are reported separately so the two never
// blur (fork issue #208).
type StuckDeliveryGroup struct {
	ProjectID        domain.ProjectID
	SessionID        domain.SessionID // oldest delivery's session; the bell's navigation anchor
	Recipient        string           // resolved for display; empty while unresolved
	CauseClass       string           // last_error folded to a stable class
	Cause            string           // newest raw last_error sample in the group
	Attempts         int64
	Age              time.Duration
	OldestDeliveryAt time.Time
	Deliveries       int
	Terminal         bool
	Zombie           bool
}

// ClassifyDeliveryCause folds a delivery's last_error into the stable class
// the doctor section groups by and the bell dedupes on. The zombie classes are
// the known never-going-to-deliver causes; everything else names itself, so an
// unseen failure mode still forms its own dedupe group instead of hiding
// inside a bucket that never delivers.
func ClassifyDeliveryCause(lastError string) (class string, zombie bool) {
	for _, sig := range []struct {
		needle string
		class  string
	}{
		{"no active orchestrator", "no active orchestrator"},
		{"project archived", "archived project"},
		{"repeat suppressed until emit TTL", "repeat suppressed until emit TTL"},
		// The #205 queue swallow, pre-#207 signature (acceptance never observed)
		// and post-#207 typed outcome (prompt held in the TUI queue) alike.
		{"prompt text is held in the target's message queue", "queued-input"},
		{"prompt submission was not observed", "queued-input"},
	} {
		if containsFold(lastError, sig.needle) {
			return sig.class, true
		}
	}
	if lastError == "" {
		return "unknown", false
	}
	if len(lastError) > 200 {
		return lastError[:200], false
	}
	return lastError, false
}

func containsFold(s, needle string) bool {
	if len(needle) > len(s) {
		return false
	}
	s, needle = foldASCII(s), foldASCII(needle)
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func foldASCII(s string) string {
	out := []byte(s)
	for i, b := range out {
		if b >= 'A' && b <= 'Z' {
			out[i] = b + ('a' - 'A')
		}
	}
	return string(out)
}

// GroupStuckDeliveries folds candidate rows into (project, cause-class) groups.
// Rows must come ordered by project, created_at, id (the store query's order),
// so each group's first row is its oldest delivery and its last row carries
// the newest error sample.
func GroupStuckDeliveries(rows []domain.ReportRecord, now time.Time) []StuckDeliveryGroup {
	type key struct {
		project domain.ProjectID
		class   string
	}
	index := map[key]int{}
	groups := make([]StuckDeliveryGroup, 0, len(rows))
	for _, row := range rows {
		terminal := row.DeliveryState == domain.ReportStale
		class, zombie := ClassifyDeliveryCause(row.LastError)
		at := key{row.ProjectID, class}
		// Terminal and pending rows never share a group: a stale row's cause and
		// a live retry's cause describe different facts even when the text matches.
		if terminal {
			at = key{row.ProjectID, class + "\x00terminal"}
		}
		if i, ok := index[at]; ok {
			g := &groups[i]
			g.Deliveries++
			if row.DeliveryAttempts > g.Attempts {
				g.Attempts = row.DeliveryAttempts
			}
			g.Cause = row.LastError
			continue
		}
		index[at] = len(groups)
		groups = append(groups, StuckDeliveryGroup{
			ProjectID:        row.ProjectID,
			SessionID:        row.SessionID,
			CauseClass:       class,
			Cause:            row.LastError,
			Attempts:         row.DeliveryAttempts,
			Age:              now.Sub(row.CreatedAt),
			OldestDeliveryAt: row.CreatedAt,
			Deliveries:       1,
			Terminal:         terminal,
			Zombie:           zombie,
		})
	}
	return groups
}

// deliveryRecipient names the group's delivery target for display: the
// project's active orchestrator, mirroring the coordinator's selection, or the
// absence the deliveries are actually failing with.
func deliveryRecipient(sessions []domain.SessionRecord) string {
	var selected domain.SessionRecord
	for _, rec := range sessions {
		if rec.Kind != domain.KindOrchestrator || rec.IsTerminated || rec.Activity.State == domain.ActivityExited {
			continue
		}
		if selected.ID == "" || rec.CreatedAt.After(selected.CreatedAt) {
			selected = rec
		}
	}
	if selected.ID == "" {
		return "no active orchestrator"
	}
	if name := selected.DisplayName; name != "" {
		return name
	}
	return string(selected.ID)
}

// stuckSourceKey is the incident dedupe key: one open entry per
// (project, cause class, age bracket), never per delivery id, so a #205-style
// avalanche of related deliveries is one entry (fork issue #211).
func stuckSourceKey(project domain.ProjectID, class string, age time.Duration) string {
	bracket := "open"
	if age > StuckEscalationAge {
		bracket = "escalated"
	}
	return "stuck-deliveries:" + string(project) + ":" + class + ":" + bracket
}

func terminalSourceKey(project domain.ProjectID, class string) string {
	return "delivery-died:" + string(project) + ":" + class
}

// stuckDeliveryReader is the persistence slice the doctor surface needs. The
// daemon's store satisfies it; the service type-asserts like ListProject does.
type stuckDeliveryReader interface {
	ListStuckDeliveryCandidates(ctx context.Context, minAttempts int64, ageCutoff time.Time) ([]domain.ReportRecord, error)
	ListSessions(ctx context.Context, projectID domain.ProjectID) ([]domain.SessionRecord, error)
}

// StuckDeliveries returns the current stuck and terminal delivery groups with
// their recipients resolved, for `ao doctor` and the diagnostics endpoint. It
// is a pure read: no delivery state changes.
func (s *Service) StuckDeliveries(ctx context.Context) ([]StuckDeliveryGroup, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("report: store is required")
	}
	reader, ok := s.store.(stuckDeliveryReader)
	if !ok {
		return nil, errors.New("report: stuck delivery reader is required")
	}
	now := s.now().UTC()
	rows, err := reader.ListStuckDeliveryCandidates(ctx, StuckAttemptThreshold, now.Add(-StuckAgeThreshold))
	if err != nil {
		return nil, err
	}
	groups := GroupStuckDeliveries(rows, now)
	sessions := map[domain.ProjectID]string{}
	for i := range groups {
		group := &groups[i]
		if _, done := sessions[group.ProjectID]; !done {
			recs, err := reader.ListSessions(ctx, group.ProjectID)
			if err != nil {
				return nil, err
			}
			sessions[group.ProjectID] = deliveryRecipient(recs)
		}
		group.Recipient = sessions[group.ProjectID]
	}
	return groups, nil
}

// StuckDeliveryStore reads the durable facts the visibility sweep judges.
type StuckDeliveryStore interface {
	ListStuckDeliveryCandidates(ctx context.Context, minAttempts int64, ageCutoff time.Time) ([]domain.ReportRecord, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// StuckDeliveryNotifier is the notification write side the sweep uses.
type StuckDeliveryNotifier interface {
	Notify(ctx context.Context, intent ports.NotificationIntent) error
	ResolveStuckDeliveries(ctx context.Context, at time.Time, keep map[string]bool) ([]domain.NotificationRecord, error)
}

// StuckDeliveryWatcher turns the deliveries ledger into edge-triggered bell
// events: one entry when a (project, cause-class) group crosses the stuck
// threshold, one follow-up on age escalation, resolution when the incident
// ends, and one terminal entry per retired group. It holds no state of its
// own — the notifications table's source-key dedupe is the incident memory.
type StuckDeliveryWatcher struct {
	store    StuckDeliveryStore
	notifier StuckDeliveryNotifier
	now      func() time.Time
	log      *slog.Logger
	interval time.Duration
}

// StuckDeliveryWatcherDeps configures a StuckDeliveryWatcher.
type StuckDeliveryWatcherDeps struct {
	Store    StuckDeliveryStore
	Notifier StuckDeliveryNotifier
	Now      func() time.Time
	Log      *slog.Logger
	// Interval overrides the sweep cadence in tests. Zero uses the default.
	Interval time.Duration
}

// NewStuckDeliveryWatcher constructs the visibility sweep.
func NewStuckDeliveryWatcher(d StuckDeliveryWatcherDeps) *StuckDeliveryWatcher {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.Interval <= 0 {
		d.Interval = StuckDeliverySweepInterval
	}
	return &StuckDeliveryWatcher{store: d.Store, notifier: d.Notifier, now: d.Now, log: d.Log, interval: d.Interval}
}

// Start runs the sweep until ctx is cancelled and returns its completion signal.
func (w *StuckDeliveryWatcher) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Sweep once at startup so rows that went stuck (or terminal) while the
		// daemon was down are surfaced without waiting a full interval.
		w.sweepQuietly(ctx)
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.sweepQuietly(ctx)
			}
		}
	}()
	return done
}

func (w *StuckDeliveryWatcher) sweepQuietly(ctx context.Context) {
	if err := w.Sweep(ctx); err != nil && ctx.Err() == nil {
		w.log.Warn("report: stuck delivery sweep failed", "err", err)
	}
}

// Sweep evaluates the ledger once: notify threshold crossings, resolve ended
// incidents, and record terminal outcomes. Errors abort the pass; the next
// tick re-reads everything, so a partial sweep converges.
func (w *StuckDeliveryWatcher) Sweep(ctx context.Context) error {
	if w == nil || w.store == nil || w.notifier == nil {
		return errors.New("stuck delivery watcher dependencies are required")
	}
	now := w.now().UTC()
	rows, err := w.store.ListStuckDeliveryCandidates(ctx, StuckAttemptThreshold, now.Add(-StuckAgeThreshold))
	if err != nil {
		return err
	}
	groups := GroupStuckDeliveries(rows, now)
	keep := make(map[string]bool, len(groups))
	for _, group := range groups {
		if group.Terminal {
			continue
		}
		keep[stuckSourceKey(group.ProjectID, group.CauseClass, group.Age)] = true
		if err := w.notifyStuck(ctx, group, now); err != nil {
			return err
		}
	}
	if _, err := w.notifier.ResolveStuckDeliveries(ctx, now, keep); err != nil {
		return err
	}
	for _, group := range groups {
		if !group.Terminal {
			continue
		}
		if err := w.notifyDied(ctx, group, now); err != nil {
			return err
		}
	}
	return nil
}

func (w *StuckDeliveryWatcher) notifyStuck(ctx context.Context, group StuckDeliveryGroup, now time.Time) error {
	return w.notifier.Notify(ctx, ports.NotificationIntent{
		Type:      domain.NotificationStuckDelivery,
		SessionID: group.SessionID,
		ProjectID: group.ProjectID,
		CreatedAt: now,
		SourceKey: stuckSourceKey(group.ProjectID, group.CauseClass, group.Age),
		Detail:    w.describe(ctx, group),
	})
}

func (w *StuckDeliveryWatcher) notifyDied(ctx context.Context, group StuckDeliveryGroup, now time.Time) error {
	return w.notifier.Notify(ctx, ports.NotificationIntent{
		Type:      domain.NotificationDeliveryDied,
		SessionID: group.SessionID,
		ProjectID: group.ProjectID,
		CreatedAt: now,
		SourceKey: terminalSourceKey(group.ProjectID, group.CauseClass),
		Detail:    w.describe(ctx, group),
	})
}

// describe renders the one-line incident fact both surfaces share: project,
// cause class, attempts, and how long the group went without success.
func (w *StuckDeliveryWatcher) describe(ctx context.Context, group StuckDeliveryGroup) string {
	name := string(group.ProjectID)
	if project, ok, err := w.store.GetProject(ctx, string(group.ProjectID)); err == nil && ok && project.DisplayName != "" {
		name = project.DisplayName
	}
	state := "failing for"
	if group.Terminal {
		state = "retired after"
	}
	return fmt.Sprintf("%s: %s - %d attempt(s), %d delivery(ies), %s %s", name, group.CauseClass, group.Attempts, group.Deliveries, state, group.Age.Truncate(time.Second))
}
