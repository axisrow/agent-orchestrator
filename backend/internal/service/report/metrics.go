package report

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DeliveryMetricsWindow is the rolling window the delivery metrics report on:
// outcome shares and delivery ages of reports created inside the window, plus
// the all-time picture for history questions.
const DeliveryMetricsWindow = 7 * 24 * time.Hour

// DeliveryMetrics is one cohort's delivery outcome distribution, computed
// from the append-only delivery event journal (fork issue #242, «строгий
// учёт»). Reports is the cohort size: deliveries whose first journal event
// falls inside the cohort's span. Acknowledged, Stale, and Open partition it;
// Superseded is the stale share whose terminal cause is reprompt_superseded.
type DeliveryMetrics struct {
	Reports      int
	Acknowledged int
	Stale        int
	Superseded   int
	Open         int
	// DeliveryRatePct, StaleSharePct, and SupersededSharePct are the shares of
	// the cohort, rounded to one decimal.
	DeliveryRatePct    float64
	StaleSharePct      float64
	SupersededSharePct float64
	// Delivery ages (creation → acknowledgment) of the delivered share:
	// median and 95th percentile seconds, zero when nothing delivered.
	MedianDeliveryAgeSeconds float64
	P95DeliveryAgeSeconds    float64
	// StaleByCause partitions the stale share by terminal cause class.
	StaleByCause map[string]int
}

// DeliveryProjectMetrics is one project's slice of the window cohort.
type DeliveryProjectMetrics struct {
	ProjectID       string
	Reports         int
	Acknowledged    int
	Stale           int
	Superseded      int
	DeliveryRatePct float64
}

// DeliveryMetricsReport answers the «почему/куда/сколько» questions from the
// journal: the rolling-window cohort, the all-time cohort, and the window's
// per-project breakdown.
type DeliveryMetricsReport struct {
	Window     DeliveryMetrics
	AllTime    DeliveryMetrics
	PerProject []DeliveryProjectMetrics
}

// deliveryEventReader is the metrics read slice of the store.
type deliveryEventReader interface {
	ListDeliveryEventsSince(ctx context.Context, since time.Time) ([]domain.DeliveryEvent, error)
}

// DeliveryMetrics computes the delivery metrics from the journal. Pure read:
// no delivery state changes.
func (s *Service) DeliveryMetrics(ctx context.Context) (DeliveryMetricsReport, error) {
	if s == nil || s.store == nil {
		return DeliveryMetricsReport{}, errors.New("report: store is required")
	}
	reader, ok := s.store.(deliveryEventReader)
	if !ok {
		return DeliveryMetricsReport{}, errors.New("report: delivery event reader is required")
	}
	events, err := reader.ListDeliveryEventsSince(ctx, time.Time{})
	if err != nil {
		return DeliveryMetricsReport{}, err
	}
	return ComputeDeliveryMetrics(events, s.now().UTC(), DeliveryMetricsWindow), nil
}

// deliveryOutcome is one delivery's journal-reconstructed fate.
type deliveryOutcome struct {
	projectID    string
	startedAt    time.Time
	outcome      string // last event's to_state
	cause        string // terminal event's cause when stale
	ageAtAck     time.Duration
	acknowledged bool
}

// ComputeDeliveryMetrics reconstructs every delivery's fate from its event
// chain and folds the chains into the window, all-time, and per-project
// metrics. Events must arrive in id order (the store guarantees it).
func ComputeDeliveryMetrics(events []domain.DeliveryEvent, now time.Time, window time.Duration) DeliveryMetricsReport {
	byDelivery := map[string]*deliveryOutcome{}
	order := make([]string, 0, len(events))
	for _, event := range events {
		outcome, seen := byDelivery[event.DeliveryID]
		if !seen {
			outcome = &deliveryOutcome{projectID: string(event.ProjectID), startedAt: event.CreatedAt, outcome: event.ToState}
			byDelivery[event.DeliveryID] = outcome
			order = append(order, event.DeliveryID)
		}
		outcome.outcome = event.ToState
		outcome.cause = event.Cause
		if event.Cause == domain.DeliveryEventAcknowledged && !outcome.acknowledged {
			outcome.acknowledged = true
			outcome.ageAtAck = event.CreatedAt.Sub(outcome.startedAt)
			if outcome.ageAtAck < 0 {
				outcome.ageAtAck = 0
			}
		}
	}
	windowStart := now.Add(-window)
	all := foldDeliveryMetrics(pickDeliveryOutcomes(byDelivery, order, time.Time{}))
	win := foldDeliveryMetrics(pickDeliveryOutcomes(byDelivery, order, windowStart))
	return DeliveryMetricsReport{Window: win, AllTime: all, PerProject: projectDeliveryMetrics(byDelivery, order, windowStart)}
}

// pickDeliveryOutcomes selects the cohort whose first journal event falls at
// or after since (zero time selects everything).
func pickDeliveryOutcomes(byDelivery map[string]*deliveryOutcome, order []string, since time.Time) []*deliveryOutcome {
	out := make([]*deliveryOutcome, 0, len(order))
	for _, id := range order {
		if outcome := byDelivery[id]; since.IsZero() || !outcome.startedAt.Before(since) {
			out = append(out, outcome)
		}
	}
	return out
}

func foldDeliveryMetrics(cohort []*deliveryOutcome) DeliveryMetrics {
	metrics := DeliveryMetrics{StaleByCause: map[string]int{}}
	ages := make([]float64, 0, len(cohort))
	for _, outcome := range cohort {
		metrics.Reports++
		switch outcome.outcome {
		case string(domain.ReportAcknowledged):
			metrics.Acknowledged++
			ages = append(ages, outcome.ageAtAck.Seconds())
		case string(domain.ReportStale):
			metrics.Stale++
			if outcome.cause == domain.DeliveryEventRepromptSuperseded {
				metrics.Superseded++
			}
			metrics.StaleByCause[outcome.cause]++
		default:
			metrics.Open++
		}
	}
	if metrics.Reports == 0 {
		return metrics
	}
	metrics.DeliveryRatePct = sharePct(metrics.Acknowledged, metrics.Reports)
	metrics.StaleSharePct = sharePct(metrics.Stale, metrics.Reports)
	metrics.SupersededSharePct = sharePct(metrics.Superseded, metrics.Reports)
	sort.Float64s(ages)
	metrics.MedianDeliveryAgeSeconds = percentile(ages, 50)
	metrics.P95DeliveryAgeSeconds = percentile(ages, 95)
	return metrics
}

// projectDeliveryMetrics folds the window cohort per project, newest-project
// order by id for stable output.
func projectDeliveryMetrics(byDelivery map[string]*deliveryOutcome, order []string, windowStart time.Time) []DeliveryProjectMetrics {
	index := map[string]int{}
	out := []DeliveryProjectMetrics{}
	for _, id := range order {
		outcome := byDelivery[id]
		if outcome.startedAt.Before(windowStart) {
			continue
		}
		at, seen := index[outcome.projectID]
		if !seen {
			at = len(out)
			index[outcome.projectID] = at
			out = append(out, DeliveryProjectMetrics{ProjectID: outcome.projectID})
		}
		project := &out[at]
		project.Reports++
		switch outcome.outcome {
		case string(domain.ReportAcknowledged):
			project.Acknowledged++
		case string(domain.ReportStale):
			project.Stale++
			if outcome.cause == domain.DeliveryEventRepromptSuperseded {
				project.Superseded++
			}
		}
	}
	for i := range out {
		if out[i].Reports > 0 {
			out[i].DeliveryRatePct = sharePct(out[i].Acknowledged, out[i].Reports)
		}
	}
	return out
}

// sharePct is a share of the cohort in percent, rounded to one decimal.
func sharePct(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(part)/float64(total)*1000) / 10
}

// percentile is the nearest-rank percentile of a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Floor(p / 100 * float64(len(sorted)-1)))
	return math.Round(sorted[idx]*10) / 10
}
