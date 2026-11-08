package httpd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// The usage hook-processing counters surface on /healthz so a wholesale hook
// outage cannot hide behind WARN spam while the state machine lies (#300).
func TestHealthzReportsUsageHookMetrics(t *testing.T) {
	router := NewRouterWithControl(config.Config{}, discardLogger(), nil, APIDeps{
		UsageHookMetrics: func() map[string]int64 {
			return map[string]int64{"hookFailures": 3, "hookArtifactsMissing": 12}
		},
	}, ControlDeps{})
	srv := httptest.NewServer(router)
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		UsageHooks map[string]int64 `json:"usageHooks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /healthz: %v", err)
	}
	if body.UsageHooks["hookFailures"] != 3 || body.UsageHooks["hookArtifactsMissing"] != 12 {
		t.Fatalf("usageHooks = %v, want the collector counters", body.UsageHooks)
	}
}

// A nil metrics source (usage collection disabled) keeps the probe payload
// free of the field rather than emitting an empty object.
func TestHealthzOmitsUsageHookMetricsWithoutASource(t *testing.T) {
	router := newTestRouter(config.Config{}, discardLogger(), nil)
	srv := httptest.NewServer(router)
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /healthz: %v", err)
	}
	if _, present := body["usageHooks"]; present {
		t.Fatal("usageHooks must be absent without a metrics source")
	}
}
