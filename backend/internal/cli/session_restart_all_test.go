package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// restartAllServer serves a fixed session list plus kill/restore endpoints, and
// records the request sequence so tests can assert the exact round trips.
func restartAllServer(t *testing.T, listBody string) (*httptest.Server, *sessionRequestLog) {
	t.Helper()
	log := &sessionRequestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.append(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions":
			_, _ = io.WriteString(w, listBody)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/kill"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/"), "/kill")
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"`+id+`","freed":true}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/restore"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/"), "/restore")
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"`+id+`","session":{"id":"`+id+`","projectId":"demo"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

const restartAllListBody = `{"sessions":[
	{"id":"demo-1","projectId":"demo","kind":"worker","isTerminated":false},
	{"id":"demo-2","projectId":"demo","kind":"orchestrator","isTerminated":false},
	{"id":"demo-3","projectId":"demo","kind":"worker","isTerminated":false}
]}`

// TestRestartAll_SkipsOrchestratorsByDefault: killing an orchestrator interrupts
// the work it coordinates, so the default selection must cover workers only.
func TestRestartAll_SkipsOrchestratorsByDefault(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv, log := restartAllServer(t, restartAllListBody)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restart-all", "--yes", "--settle-delay", "0")
	if err != nil {
		t.Fatalf("restart-all failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "restarted 2 of 2 sessions") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	want := []string{
		"GET /api/v1/sessions?active=true",
		"POST /api/v1/sessions/demo-1/kill",
		"POST /api/v1/sessions/demo-1/restore",
		"POST /api/v1/sessions/demo-3/kill",
		"POST /api/v1/sessions/demo-3/restore",
	}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

// TestRestartAll_OrchestratorsOnly: the inverse selection, used to refresh the
// long-lived orchestrators without touching workers mid-task.
func TestRestartAll_OrchestratorsOnly(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv, log := restartAllServer(t, restartAllListBody)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restart-all", "--orchestrators-only", "--yes", "--settle-delay", "0"); err != nil {
		t.Fatalf("restart-all failed: %v\nstderr=%s", err, errOut)
	}
	want := []string{
		"GET /api/v1/sessions?active=true",
		"POST /api/v1/sessions/demo-2/kill",
		"POST /api/v1/sessions/demo-2/restore",
	}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

// TestRestartAll_ExcludesSelf: the session issuing the command must never be
// killed — the kill would take down the process that still has to send restore.
func TestRestartAll_ExcludesSelf(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "demo-1")
	srv, log := restartAllServer(t, restartAllListBody)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restart-all", "--yes", "--settle-delay", "0"); err != nil {
		t.Fatalf("restart-all failed: %v\nstderr=%s", err, errOut)
	}
	for _, req := range log.all() {
		if strings.Contains(req, "demo-1") {
			t.Fatalf("self session demo-1 must not be touched, got %#v", log.all())
		}
	}
}

// TestRestartAll_SelfFlagOverridesEnv: when the command runs outside a session
// (no AO_SESSION_ID), --self is the only way to protect the calling session.
func TestRestartAll_SelfFlagOverridesEnv(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv, log := restartAllServer(t, restartAllListBody)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restart-all", "--self", "demo-3", "--yes", "--settle-delay", "0"); err != nil {
		t.Fatalf("restart-all failed: %v\nstderr=%s", err, errOut)
	}
	for _, req := range log.all() {
		if strings.Contains(req, "demo-3") {
			t.Fatalf("--self demo-3 must not be touched, got %#v", log.all())
		}
	}
}

// TestRestartAll_WarnsWithoutSelfID: silently proceeding when the caller cannot
// be identified risks killing the very session running the command.
func TestRestartAll_WarnsWithoutSelfID(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv, _ := restartAllServer(t, restartAllListBody)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restart-all", "--dry-run")
	if err != nil {
		t.Fatalf("restart-all failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(errOut, "AO_SESSION_ID is not set") {
		t.Fatalf("expected a warning about the unidentified caller, got:\n%s", errOut)
	}
}

// TestRestartAll_DryRunTouchesNothing: the plan must be a pure read, so it can be
// run safely against a live fleet before committing to the restart.
func TestRestartAll_DryRunTouchesNothing(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv, log := restartAllServer(t, restartAllListBody)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restart-all", "--dry-run")
	if err != nil {
		t.Fatalf("restart-all failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "would restart 2 sessions") {
		t.Fatalf("unexpected dry-run output:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions?active=true"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("dry-run must not mutate; requests = %#v", got)
	}
}

// TestRestartAll_FailedRestoreIsReported: a session left terminated after a failed
// restore is the worst outcome of this command, so it must surface loudly and set
// a non-zero exit code rather than being buried in a success summary.
func TestRestartAll_FailedRestoreIsReported(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"sessions":[{"id":"demo-1","projectId":"demo","kind":"worker","isTerminated":false}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/kill":
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","freed":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/restore":
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"error":{"code":"SESSION_NOT_RESTORABLE","message":"not restorable"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restart-all", "--yes", "--settle-delay", "0")
	if err == nil {
		t.Fatalf("expected a non-nil error when a restore fails, output:\n%s", out)
	}
	if !strings.Contains(out, "failures:") || !strings.Contains(out, "demo-1") {
		t.Fatalf("failure must name the session left terminated, got:\n%s", out)
	}
}

// TestRestartAll_JSONOutput: the machine-readable shape is what a wrapper script
// consumes to spot sessions that need a manual restore.
func TestRestartAll_JSONOutput(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv, _ := restartAllServer(t, restartAllListBody)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restart-all", "--json", "--yes", "--settle-delay", "0")
	if err != nil {
		t.Fatalf("restart-all failed: %v\nstderr=%s", err, errOut)
	}

	var got restartAllOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if got.Meta.Total != 2 || got.Meta.Restarted != 2 || got.Meta.Failed != 0 {
		t.Fatalf("unexpected meta: %+v", got.Meta)
	}
	if len(got.Data) != 2 || got.Data[0].SessionID != "demo-1" || got.Data[0].Status != restartStatusRestarted {
		t.Fatalf("unexpected data: %+v", got.Data)
	}
}

// TestRestartAll_MutuallyExclusiveSelectors guards the one flag pair whose
// combination has no coherent meaning.
func TestRestartAll_MutuallyExclusiveSelectors(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv, _ := restartAllServer(t, restartAllListBody)
	writeRunFileFor(t, cfg, srv)

	if _, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restart-all", "--include-orchestrators", "--orchestrators-only"); err == nil {
		t.Fatal("expected a usage error for mutually exclusive selectors")
	}
}
