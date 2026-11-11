package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The --*-file variants (issue #311): agents stage metacharacter-heavy text in
// a file or on stdin so nothing shell-quoted travels on the command line. The
// fixtures below deliberately carry ${{ }}, backticks, apostrophes, and !.

const flagFileFixture = "cancel: ${{ matrix.os }} `go test` it's! quoted"

func reportFlagFileServer(t *testing.T) (*httptest.Server, *reportAPIRequest) {
	t.Helper()
	got := &reportAPIRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/reports" {
			_ = json.NewDecoder(r.Body).Decode(got)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"rpt_1"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestReport_NoteFileVariants(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "worker-1")
	noteFile := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(noteFile, []byte(flagFileFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	emptyFile := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(emptyFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	absentFile := filepath.Join(t.TempDir(), "absent.md")
	run := func(t *testing.T, in *strings.Reader, args ...string) (string, string, error) {
		t.Helper()
		cfg := setConfigEnv(t)
		srv, _ := reportFlagFileServer(t)
		writeRunFileFor(t, cfg, srv)
		return executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }, In: in}, args...)
	}
	t.Run("file delivers the note byte-for-byte", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv, got := reportFlagFileServer(t)
		writeRunFileFor(t, cfg, srv)
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "report", "--checkpoint", "--note-file", noteFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.State != "checkpoint" || got.Note != flagFileFixture {
			t.Fatalf("state = %q note = %q", got.State, got.Note)
		}
	})
	t.Run("stdin", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv, got := reportFlagFileServer(t)
		writeRunFileFor(t, cfg, srv)
		deps := Deps{ProcessAlive: func(int) bool { return true }, In: strings.NewReader(flagFileFixture)}
		if _, _, err := executeCLI(t, deps, "report", "--done", "--note-file", "-"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.State != "done" || got.Note != flagFileFixture {
			t.Fatalf("state = %q note = %q", got.State, got.Note)
		}
	})
	t.Run("empty file behaves like an empty note", func(t *testing.T) {
		_, _, err := run(t, nil, "report", "--done", "--note-file", emptyFile)
		if err == nil || !strings.Contains(err.Error(), "--note is required") {
			t.Fatalf("err = %v, want the --note is required usage error", err)
		}
	})
	t.Run("missing file is a usage error", func(t *testing.T) {
		_, _, err := run(t, nil, "report", "--done", "--note-file", absentFile)
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("err = %v exit = %d, want a usage error", err, ExitCode(err))
		}
	})
	t.Run("inline and file together", func(t *testing.T) {
		_, _, err := run(t, nil, "report", "--done", "--note", "inline", "--note-file", noteFile)
		if err == nil || !strings.Contains(err.Error(), "not both") {
			t.Fatalf("err = %v, want the mutual-exclusion error", err)
		}
	})
}

func TestSend_MessageFileVariants(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	messageFile := filepath.Join(t.TempDir(), "msg.md")
	if err := os.WriteFile(messageFile, []byte(flagFileFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	absentFile := filepath.Join(t.TempDir(), "absent.md")
	t.Run("file delivers the message byte-for-byte", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv, capture := sendServer(t, http.StatusOK, `{"ok":true,"sessionId":"demo-1","message":"x"}`)
		writeRunFileFor(t, cfg, srv)
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "send", "--session", "demo-1", "--message-file", messageFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var req struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if req.Message != flagFileFixture {
			t.Fatalf("message = %q", req.Message)
		}
	})
	t.Run("stdin", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv, capture := sendServer(t, http.StatusOK, `{"ok":true,"sessionId":"demo-1","message":"x"}`)
		writeRunFileFor(t, cfg, srv)
		deps := Deps{ProcessAlive: func(int) bool { return true }, In: strings.NewReader(flagFileFixture)}
		if _, _, err := executeCLI(t, deps, "send", "--session", "demo-1", "--message-file", "-"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var req struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if req.Message != flagFileFixture {
			t.Fatalf("message = %q", req.Message)
		}
	})
	t.Run("empty file requires a message", func(t *testing.T) {
		cfg := setConfigEnv(t)
		emptyFile := filepath.Join(t.TempDir(), "empty.md")
		if err := os.WriteFile(emptyFile, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		srv, _ := sendServer(t, http.StatusOK, `{}`)
		writeRunFileFor(t, cfg, srv)
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "send", "--session", "demo-1", "--message-file", emptyFile)
		if err == nil || !strings.Contains(err.Error(), "--message is required") {
			t.Fatalf("err = %v, want the --message is required usage error", err)
		}
	})
	t.Run("missing file is a usage error", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv, _ := sendServer(t, http.StatusOK, `{}`)
		writeRunFileFor(t, cfg, srv)
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "send", "--session", "demo-1", "--message-file", absentFile)
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("err = %v exit = %d, want a usage error", err, ExitCode(err))
		}
	})
	t.Run("inline and file together", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv, _ := sendServer(t, http.StatusOK, `{}`)
		writeRunFileFor(t, cfg, srv)
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "send", "--session", "demo-1", "--message", "inline", "--message-file", messageFile)
		if err == nil || !strings.Contains(err.Error(), "not both") {
			t.Fatalf("err = %v, want the mutual-exclusion error", err)
		}
	})
}

func TestSpawn_PromptFile(t *testing.T) {
	promptFile := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(promptFile, []byte(flagFileFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("file feeds the spawn request", func(t *testing.T) {
		cfg := setConfigEnv(t)
		var requests []string
		var req spawnRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			appendPrimaryRequest(&requests, r)
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
				_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://github.com/aoagents/agent-orchestrator","defaultBranch":"main"}}`)
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
				_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
				_ = json.NewDecoder(r.Body).Decode(&req)
				_, _ = io.WriteString(w, `{"session":{"id":"demo-9","status":"idle"}}`)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(srv.Close)
		writeRunFileFor(t, cfg, srv)
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--project", "demo", "--agent", "codex", "--name", "worker", "--prompt-file", promptFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Prompt != flagFileFixture {
			t.Fatalf("prompt = %q", req.Prompt)
		}
	})
	t.Run("inline and file together", func(t *testing.T) {
		cfg := setConfigEnv(t)
		var requests []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			appendPrimaryRequest(&requests, r)
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
				_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://github.com/aoagents/agent-orchestrator","defaultBranch":"main"}}`)
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
				_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(srv.Close)
		writeRunFileFor(t, cfg, srv)
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--project", "demo", "--agent", "codex", "--name", "worker", "--prompt", "inline", "--prompt-file", promptFile)
		if err == nil || !strings.Contains(err.Error(), "not both") {
			t.Fatalf("err = %v, want the mutual-exclusion error", err)
		}
	})
}

func TestCueCreate_PromptFileVariants(t *testing.T) {
	newServer := func(t *testing.T, body *cueCreateDTO) *httptest.Server {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.URL.Path == "/internal/telemetry/cli-invoked":
				w.WriteHeader(http.StatusAccepted)
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
				_, _ = io.WriteString(w, `{"project":{"id":"demo","kind":"single_repo"}}`)
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/demo/cues":
				if err := json.NewDecoder(r.Body).Decode(body); err != nil {
					t.Errorf("decode create body: %v", err)
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, `{"cue":{"id":"cue-1","projectId":"demo","name":"Cue","type":"agent","prompt":"x"}}`)
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		t.Cleanup(server.Close)
		return server
	}
	t.Run("file feeds the cue prompt", func(t *testing.T) {
		cfg := setConfigEnv(t)
		t.Setenv("AO_PROJECT_ID", "demo")
		t.Setenv("AO_SESSION_ID", "")
		var body cueCreateDTO
		server := newServer(t, &body)
		writeRunFileFor(t, cfg, server)
		promptFile := filepath.Join(t.TempDir(), "prompt.md")
		if err := os.WriteFile(promptFile, []byte(flagFileFixture), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "cue", "create", "--name", "Cue", "--prompt-file", promptFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if body.Type != "agent" || body.Prompt != flagFileFixture {
			t.Fatalf("type = %q prompt = %q", body.Type, body.Prompt)
		}
	})
	t.Run("inline and file together", func(t *testing.T) {
		cfg := setConfigEnv(t)
		t.Setenv("AO_PROJECT_ID", "demo")
		t.Setenv("AO_SESSION_ID", "")
		var body cueCreateDTO
		server := newServer(t, &body)
		writeRunFileFor(t, cfg, server)
		promptFile := filepath.Join(t.TempDir(), "prompt.md")
		if err := os.WriteFile(promptFile, []byte(flagFileFixture), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "cue", "create", "--name", "Cue", "--prompt", "inline", "--prompt-file", promptFile)
		if err == nil || !strings.Contains(err.Error(), "not both") {
			t.Fatalf("err = %v, want the mutual-exclusion error", err)
		}
	})
}

func TestAutomation_PromptFileVariants(t *testing.T) {
	t.Run("create reads the prompt from a file", func(t *testing.T) {
		cfg := setConfigEnv(t)
		server, capture := automationServer(t, http.StatusCreated, `{"automation":{"id":"automation-1","displayName":"Morning","nextRunAt":"2026-08-26T03:30:00Z"}}`)
		writeRunFileFor(t, cfg, server)
		promptFile := filepath.Join(t.TempDir(), "prompt.md")
		if err := os.WriteFile(promptFile, []byte(flagFileFixture), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }, Now: func() time.Time { return time.Now() }}, "automation", "create", "--project", "demo", "--name", "Morning", "--prompt-file", promptFile, "--cron", "0 9 * * *", "--timezone", "UTC")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(capture.body, &body); err != nil {
			t.Fatal(err)
		}
		if body["prompt"] != flagFileFixture {
			t.Fatalf("prompt = %v", body["prompt"])
		}
	})
	t.Run("update reads the prompt from stdin", func(t *testing.T) {
		cfg := setConfigEnv(t)
		server, capture := automationServer(t, http.StatusOK, `{"automation":{"id":"automation-1","displayName":"Morning"}}`)
		writeRunFileFor(t, cfg, server)
		deps := Deps{ProcessAlive: func(int) bool { return true }, In: strings.NewReader(flagFileFixture)}
		_, _, err := executeCLI(t, deps, "automation", "update", "automation-1", "--prompt-file", "-")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var body automationUpdateDTO
		if err := json.Unmarshal(capture.body, &body); err != nil {
			t.Fatal(err)
		}
		if body.Prompt == nil || *body.Prompt != flagFileFixture {
			t.Fatalf("prompt pointer = %v", body.Prompt)
		}
	})
	t.Run("inline and file together", func(t *testing.T) {
		cfg := setConfigEnv(t)
		server, _ := automationServer(t, http.StatusCreated, `{"automation":{"id":"automation-1","displayName":"Morning"}}`)
		writeRunFileFor(t, cfg, server)
		promptFile := filepath.Join(t.TempDir(), "prompt.md")
		if err := os.WriteFile(promptFile, []byte(flagFileFixture), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "automation", "create", "--project", "demo", "--name", "Morning", "--prompt", "inline", "--prompt-file", promptFile, "--cron", "0 9 * * *")
		if err == nil || !strings.Contains(err.Error(), "not both") {
			t.Fatalf("err = %v, want the mutual-exclusion error", err)
		}
	})
}

func TestProjectSetConfig_ConfigJSONFileVariants(t *testing.T) {
	t.Run("file supplies the config", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv, capture := projectConfigServer(t, `{"status":"ok","project":{"id":"demo"}}`)
		writeRunFileFor(t, cfg, srv)
		configFile := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(configFile, []byte(`{"worker":{"agent":"codex"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "project", "set-config", "demo", "--config-json-file", configFile)
		if err != nil {
			t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
		}
		var got struct {
			Config struct {
				Worker struct {
					Agent string `json:"agent"`
				} `json:"worker"`
			} `json:"config"`
		}
		if err := json.Unmarshal(capture.putBody, &got); err != nil {
			t.Fatalf("decode PUT body: %v\nbody=%s", err, capture.putBody)
		}
		if got.Config.Worker.Agent != "codex" {
			t.Fatalf("worker agent = %q, want codex", got.Config.Worker.Agent)
		}
	})
	t.Run("inline and file together", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv, _ := projectConfigServer(t, `{"status":"ok","project":{"id":"demo"}}`)
		writeRunFileFor(t, cfg, srv)
		configFile := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(configFile, []byte(`{"worker":{"agent":"codex"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "project", "set-config", "demo", "--config-json", `{"worker":{"agent":"claude-code"}}`, "--config-json-file", configFile)
		if err == nil || !strings.Contains(err.Error(), "not both") {
			t.Fatalf("err = %v, want the mutual-exclusion error", err)
		}
	})
}
