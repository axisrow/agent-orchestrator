package controllers_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// blockingActivityRecorder applies one signal at a time and stays wedged on
// gate until release, emulating a store stall on the apply path.
type blockingActivityRecorder struct {
	mu      sync.Mutex
	gate    chan struct{}
	applied []ports.ActivitySignal
	entered chan struct{}
}

func newBlockingRecorder() *blockingActivityRecorder {
	return &blockingActivityRecorder{gate: make(chan struct{}), entered: make(chan struct{}, 4096)}
}

func (r *blockingActivityRecorder) ApplyActivitySignal(_ context.Context, _ domain.SessionID, s ports.ActivitySignal) error {
	r.entered <- struct{}{}
	<-r.gate
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applied = append(r.applied, s)
	return nil
}

func (r *blockingActivityRecorder) release() { close(r.gate) }

func (r *blockingActivityRecorder) appliedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.applied)
}

func waitForAppliedSignals(t *testing.T, r *blockingActivityRecorder, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for r.appliedCount() < want {
		if time.Now().After(deadline) {
			t.Fatalf("applier applied %d signals, want %d", r.appliedCount(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestActivityIngest_AppliesInArrivalOrder(t *testing.T) {
	rec := newBlockingRecorder()
	q := controllers.NewActivityIngest(rec, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	for i := 0; i < 50; i++ {
		if err := q.ApplyActivitySignal(ctx, "ao-1", ports.ActivitySignal{
			Valid: true, State: domain.ActivityActive, Event: "post-tool-use", LaunchID: fmt.Sprintf("launch-%d", i),
		}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	// The applier wedges on the first apply until release; enqueueing is
	// already complete, so the queue holds the arrival order.
	rec.release()
	waitForAppliedSignals(t, rec, 50)
	rec.mu.Lock()
	applied := append([]ports.ActivitySignal(nil), rec.applied...)
	rec.mu.Unlock()
	if len(applied) != 50 {
		t.Fatalf("applied %d signals, want 50", len(applied))
	}
	for i, s := range applied {
		if s.LaunchID != fmt.Sprintf("launch-%d", i) {
			t.Fatalf("signal %d arrived out of order: launch=%q", i, s.LaunchID)
		}
	}
}

// countingErrorRecorder counts every apply attempt and always fails, so a test
// can watch the applier keep consuming through errors.
type countingErrorRecorder struct {
	mu    sync.Mutex
	calls int
}

func (r *countingErrorRecorder) ApplyActivitySignal(context.Context, domain.SessionID, ports.ActivitySignal) error {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return ports.ErrActivityProjectionContention
}

func (r *countingErrorRecorder) appliedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestActivityIngest_ApplyErrorsKeepTheQueueDraining(t *testing.T) {
	rec := &countingErrorRecorder{}
	q := controllers.NewActivityIngest(rec, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	for i := 0; i < 5; i++ {
		if err := q.ApplyActivitySignal(ctx, "ao-1", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for rec.appliedCount() < 5 {
		if time.Now().After(deadline) {
			t.Fatalf("applier consumed %d signals after apply errors, want 5", rec.appliedCount())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestActivityIngest_FullQueueSurfacesProjectionBusy(t *testing.T) {
	rec := newBlockingRecorder()
	q := controllers.NewActivityIngest(rec, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	// Wedge the applier inside the recorder: one item is taken off the queue,
	// the channel fills, and enqueueing must surface the projection-busy
	// contract instead of blocking.
	if err := q.ApplyActivitySignal(ctx, "ao-1", ports.ActivitySignal{Valid: true, State: domain.ActivityActive}); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	<-rec.entered
	var overflowed error
	for i := 0; i < 4096 && overflowed == nil; i++ {
		err := q.ApplyActivitySignal(ctx, "ao-1", ports.ActivitySignal{Valid: true, State: domain.ActivityActive})
		if err != nil {
			overflowed = err
		}
	}
	if !errors.Is(overflowed, ports.ErrActivityProjectionContention) {
		t.Fatalf("overflow error = %v, want ErrActivityProjectionContention", overflowed)
	}
}

// The issue's acceptance: hook POSTs answer in well under one poll tick while
// the recorder is wedged behind a slow store, and every fact still lands.
func TestSessionsAPI_ActivityIngestAnswersHooksWhileRecorderIsSlow(t *testing.T) {
	rec := newBlockingRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ingest := controllers.NewActivityIngest(rec, log)
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil,
		httpd.APIDeps{Activity: ingest}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ingest.Run(ctx)

	const hooks = 24
	var wg sync.WaitGroup
	errs := make(chan error, hooks)
	start := time.Now()
	for i := 0; i < hooks; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/ao-1/activity",
				bytes.NewReader([]byte(`{"state":"active","event":"post-tool-use","launchId":"launch-1"}`)))
			if err != nil {
				errs <- err
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := srv.Client().Do(req)
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Errorf("status %d", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("%d hook POSTs answered in %v while the recorder was wedged; the ingest boundary is blocking on the apply", hooks, elapsed)
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("hook POST failed: %v", err)
		}
	}
	rec.release()
	waitForAppliedSignals(t, rec, hooks)
}
