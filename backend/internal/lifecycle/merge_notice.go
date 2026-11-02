package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// PR-merge notices (fork issue #243): the fact that a worker's PR merged must
// reach the project's orchestrator as a system notification even when no
// worker survives to report it. Every merge path — the in-app merge button
// and the SCM observer seeing an outside merge (GitHub UI, gh CLI,
// auto-merge) — funnels through ApplySCMObservation, so the notice hooks there
// instead of the merge button. One attempt per merge: the dedup signature is
// stamped before the send, because repeated polls of a merged PR must never
// re-notice and the issue explicitly asks for no retries. The bell row
// (NotificationPRMerged) already recorded the fact before this fires, so a
// failed or unwired delivery loses only the conversation copy, not the fact.

// mergeNoticeReactionPrefix names the reaction-dedup entry. The full key is
// "<prefix>:<prURL>", so reactionKeyTargetsPR keeps it grouped with the PR
// row whose last_nudge_signature persists it across restarts.
const mergeNoticeReactionPrefix = "merge-notice"

// mergeNoticeSignature is the merge identity the dedup keys on: the merge
// commit when the provider reported one, else the PR's head SHA, else a
// constant. All merge paths observe the same merged PR, so the signature is
// stable between the API merge's synthetic observation and the observer's
// later poll of the same PR.
func mergeNoticeSignature(o ports.SCMObservation) string {
	switch {
	case o.PR.MergeCommitSHA != "":
		return o.PR.MergeCommitSHA
	case o.PR.HeadSHA != "":
		return o.PR.HeadSHA
	default:
		return "merged"
	}
}

func (m *Manager) notifyPRMerged(ctx context.Context, intent *ports.NotificationIntent, o ports.SCMObservation) {
	if intent == nil || intent.ProjectID == "" {
		return
	}
	prURL := firstSCMNonEmpty(o.PR.URL, o.PR.HTMLURL)
	if prURL == "" {
		return
	}
	key := mergeNoticeReactionPrefix + ":" + prURL
	sig := mergeNoticeSignature(o)
	m.react.mu.Lock()
	if !m.react.loaded[prURL] {
		// A load failure must not block the notice: the worst case is one
		// duplicate notice after a restart, same as nudge dedup.
		if err := m.loadPRSignaturesLocked(ctx, prURL); err != nil {
			slog.Default().Warn("lifecycle: merge notice dedup load failed", "pr", prURL, "err", err)
		}
		m.react.loaded[prURL] = true
	}
	if m.react.seen[key] == sig {
		m.react.mu.Unlock()
		return
	}
	// Stamp before sending — one attempt per merge regardless of delivery
	// outcome. A send that failed is not retried: the recipient is the
	// orchestrator, a TUI refusal (for example a held prompt queue) would
	// otherwise re-fire on every poll of the merged PR forever.
	m.react.seen[key] = sig
	m.react.attempts[key]++
	persistErr := m.persistPRSignaturesLocked(ctx, prURL)
	m.react.mu.Unlock()
	if persistErr != nil {
		slog.Default().Warn("lifecycle: merge notice dedup persist failed", "pr", prURL, "err", persistErr)
	}
	notif := m.orchestratorNotifier()
	if notif == nil {
		return
	}
	message := renderPRMergedNotice(ctx, o)
	idempotencyKey := mergeNoticeDeliveryID(prURL, sig)
	if err := notif.NotifyOrchestrator(ctx, intent.ProjectID, message, idempotencyKey); err != nil {
		slog.Default().Warn("lifecycle: PR merge notice delivery failed", "pr", prURL, "project", intent.ProjectID, "err", err)
		return
	}
	slog.Default().Info("lifecycle: PR merge notice delivered to orchestrator", "pr", prURL, "project", intent.ProjectID)
}

// renderPRMergedNotice formats the system notice the orchestrator receives:
// repo/#number, head→base, the squash commit, and who merged (fork issue
// #243). The initiator comes from the request context — AO's merge API marks
// it, the SCM observer's background contexts read as outside.
func renderPRMergedNotice(ctx context.Context, o ports.SCMObservation) string {
	repo := firstSCMNonEmpty(o.Repo, o.PR.BaseRepo)
	var b strings.Builder
	fmt.Fprintf(&b, "[system] PR merged: %s#%d", repo, o.PR.Number)
	if title := strings.TrimSpace(o.PR.Title); title != "" {
		fmt.Fprintf(&b, " %q", title)
	}
	if o.PR.SourceBranch != "" || o.PR.TargetBranch != "" {
		fmt.Fprintf(&b, " (%s → %s)", o.PR.SourceBranch, o.PR.TargetBranch)
	}
	switch {
	case o.PR.MergeCommitSHA != "":
		fmt.Fprintf(&b, "\nMerge commit: %s", shortSHA(o.PR.MergeCommitSHA))
	case o.PR.HeadSHA != "":
		fmt.Fprintf(&b, "\nMerge commit not reported; head was %s", shortSHA(o.PR.HeadSHA))
	}
	if ports.MergeInitiatorFromContext(ctx) == ports.MergeInitiatorAO {
		// AO's merge API always squashes.
		b.WriteString("\nMerged by the user through AO (squash).")
	} else {
		b.WriteString("\nMerged outside AO (GitHub UI, CLI, or auto-merge).")
	}
	return b.String()
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// mergeNoticeDeliveryID is the transport-level correlation id, stable per
// (PR, merge): a delivery re-attempt after a crash converges on the same
// accepted turn instead of appending a second copy to the conversation.
func mergeNoticeDeliveryID(prURL, sig string) string {
	sum := sha256.Sum256([]byte(prURL + "\x00" + sig))
	return "pr-merge-notice:" + hex.EncodeToString(sum[:12])
}
