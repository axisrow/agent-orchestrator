package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	reportsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/report"
)

type fakeReportService struct {
	input   reportsvc.CreateInput
	err     error
	reports []domain.ReportRecord
	stuck   []reportsvc.StuckDeliveryGroup
	metrics reportsvc.DeliveryMetricsReport
}

func (f *fakeReportService) ListProject(_ context.Context, projectID domain.ProjectID) ([]domain.ReportRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]domain.ReportRecord(nil), f.reports...), nil
}

func (f *fakeReportService) Create(_ context.Context, input reportsvc.CreateInput) (domain.ReportRecord, error) {
	f.input = input
	if f.err != nil {
		return domain.ReportRecord{}, f.err
	}
	return domain.ReportRecord{ID: "rpt_1", SessionID: input.SessionID, ProjectID: "ao", State: input.State, Note: input.Note, Message: input.Message, Outputs: input.Outputs, CreatedAt: time.Unix(1, 0).UTC(), DeliveryState: domain.ReportPending}, nil
}

func TestReportsAPI_CreateAndEnvelope(t *testing.T) {
	svc := &fakeReportService{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Reports: svc}, httpd.ControlDeps{}))
	defer srv.Close()
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/reports", `{"sessionId":"ao-7","state":"done","note":"finished","outputs":[{"kind":"artifact","reference":"result","label":"Result"},{"kind":"pr_reviewed","reference":"https://github.com/o/r/pull/7"}]}`)
	if status != http.StatusCreated || svc.input.SessionID != "ao-7" || svc.input.State != domain.ReportDone || svc.input.Note != "finished" || len(svc.input.Outputs) != 2 || svc.input.Outputs[0].Label != "Result" || svc.input.Outputs[1].Kind != domain.ReportOutputPRReviewed {
		t.Fatalf("status=%d body=%s svc=%+v", status, body, svc)
	}
	if string(body) != `{"id":"rpt_1"}`+"\n" {
		t.Fatalf("response body = %s", body)
	}
	svc.err = apierr.Invalid("INVALID_REPORT", "bad report", nil)
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/reports", `{"sessionId":"ao-7","state":"done","note":"x"}`)
	if status != http.StatusBadRequest || !reportContainsAll(string(body), "INVALID_REPORT", "requestId") {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

func TestReportsAPI_InvalidJSON(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Reports: &fakeReportService{}}, httpd.ControlDeps{}))
	defer srv.Close()
	_, status, _ := doRequest(t, srv, "POST", "/api/v1/reports", `{"unknown":true}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d", status)
	}
}

func TestReportsAPI_ListIsReadOnlyProjection(t *testing.T) {
	created := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	available := created.Add(time.Hour)
	svc := &fakeReportService{reports: []domain.ReportRecord{{
		ID: "rpt_1", SessionID: "worker", ProjectID: "ao", State: domain.ReportDone,
		Note: "finished", CreatedAt: created, RepeatCount: 2,
		Outputs:       []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: "opaque"}},
		DeliveryState: domain.ReportPending, DeliveryAttempts: 3, AvailableAt: available, LastError: "no active orchestrator",
	}}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Reports: svc}, httpd.ControlDeps{}))
	defer srv.Close()
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/reports?projectId=ao", "")
	if status != http.StatusOK || !reportContainsAll(string(body), `"id":"rpt_1"`, `"sessionId":"worker"`, `"reference":"opaque"`, `"repeatCount":2`,
		`"deliveryState":"pending"`, `"deliveryAttempts":3`, `"lastError":"no active orchestrator"`) {
		t.Fatalf("status=%d body=%s", status, body)
	}
	_, status, _ = doRequest(t, srv, "GET", "/api/v1/reports", "")
	if status != http.StatusBadRequest {
		t.Fatalf("missing project status=%d", status)
	}
}

func reportContainsAll(s string, values ...string) bool {
	for _, v := range values {
		if !strings.Contains(s, v) {
			return false
		}
	}
	return true
}

func (f *fakeReportService) StuckDeliveries(_ context.Context) ([]reportsvc.StuckDeliveryGroup, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.stuck, nil
}

// The stuck-delivery visibility section (fork issue #211): one line per
// (project, cause-class) group, terminal stale groups separate from live
// retries, and an empty ledger is a healthy answer.
func TestReportsAPI_ListStuckDeliveries(t *testing.T) {
	svc := &fakeReportService{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Reports: svc}, httpd.ControlDeps{}))
	defer srv.Close()

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/reports/deliveries/stuck", "")
	if status != http.StatusOK || strings.TrimSpace(string(body)) != `{"groups":[]}` {
		t.Fatalf("status=%d body=%s, want empty groups", status, body)
	}
	svc.stuck = []reportsvc.StuckDeliveryGroup{
		{ProjectID: "cc", SessionID: "worker-1", Recipient: "no active orchestrator", CauseClass: "no active orchestrator",
			Cause: "project cc has no active orchestrator", Attempts: 3631, Age: 26 * time.Hour,
			OldestDeliveryAt: time.Unix(1700, 0).UTC(), Deliveries: 1, Zombie: true},
		{ProjectID: "q", SessionID: "worker-2", CauseClass: "archived project", Attempts: 40,
			Age: time.Hour, Deliveries: 2, Terminal: true, Zombie: true},
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/reports/deliveries/stuck", "")
	if status != http.StatusOK || !reportContainsAll(string(body),
		`"projectId":"cc"`, `"recipient":"no active orchestrator"`, `"causeClass":"no active orchestrator"`,
		`"attempts":3631`, `"zombie":true`, `"terminal":false`,
		`"projectId":"q"`, `"terminal":true`, `"deliveries":2`) {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

func (f *fakeReportService) DeliveryMetrics(_ context.Context) (reportsvc.DeliveryMetricsReport, error) {
	return f.metrics, nil
}

// The delivery-metrics section (fork issue #242): outcome shares and delivery
// ages from the journal, with the per-project breakdown, as one wire shape.
func TestReportsAPI_DeliveryMetrics(t *testing.T) {
	svc := &fakeReportService{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Reports: svc}, httpd.ControlDeps{}))
	defer srv.Close()

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/reports/deliveries/metrics", "")
	if status != http.StatusOK || strings.TrimSpace(string(body)) != `{"window":{"reports":0,"acknowledged":0,"stale":0,"superseded":0,"open":0,"deliveryRatePct":0,"staleSharePct":0,"supersededSharePct":0,"medianDeliveryAgeSeconds":0,"p95DeliveryAgeSeconds":0},"allTime":{"reports":0,"acknowledged":0,"stale":0,"superseded":0,"open":0,"deliveryRatePct":0,"staleSharePct":0,"supersededSharePct":0,"medianDeliveryAgeSeconds":0,"p95DeliveryAgeSeconds":0}}` {
		t.Fatalf("status=%d body=%s, want zeroed window and all-time", status, body)
	}

	svc.metrics = reportsvc.DeliveryMetricsReport{
		Window: reportsvc.DeliveryMetrics{
			Reports: 7, Acknowledged: 4, Stale: 2, Superseded: 1, Open: 1,
			DeliveryRatePct: 57.1, StaleSharePct: 28.6, SupersededSharePct: 14.3,
			MedianDeliveryAgeSeconds: 70, P95DeliveryAgeSeconds: 210,
			StaleByCause: map[string]int{"expired": 1, "reprompt_superseded": 1},
		},
		AllTime: reportsvc.DeliveryMetrics{Reports: 8, Acknowledged: 5, DeliveryRatePct: 62.5},
		PerProject: []reportsvc.DeliveryProjectMetrics{
			{ProjectID: "cc", Reports: 5, Acknowledged: 4, DeliveryRatePct: 80},
		},
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/reports/deliveries/metrics", "")
	if status != http.StatusOK || !reportContainsAll(string(body),
		`"window":{"reports":7,"acknowledged":4,"stale":2,"superseded":1,"open":1`,
		`"deliveryRatePct":57.1`, `"medianDeliveryAgeSeconds":70`, `"p95DeliveryAgeSeconds":210`,
		`"staleByCause":{"expired":1,"reprompt_superseded":1}`,
		`"allTime":{"reports":8,"acknowledged":5,"stale":0,"superseded":0,"open":0,"deliveryRatePct":62.5`,
		`"perProject":[{"projectId":"cc","reports":5,"acknowledged":4,"stale":0,"superseded":0,"deliveryRatePct":80}]`) {
		t.Fatalf("status=%d body=%s", status, body)
	}
}
