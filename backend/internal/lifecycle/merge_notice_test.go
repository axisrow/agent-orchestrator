package lifecycle

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeOrchestratorNotifier records detached merge-notice deliveries. Every
// invocation signals sent before returning, so tests wait for an attempt
// (successful or failed) deterministically instead of racing the goroutine.
type fakeOrchestratorNotifier struct {
	mu    sync.Mutex
	calls []fakeOrchestratorNotice
	err   error
	sent  chan struct{}
}

type fakeOrchestratorNotice struct {
	projectID domain.ProjectID
	message   string
	key       string
}

func newFakeOrchestratorNotifier() *fakeOrchestratorNotifier {
	return &fakeOrchestratorNotifier{sent: make(chan struct{}, 16)}
}

func (f *fakeOrchestratorNotifier) NotifyOrchestrator(_ context.Context, projectID domain.ProjectID, message, idempotencyKey string) error {
	f.mu.Lock()
	f.calls = append(f.calls, fakeOrchestratorNotice{projectID: projectID, message: message, key: idempotencyKey})
	f.mu.Unlock()
	f.sent <- struct{}{}
	return f.err
}

func (f *fakeOrchestratorNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// waitFor fails the test unless n notices are attempted within a bounded
// timeout, then returns the recorded attempts.
func (f *fakeOrchestratorNotifier) waitFor(t *testing.T, n int) []fakeOrchestratorNotice {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-f.sent:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d merge notices attempted", f.count(), n)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeOrchestratorNotice(nil), f.calls...)
}

// assertStableCount proves suppression: a dedup-suppressed poll launches no
// goroutine at all, so no further attempt may land within a short grace
// window (the window only covers a goroutine that was already in flight).
func (f *fakeOrchestratorNotifier) assertStableCount(t *testing.T, want int) {
	t.Helper()
	time.Sleep(250 * time.Millisecond)
	if got := f.count(); got != want {
		t.Fatalf("notices = %d, want %d: suppressed poll re-fired a send", got, want)
	}
}

func mergedObs(prURL string, number int) ports.SCMObservation {
	return ports.SCMObservation{
		Fetched:  true,
		Provider: "github",
		Repo:     "o/r",
		PR: ports.SCMPRObservation{
			URL:            prURL,
			Number:         number,
			Merged:         true,
			Title:          "add checkout",
			SourceBranch:   "feature/checkout",
			TargetBranch:   "main",
			HeadSHA:        "aaaa00000000000000000000000000000000000a",
			MergeCommitSHA: "1234567890abcdef1234567890abcdef12345678",
		},
	}
}

// TestMergeNotice_APIMergeNotifiesOrchestratorOnce is the issue #243 API
// acceptance: a merge through AO's API yields exactly one orchestrator
// notification carrying repo/#number, the branch pair, the merge commit, and
// the in-app attribution — and the observer's later poll of the same merged
// PR (plain context, no initiator mark) does not duplicate it. The send is
// detached: the lifecycle write returns before delivery completes.
func TestMergeNotice_APIMergeNotifiesOrchestratorOnce(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	notices := newFakeOrchestratorNotifier()
	m.SetOrchestratorNotifier(notices)
	st.sessions["mer-1"] = working("mer-1")

	apiCtx := ports.WithMergeInitiator(ctx, ports.MergeInitiatorAO)
	obs := mergedObs("https://github.com/o/r/pull/7", 7)
	if err := m.ApplySCMObservation(apiCtx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	calls := notices.waitFor(t, 1)
	if len(calls) != 1 {
		t.Fatalf("attempts = %d, want 1", len(calls))
	}
	got := calls[0]
	if got.projectID != "mer" {
		t.Fatalf("project = %q, want mer", got.projectID)
	}
	for _, want := range []string{"o/r#7", "add checkout", "feature/checkout → main", "1234567890ab", "squash", "through AO"} {
		if !strings.Contains(got.message, want) {
			t.Fatalf("message %q missing %q", got.message, want)
		}
	}
	if strings.Contains(got.message, "outside AO") {
		t.Fatalf("API merge misattributed as outside: %q", got.message)
	}
	// The bell intent is merge-scoped: API merge and observer poll race onto
	// the same source key, so the store's source-key index collapses them.
	if len(sink.intents) != 1 || sink.intents[0].Type != domain.NotificationPRMerged {
		t.Fatalf("intents = %+v, want one pr_merged", sink.intents)
	}
	wantSource := "pr-merged:https://github.com/o/r/pull/7:1234567890abcdef1234567890abcdef12345678"
	if sink.intents[0].SourceKey != wantSource {
		t.Fatalf("source key = %q, want %q", sink.intents[0].SourceKey, wantSource)
	}

	// Observer poll after restart-worth reload: suppressed by the persisted
	// signature, same key, no second send.
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	notices.assertStableCount(t, 1)
	persisted, err := st.GetPRLastNudgeSignature(ctx, "https://github.com/o/r/pull/7")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(persisted, "merge-notice:https://github.com/o/r/pull/7") {
		t.Fatalf("merge-notice signature not persisted: %q", persisted)
	}
}

func TestMergeNotice_ExternalMergeAttributedOutsideAO(t *testing.T) {
	st := newFakeStore()
	m := New(st, nil)
	notices := newFakeOrchestratorNotifier()
	m.SetOrchestratorNotifier(notices)
	st.sessions["mer-1"] = working("mer-1")

	obs := mergedObs("https://github.com/o/r/pull/8", 8)
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	calls := notices.waitFor(t, 1)
	if !strings.Contains(calls[0].message, "outside AO") {
		t.Fatalf("external merge not attributed outside AO: %q", calls[0].message)
	}
	if strings.Contains(calls[0].message, "squash") {
		t.Fatalf("external merge wrongly labeled squash: %q", calls[0].message)
	}
}

func TestMergeNotice_EachPRNoticedOnce(t *testing.T) {
	st := newFakeStore()
	m := New(st, nil)
	notices := newFakeOrchestratorNotifier()
	m.SetOrchestratorNotifier(notices)
	st.sessions["mer-1"] = working("mer-1")

	if err := m.ApplySCMObservation(ctx, "mer-1", mergedObs("https://github.com/o/r/pull/1", 1)); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplySCMObservation(ctx, "mer-1", mergedObs("https://github.com/o/r/pull/2", 2)); err != nil {
		t.Fatal(err)
	}
	calls := notices.waitFor(t, 2)
	if calls[0].key == calls[1].key {
		t.Fatalf("distinct PRs shared a delivery id: %q", calls[0].key)
	}
	if err := m.ApplySCMObservation(ctx, "mer-1", mergedObs("https://github.com/o/r/pull/1", 1)); err != nil {
		t.Fatal(err)
	}
	notices.assertStableCount(t, 2)
}

// TestMergeNotice_MergedFallbackSignature keeps dedup working when the
// provider reports neither a merge commit nor a usable head: one attempt per
// merge still holds, keyed on the constant fallback.
func TestMergeNotice_MergedFallbackSignature(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	notices := newFakeOrchestratorNotifier()
	m.SetOrchestratorNotifier(notices)
	st.sessions["mer-1"] = working("mer-1")

	obs := ports.SCMObservation{Fetched: true, PR: ports.SCMPRObservation{URL: "https://github.com/o/r/pull/9", Number: 9, Merged: true}}
	wantSource := "pr-merged:https://github.com/o/r/pull/9:merged"
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	notices.waitFor(t, 1)
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	notices.assertStableCount(t, 1)
	if len(sink.intents) == 0 || sink.intents[0].SourceKey != wantSource {
		t.Fatalf("source keys = %+v, want %q", sink.intents, wantSource)
	}
}

func TestMergeNotice_UnwiredNotifierOnlyRingsBell(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	st.sessions["mer-1"] = working("mer-1")
	if err := m.ApplySCMObservation(ctx, "mer-1", mergedObs("https://github.com/o/r/pull/3", 3)); err != nil {
		t.Fatal(err)
	}
	if len(sink.intents) != 1 || sink.intents[0].Type != domain.NotificationPRMerged {
		t.Fatalf("intents = %+v, want one pr_merged", sink.intents)
	}
}

// TestMergeNotice_DeliveryFailureNotRetried pins the one-attempt rule: a
// failing sender is stamped anyway, so later polls of the merged PR stay
// silent instead of re-firing forever (the issue asks for no retries).
func TestMergeNotice_DeliveryFailureNotRetried(t *testing.T) {
	st := newFakeStore()
	m := New(st, nil)
	notices := newFakeOrchestratorNotifier()
	notices.err = context.DeadlineExceeded
	m.SetOrchestratorNotifier(notices)
	st.sessions["mer-1"] = working("mer-1")

	obs := mergedObs("https://github.com/o/r/pull/4", 4)
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatalf("notice failure must not fail the lifecycle write: %v", err)
	}
	notices.waitFor(t, 1)
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	notices.assertStableCount(t, 1)
}

// TestMergeNotice_SurvivesRestart replays the persisted-signature load path:
// a fresh Manager over a store whose PR row already carries the merge-notice
// stamp stays silent on the same merge.
func TestMergeNotice_SurvivesRestart(t *testing.T) {
	st := newFakeStore()
	m := New(st, nil)
	notices := newFakeOrchestratorNotifier()
	m.SetOrchestratorNotifier(notices)
	st.sessions["mer-1"] = working("mer-1")

	prURL := "https://github.com/o/r/pull/5"
	obs := mergedObs(prURL, 5)
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	notices.waitFor(t, 1)

	fresh := New(st, nil)
	fresh.SetOrchestratorNotifier(notices)
	st.sessions["mer-1"] = working("mer-1")
	if err := fresh.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	notices.assertStableCount(t, 1)
}

// TestMergeNotice_SendSeamReceivesDelivery pins the injection seam the
// detached send exposes: an injected mergeNoticeSendFunc receives the
// project, rendered message, and stable idempotency key.
func TestMergeNotice_SendSeamReceivesDelivery(t *testing.T) {
	st := newFakeStore()
	sent := make(chan fakeOrchestratorNotice, 4)
	m := New(st, nil, WithMergeNoticeSend(func(_ context.Context, projectID domain.ProjectID, message, idempotencyKey string) error {
		sent <- fakeOrchestratorNotice{projectID: projectID, message: message, key: idempotencyKey}
		return nil
	}))
	st.sessions["mer-1"] = working("mer-1")
	if err := m.ApplySCMObservation(ctx, "mer-1", mergedObs("https://github.com/o/r/pull/6", 6)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-sent:
		if got.projectID != "mer" {
			t.Fatalf("project = %q, want mer", got.projectID)
		}
		if got.key == "" {
			t.Fatal("seam delivery missing idempotency key")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("seam send never invoked")
	}
}
