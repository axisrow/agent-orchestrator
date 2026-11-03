package report

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// metricsFixture builds journal chains for seven window-cohort reports and one
// all-time-only report: four acknowledged (two directly, one after a release
// retry), one expired, one superseded, one still open. Ages 10s/60s/200s/300s
// separate median from p95.
func metricsFixture(now time.Time) []domain.DeliveryEvent {
	emit := func(id string, project domain.ProjectID, at time.Time) []domain.DeliveryEvent {
		return []domain.DeliveryEvent{{
			DeliveryID: id, SessionID: "worker", ProjectID: project,
			FromState: "", ToState: string(domain.ReportPending),
			Cause: domain.DeliveryEventEmitted, CreatedAt: at,
		}}
	}
	ack := func(id string, project domain.ProjectID, at time.Time) domain.DeliveryEvent {
		return domain.DeliveryEvent{
			DeliveryID: id, SessionID: "worker", ProjectID: project,
			FromState: string(domain.ReportClaimed), ToState: string(domain.ReportAcknowledged),
			Cause: domain.DeliveryEventAcknowledged, CreatedAt: at,
		}
	}
	stale := func(id string, project domain.ProjectID, cause string, at time.Time) domain.DeliveryEvent {
		return domain.DeliveryEvent{
			DeliveryID: id, SessionID: "worker", ProjectID: project,
			FromState: string(domain.ReportPending), ToState: string(domain.ReportStale),
			Cause: cause, CreatedAt: at,
		}
	}
	cc := domain.ProjectID("cc")
	q := domain.ProjectID("q")
	a := emit("rpt_a", cc, now.Add(-10*time.Second))
	a = append(a,
		domain.DeliveryEvent{DeliveryID: "rpt_a", SessionID: "worker", ProjectID: cc, FromState: string(domain.ReportPending), ToState: string(domain.ReportClaimed), Cause: domain.DeliveryEventDeliveryAttempt, CreatedAt: now.Add(-9 * time.Second)},
		ack("rpt_a", cc, now))
	b := append(emit("rpt_b", cc, now.Add(-20*time.Second)), stale("rpt_b", cc, domain.DeliveryEventExpired, now.Add(-time.Second)))
	c := append(emit("rpt_c", q, now.Add(-30*time.Second)), stale("rpt_c", q, domain.DeliveryEventRepromptSuperseded, now.Add(-2*time.Second)))
	d := emit("rpt_d", q, now.Add(-40*time.Second))
	f := emit("rpt_f", cc, now.Add(-70*time.Second))
	f = append(f,
		domain.DeliveryEvent{DeliveryID: "rpt_f", SessionID: "worker", ProjectID: cc, FromState: string(domain.ReportPending), ToState: string(domain.ReportClaimed), Cause: domain.DeliveryEventDeliveryAttempt, CreatedAt: now.Add(-69 * time.Second)},
		domain.DeliveryEvent{DeliveryID: "rpt_f", SessionID: "worker", ProjectID: cc, FromState: string(domain.ReportClaimed), ToState: string(domain.ReportPending), Cause: domain.DeliveryEventReleased, Reason: "boom", CreatedAt: now.Add(-68 * time.Second)},
		domain.DeliveryEvent{DeliveryID: "rpt_f", SessionID: "worker", ProjectID: cc, FromState: string(domain.ReportPending), ToState: string(domain.ReportClaimed), Cause: domain.DeliveryEventDeliveryAttempt, CreatedAt: now.Add(-67 * time.Second)},
		ack("rpt_f", cc, now))
	g := append(emit("rpt_g", cc, now.Add(-210*time.Second)), ack("rpt_g", cc, now))
	h := append(emit("rpt_h", cc, now.Add(-310*time.Second)), ack("rpt_h", cc, now))
	old := emit("rpt_old", cc, now.Add(-30*24*time.Hour))
	old = append(old, ack("rpt_old", cc, now.Add(-30*24*time.Hour).Add(5*time.Second)))
	return slices.Concat(a, b, c, d, f, g, h, old)
}

// ComputeDeliveryMetrics folds journal chains into the delivery outcome
// distribution: cohort membership by first event, terminal causes, delivery
// ages, and the per-project breakdown (fork issue #242 acceptance).
func TestComputeDeliveryMetricsFoldsOutcomesFromChains(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	report := ComputeDeliveryMetrics(metricsFixture(now), now, DeliveryMetricsWindow)

	win := report.Window
	if win.Reports != 7 || win.Acknowledged != 4 || win.Stale != 2 || win.Superseded != 1 || win.Open != 1 {
		t.Fatalf("window = %+v", win)
	}
	if win.DeliveryRatePct != 57.1 || win.StaleSharePct != 28.6 || win.SupersededSharePct != 14.3 {
		t.Fatalf("window shares = %v/%v/%v", win.DeliveryRatePct, win.StaleSharePct, win.SupersededSharePct)
	}
	if win.StaleByCause[domain.DeliveryEventExpired] != 1 || win.StaleByCause[domain.DeliveryEventRepromptSuperseded] != 1 || len(win.StaleByCause) != 2 {
		t.Fatalf("window stale by cause = %+v", win.StaleByCause)
	}
	if win.MedianDeliveryAgeSeconds != 70 || win.P95DeliveryAgeSeconds != 210 {
		t.Fatalf("window ages = median %v p95 %v", win.MedianDeliveryAgeSeconds, win.P95DeliveryAgeSeconds)
	}

	all := report.AllTime
	if all.Reports != 8 || all.Acknowledged != 5 || all.Stale != 2 || all.Open != 1 {
		t.Fatalf("all time = %+v", all)
	}
	if all.DeliveryRatePct != 62.5 {
		t.Fatalf("all-time rate = %v", all.DeliveryRatePct)
	}
	if all.MedianDeliveryAgeSeconds != 70 || all.P95DeliveryAgeSeconds != 210 {
		t.Fatalf("all-time ages = median %v p95 %v", all.MedianDeliveryAgeSeconds, all.P95DeliveryAgeSeconds)
	}

	if len(report.PerProject) != 2 {
		t.Fatalf("per project = %+v", report.PerProject)
	}
	byProject := map[string]DeliveryProjectMetrics{}
	for _, project := range report.PerProject {
		byProject[project.ProjectID] = project
	}
	cc := byProject["cc"]
	q := byProject["q"]
	if cc.Reports != 5 || cc.Acknowledged != 4 || cc.DeliveryRatePct != 80 {
		t.Fatalf("cc = %+v", cc)
	}
	if q.Reports != 2 || q.Acknowledged != 0 || q.Stale != 1 || q.Superseded != 1 || q.DeliveryRatePct != 0 {
		t.Fatalf("q = %+v", q)
	}
}

func TestComputeDeliveryMetricsEmptyJournal(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	report := ComputeDeliveryMetrics(nil, now, DeliveryMetricsWindow)
	if report.Window.Reports != 0 || report.Window.DeliveryRatePct != 0 || report.AllTime.Reports != 0 || len(report.PerProject) != 0 {
		t.Fatalf("empty journal = %+v", report)
	}
}

type metricsStore struct {
	events []domain.DeliveryEvent
}

func (m *metricsStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{}, false, nil
}

func (m *metricsStore) CreateReport(context.Context, domain.ReportRecord) (domain.ReportRecord, error) {
	return domain.ReportRecord{}, nil
}

func (m *metricsStore) ListDeliveryEventsSince(context.Context, time.Time) ([]domain.DeliveryEvent, error) {
	return m.events, nil
}

// The service path reads the whole journal read-only and folds it through the
// same pure function.
func TestServiceDeliveryMetricsReadsTheJournal(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	store := &metricsStore{events: metricsFixture(now)}
	svc := New(Deps{Store: store, Now: func() time.Time { return now }})
	report, err := svc.DeliveryMetrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Window.Reports != 7 || report.AllTime.Reports != 8 {
		t.Fatalf("service metrics = %+v", report)
	}
}
