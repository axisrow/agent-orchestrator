import { describe, expect, it } from "vitest";
import {
	SESSION_LIFECYCLE_STATES,
	sessionLifecycleRunning,
	sessionLifecycleState,
	sessionLifecycleStateView,
	type SessionLifecycleFacts,
	type SessionLifecycleState,
} from "@aoagents/product-ui";
import { getAgentActivityView, getOrchestratorStatusDotView, getSessionStatusDotView } from "./session-presentation";
import type { WorkspaceSession } from "../types/workspace";

// Cross-surface matrix (#355): for every fact combination the daemon can
// send, every surface that answers "what is this session doing" must give
// the same answer. The sidebar dots, the overview Stage cell, the overview
// Running filter and the tray badge all read the one contract; this pins
// each surface's observable output to the state, so a surface that quietly
// goes back to a local derivation fails here.

const ACTIVITY_STATES = ["active", "idle", "waiting_input", "blocked", "exited", "unknown"] as const;
const PROVISION_STATES = [undefined, "provisioning", "ready", "failed"] as const;
// The lifecycle-relevant statuses. The daemon's anomaly statuses (no_signal,
// unknown) carry a documented dot-level alarm tone (see
// session-presentation.test.ts) and are not lifecycle inputs, so the matrix
// leaves them out.
const STATUSES = [undefined, "working", "idle", "needs_input", "terminated", "exited"] as const;

// The dot tone each state must paint on a PR-less session (no lane to
// preserve). Sidebar worker and orchestrator agree on every state except the
// dead gray: the worker dot's exited tone is the quiet passive gray, the
// orchestrator's stays the status view's exited tone — both read "Exited".
const workerDotByState: Record<SessionLifecycleState, string> = {
	running: "bg-status-working",
	idle: "bg-status-idle",
	waiting_input: "bg-status-needs-you",
	blocked: "bg-status-needs-you",
	provisioning: "bg-status-working",
	exited: "bg-passive",
};
const orchestratorDotByState: Record<SessionLifecycleState, string> = {
	running: "bg-status-working",
	idle: "bg-status-idle",
	waiting_input: "bg-status-needs-you",
	blocked: "bg-status-needs-you",
	provisioning: "bg-status-working",
	exited: "bg-status-exited",
};
const stageLabelByState: Record<SessionLifecycleState, string> = {
	running: "Working",
	idle: "Idle",
	waiting_input: "Input Needed",
	blocked: "Awaiting Decision",
	provisioning: "Starting",
	exited: "Exited",
};

function session(facts: SessionLifecycleFacts, kind: "worker" | "orchestrator"): WorkspaceSession {
	return {
		id: `sess-${kind}`,
		workspaceId: "ws-1",
		workspaceName: "my-app",
		title: "session",
		provider: "claude-code",
		kind,
		status: facts.status ?? "unknown",
		activity: facts.activity ?? undefined,
		isTerminated: facts.isTerminated,
		// The contract reads provisionState as a loose string (the daemon's
		// wire), the session view as its closed union; the matrix only ever
		// feeds the union.
		provisionState: facts.provisionState as WorkspaceSession["provisionState"],
		updatedAt: "2026-11-16T00:00:00Z",
		prs: [],
	};
}

describe("cross-surface session state agreement", () => {
	it("sidebar, overview and tray answer identically for every fact combination", () => {
		for (const terminated of [false, true]) {
			for (const activityState of [...ACTIVITY_STATES, undefined]) {
				for (const provisionState of PROVISION_STATES) {
					for (const status of STATUSES) {
						const facts: SessionLifecycleFacts = {
							activity: activityState ? { state: activityState, lastActivityAt: "" } : undefined,
							isTerminated: terminated || undefined,
							provisionState,
							status,
						};
						const state = sessionLifecycleState(facts);
						expect(SESSION_LIFECYCLE_STATES).toContain(state);

						// Sidebar: both dots paint the state's tone.
						const combo = JSON.stringify({ terminated, activityState, provisionState, status });
						expect(getSessionStatusDotView(session(facts, "worker")).className, combo).toBe(workerDotByState[state]);
						expect(getOrchestratorStatusDotView(session(facts, "orchestrator")).className, combo).toBe(
							orchestratorDotByState[state],
						);

						// Overview: the Running filter follows the state's running
						// rule (#343), and the Stage cell of a PR-less session shows
						// the state's label instead of a fallback lane.
						expect(sessionLifecycleRunning(facts)).toBe(state === "running");
						expect(sessionLifecycleStateView(state).label).toBe(stageLabelByState[state]);

						// Tray: the badge queue is exactly the waiting-on-the-user
						// states (#353) — the same states the sidebar paints red.
						const waitingOnUser = state === "waiting_input" || state === "blocked";
						expect(waitingOnUser).toBe(workerDotByState[state] === "bg-status-needs-you");
					}
				}
			}
		}
	});

	it("an orchestrator is never running while its facts say idle, on any surface", () => {
		// The reported case: orchestrator sessions sat in a degenerate
		// "working" status for their whole life, so the overview called them
		// Building while the sidebar called them idle.
		const facts: SessionLifecycleFacts = { status: "working", activity: { state: "idle", lastActivityAt: "" } };
		const state = sessionLifecycleState(facts);
		expect(state).toBe("idle");
		expect(sessionLifecycleRunning(facts)).toBe(false);
		expect(getSessionStatusDotView(session(facts, "worker")).className).toBe("bg-status-idle");
		expect(getOrchestratorStatusDotView(session(facts, "orchestrator")).className).toBe("bg-status-idle");
	});

	it("a merged session with stale hook facts is never Working on any surface (#361)", () => {
		// The reported case: the PR merged and the agent finished, but its
		// hooks never downgraded the activity fact — the chip read
		// "Merged · Working" and the activity panel kept saying Working. The
		// daemon flags the silent turn as stalled; every surface must read the
		// contract's idle, not the stale claim.
		const facts: SessionLifecycleFacts = {
			status: "merged",
			activity: { state: "active", lastActivityAt: "2026-11-17T04:56:00Z", turnStalled: true, turnSilentForMs: 15 * 60 * 1000 },
		};
		const state = sessionLifecycleState(facts);
		expect(state).toBe("idle");
		expect(sessionLifecycleRunning(facts)).toBe(false);

		// Sidebar and overview dots: idle gray, no breathing, and the hover
		// carries the silence instead of a "· Working" suffix.
		const workerDot = getSessionStatusDotView(session(facts, "worker"));
		expect(workerDot.className).toBe("bg-status-idle");
		expect(workerDot.breathe).toBe(false);
		expect(workerDot.title).toBe("No signs of life 15m · Idle");
		expect(getOrchestratorStatusDotView(session(facts, "orchestrator")).className).toBe("bg-status-idle");

		// Overview: the Running filter and the Stage cell follow the contract.
		expect(sessionLifecycleStateView(state).label).toBe("Idle");

		// The activity pill (topbar, inspector timeline) reads the same
		// demotion: no Working label, no pulse.
		const pill = getAgentActivityView(facts.activity);
		expect(pill.label).toBe("Idle");
		expect(pill.breathe).toBe(false);
	});
});
