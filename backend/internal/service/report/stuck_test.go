package report

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestClassifyDeliveryCause(t *testing.T) {
	cases := []struct {
		lastError string
		class     string
		zombie    bool
	}{
		{"project p has no active orchestrator", "no active orchestrator", true},
		{"project archived: no report recipient can exist (fork issue #208)", "archived project", true},
		{"report batch already emitted; repeat suppressed until emit TTL", "repeat suppressed until emit TTL", true},
		{"session: prompt text is held in the target's message queue", "queued-input", true},
		{"session: semantic message acceptance unsupported: prompt submission was not observed", "queued-input", true},
		{"", "unknown", false},
		{"delivery claim recovered after daemon restart", "delivery claim recovered after daemon restart", false},
	}
	for _, tc := range cases {
		class, zombie := ClassifyDeliveryCause(tc.lastError)
		if class != tc.class || zombie != tc.zombie {
			t.Fatalf("ClassifyDeliveryCause(%q) = (%q, %v), want (%q, %v)", tc.lastError, class, zombie, tc.class, tc.zombie)
		}
	}
	long := strings.Repeat("x", 300)
	class, _ := ClassifyDeliveryCause(long)
	if len(class) != 200 {
		t.Fatalf("long cause class length = %d, want 200", len(class))
	}
}

func stuckRow(id string, project domain.ProjectID, created time.Time, attempts int64, lastError string, terminal bool) domain.ReportRecord {
	state := domain.ReportPending
	if terminal {
		state = domain.ReportStale
	}
	return domain.ReportRecord{ID: id, SessionID: domain.SessionID("worker-" + id), ProjectID: project, CreatedAt: created, DeliveryState: state, DeliveryAttempts: attempts, LastError: lastError, RepeatCount: 1}
}

func TestGroupStuckDeliveries(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	rows := []domain.ReportRecord{
		stuckRow("old", "p1", now.Add(-2*time.Hour), 3631, "project p1 has no active orchestrator", false),
		stuckRow("new", "p1", now.Add(-time.Minute), 3, "project p1 has no active orchestrator", false),
		stuckRow("other", "p1", now.Add(-90*time.Minute), 12, "session: prompt text is held in the target's message queue", false),
		stuckRow("dead", "p2", now.Add(-50*time.Hour), 40, "project archived: no report recipient can exist (fork issue #208)", true),
	}
	groups := GroupStuckDeliveries(rows, now)
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3: %#v", len(groups), groups)
	}
	orch := groups[0]
	if orch.ProjectID != "p1" || orch.CauseClass != "no active orchestrator" || !orch.Zombie {
		t.Fatalf("orchestrator group = %#v", orch)
	}
	if orch.Attempts != 3631 || orch.Deliveries != 2 || orch.Age != 2*time.Hour || orch.SessionID != "worker-old" {
		t.Fatalf("orchestrator group facts = %#v", orch)
	}
	if orch.OldestDeliveryAt != now.Add(-2*time.Hour) || orch.Cause != "project p1 has no active orchestrator" {
		t.Fatalf("orchestrator group age/cause = %#v", orch)
	}
	queued := groups[1]
	if queued.CauseClass != "queued-input" || queued.Deliveries != 1 || queued.Attempts != 12 {
		t.Fatalf("queued group = %#v", queued)
	}
	dead := groups[2]
	if !dead.Terminal || dead.CauseClass != "archived project" || dead.Deliveries != 1 {
		t.Fatalf("terminal group = %#v", dead)
	}
}

type stuckStore struct {
	mu       sync.Mutex
	rows     []domain.ReportRecord
	project  domain.ProjectRecord
	sessions []domain.SessionRecord
}

func (s *stuckStore) ListStuckDeliveryCandidates(_ context.Context, _ int64, _ time.Time) ([]domain.ReportRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.ReportRecord(nil), s.rows...), nil
}

func (s *stuckStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	rec := s.project
	if rec.ID == "" {
		rec.ID = id
		rec.DisplayName = "Project " + id
	}
	return rec, true, nil
}

func (s *stuckStore) setRows(rows ...domain.ReportRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = rows
}

type stuckNotifier struct {
	mu       sync.Mutex
	proposed map[string]int // every Notify call: the keys the watcher proposed
	created  map[string]int // first sighting per key: mimics the store's source-key dedupe
	types    map[string]domain.NotificationType
	details  map[string]string
	keeps    []map[string]bool
	resolved int
}

func (n *stuckNotifier) Notify(_ context.Context, intent ports.NotificationIntent) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.created == nil {
		n.proposed = map[string]int{}
		n.created = map[string]int{}
		n.types = map[string]domain.NotificationType{}
		n.details = map[string]string{}
	}
	n.proposed[intent.SourceKey]++
	if n.proposed[intent.SourceKey] == 1 {
		n.created[intent.SourceKey] = 1
		n.types[intent.SourceKey] = intent.Type
		n.details[intent.SourceKey] = intent.Detail
	}
	return nil
}

func (n *stuckNotifier) ResolveStuckDeliveries(_ context.Context, _ time.Time, keep map[string]bool) ([]domain.NotificationRecord, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.keeps = append(n.keeps, keep)
	n.resolved++
	return nil, nil
}

func (n *stuckNotifier) creations() (distinct int, created map[string]int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	created = map[string]int{}
	for key, count := range n.created {
		created[key] = count
	}
	return len(n.created), created
}

func newStuckWatcher(now time.Time, store *stuckStore, notifier *stuckNotifier) *StuckDeliveryWatcher {
	return NewStuckDeliveryWatcher(StuckDeliveryWatcherDeps{
		Store: store, Notifier: notifier, Now: func() time.Time { return now },
	})
}

// The live #208 shape: one delivery failing "no active orchestrator" every
// minute. Crossing the threshold rings once; a thousand more failing attempts
// of the same cause stay silent; the incident key never changes.
func TestStuckWatcherEdgeTriggersOncePerIncident(t *testing.T) {
	now := time.Date(2026, 11, 1, 13, 26, 0, 0, time.UTC)
	store := &stuckStore{}
	notifier := &stuckNotifier{}
	w := newStuckWatcher(now, store, notifier)

	store.setRows(stuckRow("rpt_99959601", "ccstatusline", now.Add(-55*time.Minute), 55, "project ccstatusline has no active orchestrator", false))
	if err := w.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	distinct, keys := notifier.creations()
	if distinct != 1 {
		t.Fatalf("creations after crossing = %d, want 1: %#v", distinct, keys)
	}
	var key string
	for k := range keys {
		key = k
	}
	if !strings.HasPrefix(key, "stuck-deliveries:ccstatusline:no active orchestrator:open") {
		t.Fatalf("source key = %q", key)
	}
	// 100 more failing sweeps of the same cause: the watcher keeps proposing
	// exactly the same incident key, so the store-side dedupe yields no new
	// entries (the acceptance's "100 fails = still one entry").
	for i := 0; i < 100; i++ {
		if err := w.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	distinct, created := notifier.creations()
	if distinct != 1 || created[key] != 1 || notifier.proposed[key] != 101 {
		t.Fatalf("after 101 sweeps: distinct = %d, created[%q] = %d, proposed = %d, want 1/1/101", distinct, key, created[key], notifier.proposed[key])
	}
}

func TestStuckWatcherCauseChangeAndEscalationCreateNewEntries(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	store := &stuckStore{}
	notifier := &stuckNotifier{}
	w := newStuckWatcher(now, store, notifier)
	created := now.Add(-2 * time.Hour)
	store.setRows(stuckRow("a", "p", created, 30, "project p has no active orchestrator", false))
	if err := w.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Cause class change: a new incident key, one new entry.
	store.setRows(stuckRow("a", "p", created, 31, "session: prompt text is held in the target's message queue", false))
	if err := w.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	distinct, keys := notifier.creations()
	if distinct != 2 {
		t.Fatalf("creations after cause change = %d (%#v), want 2", distinct, keys)
	}
	// Age escalation past a day: exactly one follow-up entry, new bracket.
	// The row is 2h old here; 21h more keeps it inside the open bracket.
	now = now.Add(21 * time.Hour)
	if err := newStuckWatcher(now, store, notifier).Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	distinct, _ = notifier.creations()
	if distinct != 2 {
		t.Fatalf("creations before escalation = %d, want 2", distinct)
	}
	// Two more hours put the group past the day: one escalated follow-up.
	now = now.Add(2 * time.Hour)
	if err := newStuckWatcher(now, store, notifier).Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	distinct, createdKeys := notifier.creations()
	if distinct != 3 {
		t.Fatalf("creations after escalation = %d (%#v), want 3", distinct, createdKeys)
	}
	escalated := false
	for key := range createdKeys {
		if strings.HasSuffix(key, "queued-input:escalated") {
			escalated = true
		}
	}
	if !escalated {
		t.Fatalf("no escalated key for the surviving cause in %#v", created)
	}
}

func TestStuckWatcherResolvesOnSuccessAndRecordsDeathOnce(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	store := &stuckStore{}
	notifier := &stuckNotifier{}
	w := newStuckWatcher(now, store, notifier)
	created := now.Add(-2 * time.Hour)
	store.setRows(stuckRow("a", "p", created, 30, "project p has no active orchestrator", false))
	if err := w.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Success: the rows delivered, the group leaves the ledger, the sweep
	// resolves every stuck entry by proposing an empty keep set.
	store.setRows()
	if err := w.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notifier.keeps) == 0 || len(notifier.keeps[len(notifier.keeps)-1]) != 0 {
		t.Fatalf("final keep = %#v, want empty", notifier.keeps)
	}
	// Terminal retirement (the post-#208 shape): one death entry, no stuck key,
	// and silence on every later sighting of the same stale rows.
	stuckKeysBefore := 0
	for key := range notifier.created {
		if strings.HasPrefix(key, "stuck-deliveries:") {
			stuckKeysBefore++
		}
	}
	store.setRows(stuckRow("a", "p", created, 3631, "project archived: no report recipient can exist (fork issue #208)", true))
	for i := 0; i < 3; i++ {
		if err := w.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	deaths := 0
	var deathKey string
	for key := range notifier.created {
		if strings.HasPrefix(key, "delivery-died:") {
			deaths += notifier.created[key]
			deathKey = key
		}
	}
	if deaths != 1 {
		t.Fatalf("death creations = %d, want exactly 1", deaths)
	}
	if notifier.types[deathKey] != domain.NotificationDeliveryDied {
		t.Fatalf("death type = %v", notifier.types[deathKey])
	}
	// The death key is permanent dedupe: re-sighting stale rows proposes the
	// same key, which the store never turns into a second entry; terminal rows
	// also never produce stuck keys.
	stuckKeysAfter := 0
	for key := range notifier.created {
		if strings.HasPrefix(key, "stuck-deliveries:") {
			stuckKeysAfter++
		}
	}
	if stuckKeysAfter != stuckKeysBefore {
		t.Fatalf("stuck keys before/after terminal = %d/%d, terminal rows must not open stuck incidents", stuckKeysBefore, stuckKeysAfter)
	}
	if len(notifier.created) != stuckKeysAfter+1 {
		t.Fatalf("distinct keys = %d (%#v), want stuck + died", len(notifier.created), notifier.created)
	}
}

func TestStuckWatcherDetailsCarryProjectClassAttemptsAge(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	store := &stuckStore{}
	notifier := &stuckNotifier{}
	w := newStuckWatcher(now, store, notifier)
	store.setRows(stuckRow("a", "cc", now.Add(-time.Hour), 12, "project cc has no active orchestrator", false))
	if err := w.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, detail := range notifier.details {
		for _, want := range []string{"Project cc", "no active orchestrator", "12 attempt(s)", "1 delivery(ies)"} {
			if !strings.Contains(detail, want) {
				t.Fatalf("detail %q missing %q", detail, want)
			}
		}
	}
}

type stuckReaderStore struct {
	rows     []domain.ReportRecord
	sessions []domain.SessionRecord
}

func (s *stuckReaderStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{}, false, nil
}
func (s *stuckReaderStore) CreateReport(_ context.Context, rec domain.ReportRecord) (domain.ReportRecord, error) {
	return rec, nil
}
func (s *stuckReaderStore) ListStuckDeliveryCandidates(context.Context, int64, time.Time) ([]domain.ReportRecord, error) {
	return s.rows, nil
}
func (s *stuckReaderStore) ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error) {
	return s.sessions, nil
}

func TestServiceStuckDeliveriesResolvesRecipient(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	store := &stuckReaderStore{
		rows: []domain.ReportRecord{stuckRow("a", "p", now.Add(-2*time.Hour), 30, "project p has no active orchestrator", false)},
		sessions: []domain.SessionRecord{
			{ID: "old-orch", ProjectID: "p", Kind: domain.KindOrchestrator, IsTerminated: true, CreatedAt: now.Add(-time.Hour)},
			{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, CreatedAt: now.Add(-30 * time.Minute)},
			{ID: "worker", ProjectID: "p", Kind: domain.KindWorker, CreatedAt: now.Add(-time.Minute)},
		},
	}
	svc := New(Deps{Store: store, Now: func() time.Time { return now }})
	groups, err := svc.StuckDeliveries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Recipient != "orch" {
		t.Fatalf("groups = %#v, want one with recipient orch", groups)
	}
}
