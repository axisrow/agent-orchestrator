package lifecycle

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeOrchestratorNotifier struct {
	calls []fakeOrchestratorNotice
	err   error
}

type fakeOrchestratorNotice struct {
	projectID domain.ProjectID
	message   string
	key       string
}

func (f *fakeOrchestratorNotifier) NotifyOrchestrator(_ context.Context, projectID domain.ProjectID, message, idempotencyKey string) error {
	f.calls = append(f.calls, fakeOrchestratorNotice{projectID: projectID, message: message, key: idempotencyKey})
	return f.err
}

func mergedObs(prURL string, number int) ports.SCMObservation {
	return ports.SCMObservation{
		Fetched:  true,
		Provider: "github",
		Repo:     "o/r",
		PR: ports.SCMPRObservation{
			URL: prURL, Number: number, Merged: true,
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
// PR (plain context, no initiator mark) does not duplicate it.
func TestMergeNotice_APIMergeNotifiesOrchestratorOnce(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	notices := &fakeOrchestratorNotifier{}
	m.SetOrchestratorNotifier(notices)
	st.sessions["mer-1"] = working("mer-1")

	apiCtx := ports.WithMergeInitiator(ctx, ports.MergeInitiatorAO)
	obs := mergedObs("https://github.com/o/r/pull/7", 7)
	if err := m.ApplySCMObservation(apiCtx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	if len(notices.calls) != 1 {
		t.Fatalf("notices = %d, want 1: %+v", len(notices.calls), notices.calls)
	}
	got := notices.calls[0]
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
	// signature, same key, no second message.
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	if len(notices.calls) != 1 {
		t.Fatalf("observer poll duplicated the notice: %+v", notices.calls)
	}
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
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	notices := &fakeOrchestratorNotifier{}
	m.SetOrchestratorNotifier(notices)
	st.sessions["mer-1"] = working("mer-1")

	obs := mergedObs("https://github.com/o/r/pull/8", 8)
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	if len(notices.calls) != 1 {
		t.Fatalf("notices = %d, want 1", len(notices.calls))
	}
	if !strings.Contains(notices.calls[0].message, "outside AO") {
		t.Fatalf("external merge not attributed outside AO: %q", notices.calls[0].message)
	}
	if strings.Contains(notices.calls[0].message, "squash") {
		t.Fatalf("external merge wrongly labeled squash: %q", notices.calls[0].message)
	}
}

func TestMergeNotice_EachPRNoticedOnce(t *testing.T) {
	st := newFakeStore()
	m := New(st, nil)
	notices := &fakeOrchestratorNotifier{}
	m.SetOrchestratorNotifier(notices)
	st.sessions["mer-1"] = working("mer-1")

	if err := m.ApplySCMObservation(ctx, "mer-1", mergedObs("https://github.com/o/r/pull/1", 1)); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplySCMObservation(ctx, "mer-1", mergedObs("https://github.com/o/r/pull/2", 2)); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplySCMObservation(ctx, "mer-1", mergedObs("https://github.com/o/r/pull/1", 1)); err != nil {
		t.Fatal(err)
	}
	if len(notices.calls) != 2 {
		t.Fatalf("notices = %d, want one per PR: %+v", len(notices.calls), notices.calls)
	}
	if notices.calls[0].key == notices.calls[1].key {
		t.Fatalf("distinct PRs shared a delivery id: %q", notices.calls[0].key)
	}
}

// TestMergeNotice_MergedFallbackSignature keeps dedup working when the
// provider reports neither a merge commit nor a usable head: one attempt per
// merge still holds, keyed on the constant fallback.
func TestMergeNotice_MergedFallbackSignature(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	notices := &fakeOrchestratorNotifier{}
	m.SetOrchestratorNotifier(notices)
	st.sessions["mer-1"] = working("mer-1")

	obs := ports.SCMObservation{Fetched: true, PR: ports.SCMPRObservation{URL: "https://github.com/o/r/pull/9", Number: 9, Merged: true}}
	wantSource := "pr-merged:https://github.com/o/r/pull/9:merged"
	for i := 0; i < 2; i++ {
		if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
			t.Fatal(err)
		}
	}
	if len(notices.calls) != 1 {
		t.Fatalf("notices = %d, want 1 under fallback signature", len(notices.calls))
	}
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
	notices := &fakeOrchestratorNotifier{err: context.DeadlineExceeded}
	m.SetOrchestratorNotifier(notices)
	st.sessions["mer-1"] = working("mer-1")

	obs := mergedObs("https://github.com/o/r/pull/4", 4)
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatalf("notice failure must not fail the lifecycle write: %v", err)
	}
	if len(notices.calls) != 1 {
		t.Fatalf("notices = %d, want 1", len(notices.calls))
	}
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	if len(notices.calls) != 1 {
		t.Fatalf("failed delivery was retried on the next poll: %+v", notices.calls)
	}
}

// TestMergeNotice_SurvivesRestart replays the persisted-signature load path:
// a fresh Manager over a store whose PR row already carries the merge-notice
// stamp stays silent on the same merge.
func TestMergeNotice_SurvivesRestart(t *testing.T) {
	st := newFakeStore()
	m := New(st, nil)
	notices := &fakeOrchestratorNotifier{}
	m.SetOrchestratorNotifier(notices)
	st.sessions["mer-1"] = working("mer-1")

	prURL := "https://github.com/o/r/pull/5"
	obs := mergedObs(prURL, 5)
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}

	fresh := New(st, nil)
	freshNotices := &fakeOrchestratorNotifier{}
	fresh.SetOrchestratorNotifier(freshNotices)
	st.sessions["mer-1"] = working("mer-1")
	if err := fresh.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	if len(freshNotices.calls) != 0 {
		t.Fatalf("restart re-noticed the merge: %+v", freshNotices.calls)
	}
}
