package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	usagepipeline "github.com/aoagents/agent-orchestrator/backend/internal/observe/usage"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
)

// TestUsagePipelineWatchRootsIncludeProviderWrites catches daemon wiring that
// registers provider sources but omits their managed roots from the file
// watcher.
func TestUsagePipelineWatchRootsIncludeProviderWrites(t *testing.T) {
	codexSessions := filepath.Join(t.TempDir(), "sessions")
	rolloutDir := filepath.Join(codexSessions, "2026", "11", "03")
	if err := os.MkdirAll(rolloutDir, 0o700); err != nil {
		t.Fatal(err)
	}
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-11-03-native-1.jsonl")
	if err := os.WriteFile(rolloutPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	roots := usagesvc.SourceRoots{
		ClaudeProjects: t.TempDir(),
		CodexSessions:  codexSessions,
		CodexArchived:  t.TempDir(),
	}
	watcher, err := usagepipeline.NewTranscriptWatcher(
		context.Background(), usagePipelineWatchRoots(roots),
	)
	if err != nil {
		t.Fatalf("create usage watcher: %v", err)
	}
	if err := watcher.Rebuild(context.Background(), []string{rolloutPath}); err != nil {
		t.Fatalf("rebuild usage watcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := watcher.Start(ctx)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("usage watcher did not stop")
		}
	})

	if err := os.WriteFile(rolloutPath, []byte("{\"updated\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantPath, err := filepath.EvalSymlinks(rolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-watcher.Events():
		if event.Path != wantPath {
			t.Fatalf("usage event path = %q, want %q", event.Path, wantPath)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("codex rollout write did not reach the usage watcher")
	}
}
