package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	reportsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/report"
)

type reportDeliverySession struct {
	sentID      domain.SessionID
	sentMessage string
	sentKey     string
	interrupted domain.SessionID
}

func (s *reportDeliverySession) SendSemantic(_ context.Context, id domain.SessionID, message, key string) error {
	s.sentID, s.sentMessage, s.sentKey = id, message, key
	return nil
}

func (s *reportDeliverySession) InterruptTUI(_ context.Context, id domain.SessionID) error {
	s.interrupted = id
	return nil
}

type reportDeliveryStore struct{ rec domain.SessionRecord }

func (s reportDeliveryStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return s.rec, true, nil
}

func TestReportSemanticDeliveryUsesAcceptedTUIBoundary(t *testing.T) {
	sessions := &reportDeliverySession{}
	delivery := reportSemanticDelivery{
		sessions: sessions,
		store: reportDeliveryStore{rec: domain.SessionRecord{
			ID: "ao-1", Mode: domain.SessionModeTUI,
		}},
	}
	if err := delivery.Submit(context.Background(), "ao-1", "report context", "report-batch:abc123"); err != nil {
		t.Fatal(err)
	}
	if sessions.sentID != "ao-1" || sessions.sentMessage != "report context" || sessions.sentKey != "report-batch:abc123" {
		t.Fatalf("semantic send = %+v", sessions)
	}
	if err := delivery.Interrupt(context.Background(), "ao-1"); err != nil {
		t.Fatal(err)
	}
	if sessions.interrupted != "ao-1" {
		t.Fatalf("interrupted = %q", sessions.interrupted)
	}
}

// TestReportExpiryAgeFromEnv pins the configurable unread expiry knob
// (fork issue #242): whole days override the default; garbage and non-positive
// values keep it.
func TestReportExpiryAgeFromEnv(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"", reportsvc.ReportExpiryAge},
		{"14", 14 * 24 * time.Hour},
		{" 3 ", 3 * 24 * time.Hour},
		{"0", reportsvc.ReportExpiryAge},
		{"-2", reportsvc.ReportExpiryAge},
		{"soon", reportsvc.ReportExpiryAge},
	}
	for _, tc := range cases {
		if got := reportExpiryAgeFromEnv(func(string) string { return tc.raw }); got != tc.want {
			t.Fatalf("reportExpiryAgeFromEnv(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}
