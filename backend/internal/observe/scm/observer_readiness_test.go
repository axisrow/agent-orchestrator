package scm

// Tests for the readiness-pipeline failure tracker (issue #306): a failing
// commit check-runs guard must not suppress the PR detail refresh, repeated
// failures must surface an explicit error state with a retry schedule, and a
// recovered provider must clear the state.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// readinessTestObserver builds an observer over a mutable clock so tests can
// walk through backoff windows without sleeping.
func readinessTestObserver(store *fakeStore, provider *fakeProvider, lc Lifecycle, clock *time.Time) *Observer {
	if provider.identity.Login == "" && provider.identityErr == nil {
		provider.identity = ports.SCMIdentity{Login: "alice", Human: true}
	}
	for repo, prs := range provider.openPRs {
		for i := range prs {
			if prs[i].Author == "" {
				prs[i].Author = "alice"
			}
		}
		provider.openPRs[repo] = prs
	}
	return New(provider, store, lc, Config{Clock: func() time.Time { return *clock }, Tick: time.Hour, Logger: quietSlog(), CacheMax: 128, ScopedIdentityResolver: provider})
}

// guardFailingProvider returns a provider whose repo guard answers 304 (so
// nothing else promotes the PR) and whose commit check-runs guard always
// fails — the live #306 shape: PR listed nowhere, guard erroring, facts stuck.
func guardFailingProvider(fetchErr error) *fakeProvider {
	repoKey := prKey(testRepo, 0)
	p := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{repoKey: {ETag: "v1", NotModified: true}},
		checkGuards:  map[string]ports.SCMGuardResult{},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	p.checkGuardErrs = map[string]error{commitKey(testRepo, "sha1"): errors.New("context deadline exceeded (Client.Timeout exceeded while awaiting headers)")}
	if fetchErr != nil {
		p.fetchObsErrors = map[string]error{prKey(testRepo, 1): fetchErr}
	}
	return p
}

// seedStaleTrackedPR reproduces the incident's durable state: incremental
// discovery active (cursor), repo 304, PR already fetched recently so the
// max-age backstop does not fire either, and mergeability still unknown even
// though the provider reports the PR mergeable — exactly the stuck shape.
func seedStaleTrackedPR(t *testing.T, store *fakeStore, obs *Observer, now time.Time) {
	t.Helper()
	// The row's hashes must describe the unknown-mergeability state, so it is
	// built from an unknown observation rather than by editing knownPR's enum:
	// semantic-diff compares stored hashes, not the enum.
	unknownObs := testObs(1)
	unknownObs.Mergeability = ports.SCMMergeabilityObservation{State: string(domain.MergeUnknown)}
	local, _, _, _, _ := domainFromObservation("p-1", domain.SessionRecord{AutoInjectReview: true}, unknownObs, domain.PullRequest{}, persistenceOptions{}, time.Unix(1, 0).UTC())
	local.ReviewObservedAt = time.Unix(2, 0).UTC()
	store.prs["p-1"] = []domain.PullRequest{local}
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "v1"
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = now.Add(-time.Minute)
	obs.Cache.LastPRFetchAt[prKey(testRepo, 1)] = now.Add(-30 * time.Second)
}

func TestPoll_GuardFailurePromotesRefreshCandidate(t *testing.T) {
	store := testStoreWithSession()
	provider := guardFailingProvider(nil)
	clock := time.Unix(1000, 0).UTC()
	obs := readinessTestObserver(store, provider, &fakeLifecycle{}, &clock)
	seedStaleTrackedPR(t, store, obs, clock)

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The failed guard must not suppress the refresh: the PR is fetched and
	// its mergeability lands durably.
	if len(provider.fetchBatches) != 1 || len(provider.fetchBatches[0]) != 1 {
		t.Fatalf("expected the PR to be fetched despite the guard failure, got %#v", provider.fetchBatches)
	}
	var readyWrite *fakeWrite
	for i := range store.writes {
		if store.writes[i].pr.Number == 1 && store.writes[i].pr.Mergeability == domain.MergeMergeable {
			readyWrite = &store.writes[i]
			break
		}
	}
	if readyWrite == nil {
		t.Fatalf("expected a mergeable write, got %#v", store.writes)
	}
	if got := provider.checkGuardCalls; got != 1 {
		t.Fatalf("checkGuardCalls = %d, want 1", got)
	}
	// A single failure stays private: below the reporting threshold, and the
	// successful fetch in the same poll clears it entirely.
	if failures := obs.ReadinessFailures([]string{"https://github.com/o/r/pull/1"}); len(failures) != 0 {
		t.Fatalf("single transient failure must not surface, got %#v", failures)
	}
}

func TestPoll_RepeatedGuardFailuresSurfaceThenRecover(t *testing.T) {
	store := testStoreWithSession()
	provider := guardFailingProvider(errors.New("read tcp 1.2.3.4:443: connection reset by peer"))
	clock := time.Unix(1000, 0).UTC()
	obs := readinessTestObserver(store, provider, &fakeLifecycle{}, &clock)
	seedStaleTrackedPR(t, store, obs, clock)
	prURL := "https://github.com/o/r/pull/1"

	// Poll 1: guard + fetch fail together; one poll counts as one try.
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures := obs.ReadinessFailures([]string{prURL}); len(failures) != 0 {
		t.Fatalf("first failure must stay below the reporting threshold, got %#v", failures)
	}

	// Poll 2, past the first backoff window: second consecutive failure.
	clock = clock.Add(30 * time.Second)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	failures := obs.ReadinessFailures([]string{prURL})
	f, ok := failures[prURL]
	if !ok {
		t.Fatalf("repeated failures must surface an explicit readiness error, got %#v", failures)
	}
	if f.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", f.Attempts)
	}
	if !strings.Contains(f.LastError, "connection reset by peer") {
		t.Fatalf("lastError = %q, want the provider failure reason", f.LastError)
	}
	if !f.NextRetryAt.After(clock) {
		t.Fatalf("nextRetryAt = %v, want a future retry schedule", f.NextRetryAt)
	}

	// Recovery: the provider heals with a changed check-run ETag, the next
	// poll clears the state and the mergeable facts land.
	provider.mu.Lock()
	provider.checkGuardErrs = nil
	provider.fetchObsErrors = nil
	provider.checkGuards = map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci1"}}
	provider.mu.Unlock()
	clock = clock.Add(time.Minute)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures := obs.ReadinessFailures([]string{prURL}); len(failures) != 0 {
		t.Fatalf("recovery must clear the readiness error, got %#v", failures)
	}
	found := false
	for i := range store.writes {
		if store.writes[i].pr.Number == 1 && store.writes[i].pr.Mergeability == domain.MergeMergeable {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("recovered poll must persist the mergeable facts, got %#v", store.writes)
	}
}

func TestPoll_ReadinessBackoffGatesGuardAndFetch(t *testing.T) {
	store := testStoreWithSession()
	provider := guardFailingProvider(errors.New("connection reset by peer"))
	clock := time.Unix(1000, 0).UTC()
	obs := readinessTestObserver(store, provider, &fakeLifecycle{}, &clock)
	seedStaleTrackedPR(t, store, obs, clock)

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.checkGuardCalls != 1 || len(provider.fetchBatches) != 1 {
		t.Fatalf("first failing poll must try once: guard calls %d, batches %#v", provider.checkGuardCalls, provider.fetchBatches)
	}

	// Inside the first backoff window both the guard and the fetch stay idle.
	provider.mu.Lock()
	provider.fetchBatches = nil
	provider.mu.Unlock()
	clock = clock.Add(5 * time.Second)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := provider.checkGuardCalls; got != 1 {
		t.Fatalf("guard must be gated inside the backoff window, calls = %d", got)
	}
	if len(provider.fetchBatches) != 0 {
		t.Fatalf("fetch must be gated inside the backoff window, got %#v", provider.fetchBatches)
	}

	// Past the window the pipeline retries.
	clock = clock.Add(30 * time.Second)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := provider.checkGuardCalls; got != 2 {
		t.Fatalf("guard must retry past the backoff window, calls = %d", got)
	}
}

func TestPoll_GuardTimeoutBoundsHungCheckRunsResponse(t *testing.T) {
	store := testStoreWithSession()
	provider := guardFailingProvider(nil)
	provider.mu.Lock()
	provider.checkGuardBlocksUntilCtx = true
	provider.mu.Unlock()
	clock := time.Unix(1000, 0).UTC()
	obs := readinessTestObserver(store, provider, &fakeLifecycle{}, &clock)
	obs.guardTimeout = 50 * time.Millisecond
	seedStaleTrackedPR(t, store, obs, clock)

	started := time.Now()
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("poll took %v; the hung guard must be cut at its own timeout", elapsed)
	}
	if got := provider.checkGuardCalls; got != 1 {
		t.Fatalf("checkGuardCalls = %d, want 1", got)
	}
	// Fail-open: the fetch still ran and the ready facts landed.
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("expected the fetch despite the hung guard, got %#v", provider.fetchBatches)
	}
	if failures := obs.ReadinessFailures([]string{"https://github.com/o/r/pull/1"}); len(failures) != 0 {
		t.Fatalf("successful same-poll refresh must clear the failure, got %#v", failures)
	}
}

func TestPoll_ReadinessLogsRetryCountsAndCheckingDuration(t *testing.T) {
	store := testStoreWithSession()
	provider := guardFailingProvider(errors.New("connection reset by peer"))
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	clock := time.Unix(1000, 0).UTC()
	if provider.identity.Login == "" && provider.identityErr == nil {
		provider.identity = ports.SCMIdentity{Login: "alice", Human: true}
	}
	obs := New(provider, store, &fakeLifecycle{}, Config{Clock: func() time.Time { return clock }, Tick: time.Hour, Logger: logger, CacheMax: 128, ScopedIdentityResolver: provider})
	seedStaleTrackedPR(t, store, obs, clock)

	// Two failing polls: the retry counter climbs and is logged.
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(30 * time.Second)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Recovery poll one: the pipeline works again but the PR is not ready yet
	// (CI pending) — the checking window opens.
	provider.mu.Lock()
	provider.checkGuardErrs = nil
	provider.fetchObsErrors = nil
	provider.checkGuards = map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci1"}}
	pendingObs := testObs(1)
	pendingObs.CI = ports.SCMCIObservation{Summary: string(domain.CIPending), HeadSHA: "sha1"}
	provider.observations[prKey(testRepo, 1)] = pendingObs
	provider.mu.Unlock()
	clock = clock.Add(time.Minute)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Recovery poll two: CI lands passing and mergeable — the window closes
	// and its duration is logged.
	provider.mu.Lock()
	provider.checkGuards = map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}}
	provider.observations[prKey(testRepo, 1)] = testObs(1)
	provider.mu.Unlock()
	clock = clock.Add(time.Minute)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	logs := buf.String()
	if !strings.Contains(logs, "readiness check failing; backing off") || !strings.Contains(logs, "attempts=2") {
		t.Fatalf("retry counts must be logged, got:\n%s", logs)
	}
	if !strings.Contains(logs, "pr readiness resolved") || !strings.Contains(logs, "checking_duration_ms=") {
		t.Fatalf("checking duration must be logged, got:\n%s", logs)
	}
}
