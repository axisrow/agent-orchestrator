package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
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
	}, "session", "restart-all", "--self", "-", "--yes", "--settle-delay", "0")
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
	}, "session", "restart-all", "--self", "-", "--orchestrators-only", "--yes", "--settle-delay", "0"); err != nil {
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
	}, "session", "restart-all", "--self", "-", "--yes", "--settle-delay", "0")
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
	}, "session", "restart-all", "--self", "-", "--json", "--yes", "--settle-delay", "0")
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

// TestRestartAll_SelfDashAcknowledgesNoSessionToProtect: --self - is the explicit
// "running outside AO, nothing needs protecting" acknowledgment. It must satisfy
// the identification requirement without being treated as a session id to exclude
// (there is no session literally named "-").
func TestRestartAll_SelfDashAcknowledgesNoSessionToProtect(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv, log := restartAllServer(t, restartAllListBody)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restart-all", "--self", "-", "--yes", "--settle-delay", "0")
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
		t.Fatalf("--self - must not exclude anything; requests = %#v, want %#v", got, want)
	}
}

// TestRestartAll_JSONWithoutYesRequiresConfirmation: --json must not be a way to
// bypass the destructive-action confirmation gate. Every other confirmation-guarded
// command in this package (project rm, session cleanup) gates on --yes alone, so
// restart-all must too — otherwise a script adding --json for parsing unexpectedly
// authorizes a fleet-wide kill+restore with no confirmation at all.
func TestRestartAll_JSONWithoutYesRequiresConfirmation(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv, log := restartAllServer(t, restartAllListBody)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
		In:           strings.NewReader(""),
	}, "session", "restart-all", "--json", "--settle-delay", "0")

	for _, req := range log.all() {
		if strings.Contains(req, "/kill") || strings.Contains(req, "/restore") {
			t.Fatalf("restart-all --json without --yes must not touch any session, but got request %q\nstdout=%s\nstderr=%s\nerr=%v", req, out, errOut, err)
		}
	}
}

// TestRestartAll_UnidentifiedSelfIsExcludedFromTargets: when neither --self nor
// AO_SESSION_ID can identify the caller, the command must fail closed rather than
// silently including that session in the kill+restore loop — a fail-open warning
// is not enough to prevent an unidentified session from restarting (and possibly
// stranding) itself.
func TestRestartAll_UnidentifiedSelfIsExcludedFromTargets(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv, log := restartAllServer(t, restartAllListBody)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restart-all", "--yes", "--settle-delay", "0")

	if err == nil {
		t.Fatalf("expected restart-all to refuse when the caller cannot be identified, got success\nstdout=%s", out)
	}
	for _, req := range log.all() {
		if strings.Contains(req, "/kill") || strings.Contains(req, "/restore") {
			t.Fatalf("restart-all must not kill/restore anything when self-identification fails, got request %q\nstdout=%s\nstderr=%s", req, out, errOut)
		}
	}
}

// TestRestartAll_JSONOutputHasNoSkippedField: the JSON output must not expose a
// meta.skipped counter that can never be non-zero — runRestartAll never produces
// a "skipped" outcome, so the field was dead weight that misleads callers into
// thinking partial-skip reporting exists.
func TestRestartAll_JSONOutputHasNoSkippedField(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv, _ := restartAllServer(t, restartAllListBody)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restart-all", "--self", "-", "--json", "--yes", "--settle-delay", "0")
	if err != nil {
		t.Fatalf("restart-all failed: %v\nstderr=%s", err, errOut)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	meta, ok := raw["meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing meta object in output: %s", out)
	}
	if _, present := meta["skipped"]; present {
		t.Fatalf("meta.skipped must not be present (dead field, never non-zero); got meta=%+v", meta)
	}
}

// TestRestartAll_InterruptionAccountsForRemainingTargets: cancelling the context
// mid-run (during the settle delay between a kill and its restore) must not
// silently drop the sessions that were never reached. Every originally-selected
// target must appear in the returned results, so a caller can tell which
// sessions still need a manual restore.
func TestRestartAll_InterruptionAccountsForRemainingTargets(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")

	firstKilled := make(chan struct{}, 1)
	killCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/kill"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/"), "/kill")
			killCount++
			if killCount > 1 {
				// The interruption must land during the first target's settle-delay —
				// a second kill request means the loop raced past that point.
				t.Errorf("unexpected second /kill request for %s; interruption did not land during settle-delay", id)
			}
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"`+id+`","freed":true}`)
			w.(http.Flusher).Flush()
			// Signal only after the response is fully flushed to the client, so
			// restartKill has already parsed a successful result before cancel()
			// fires — otherwise cancelling races the in-flight HTTP round trip
			// and can turn a clean kill into a spurious "kill: context canceled".
			time.Sleep(20 * time.Millisecond)
			select {
			case firstKilled <- struct{}{}:
			default:
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/restore"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/"), "/restore")
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"`+id+`","session":{"id":"`+id+`","projectId":"demo"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	deps := Deps{ProcessAlive: func(int) bool { return true }}.withDefaults()
	cc := &commandContext{deps: deps}
	cmd := NewRootCommand(Deps{ProcessAlive: func(int) bool { return true }})
	cmd.SetArgs([]string{"session", "restart-all"}) // only used for OutOrStdout/ErrOrStderr wiring below

	targets := []sessionDTO{
		{ID: "demo-1", ProjectID: "demo", Kind: "worker"},
		{ID: "demo-3", ProjectID: "demo", Kind: "worker"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-firstKilled // cancel only after the first kill succeeds, during its settle-delay wait
		cancel()
	}()

	results := cc.runRestartAll(ctx, cmd, targets, restartAllOptions{settleDelay: time.Hour})

	if len(results) != len(targets) {
		t.Fatalf("interrupted run dropped targets: got %d results for %d targets: %+v", len(results), len(targets), results)
	}
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		seen[r.SessionID] = true
	}
	for _, tg := range targets {
		if !seen[tg.ID] {
			t.Fatalf("target %s missing from results entirely: %+v", tg.ID, results)
		}
	}
}
