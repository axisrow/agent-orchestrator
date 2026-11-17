import { describe, expect, it } from "vitest";
import type { SessionActivity, SessionStatus } from "./session-models";
import {
	defaultLifecycleTranslator,
	lifecycleEnglishLabels,
	SESSION_LIFECYCLE_STATES,
	sessionLifecycleRunning,
	sessionLifecycleState,
	sessionLifecycleStateView,
	sessionLifecycleWaitingInput,
	type SessionLifecycleState,
} from "./session-lifecycle";

const activity = (state: SessionActivity["state"]): SessionActivity => ({
	state,
	lastActivityAt: "2026-11-16T12:00:00Z",
});

const ACTIVITY_STATES = ["active", "idle", "waiting_input", "blocked", "exited", "unknown"] as const;
const PROVISION_STATES = [undefined, "provisioning", "ready", "failed"] as const;
const TERMINATED = [false, true] as const;

// The lifecycle only reads three status values; one SCM value proves the rest
// cannot move it.
const STATUSES = [
	undefined,
	"needs_input",
	"terminated",
	"exited",
	"idle",
	"working",
	"mergeable",
	"ci_failed",
] as const;

function expected(
	terminated: boolean,
	act: (typeof ACTIVITY_STATES)[number] | undefined,
	provision: (typeof PROVISION_STATES)[number],
	status: (typeof STATUSES)[number],
): SessionLifecycleState {
	if (terminated || status === "terminated" || status === "exited") return "exited";
	if (act === "exited") return "exited";
	if (provision === "provisioning") return "provisioning";
	if (provision === "failed") return "blocked";
	// Rows 3/4 outrank the needs_input facts; ready/absent provision does not.
	if (act === "waiting_input" || (provision === undefined || provision === "ready") && status === "needs_input") return "waiting_input";
	if (act === "blocked") return "blocked";
	if (act === "active") return "running";
	return "idle";
}

describe("sessionLifecycleState", () => {
	// The cross-surface contract: every fact combination the daemon can send,
	// so no surface can quietly diverge on a case the table does not answer.
	it("assigns exactly one state to every fact combination", () => {
		for (const terminated of TERMINATED) {
			for (const act of [...ACTIVITY_STATES, undefined]) {
				for (const provision of PROVISION_STATES) {
					for (const status of STATUSES) {
						const facts = {
							activity: act ? activity(act) : undefined,
							isTerminated: terminated || undefined,
							provisionState: provision,
							status: status as SessionStatus | undefined,
						};
						const state = sessionLifecycleState(facts);
						expect(SESSION_LIFECYCLE_STATES).toContain(state);
						expect(state).toBe(expected(terminated, act, provision, status));
					}
				}
			}
		}
	});

	it("never reads running from the aggregated status: an orchestrator idle in facts is idle", () => {
		// The live case behind #355: an orchestrator's status stays "working"
		// for its whole life while its agent idles for weeks.
		expect(sessionLifecycleState({ status: "working", activity: activity("idle") })).toBe("idle");
		expect(sessionLifecycleState({ status: "working" })).toBe("idle");
		expect(sessionLifecycleState({ status: "working", activity: activity("waiting_input") })).toBe("waiting_input");
	});

	it("ends a terminated session whatever stale facts say (#350)", () => {
		expect(sessionLifecycleState({ isTerminated: true, activity: activity("active") })).toBe("exited");
		expect(sessionLifecycleState({ isTerminated: true, status: "needs_input" })).toBe("exited");
		expect(sessionLifecycleState({ isTerminated: true, provisionState: "provisioning" })).toBe("exited");
		expect(sessionLifecycleState({ status: "terminated", activity: activity("waiting_input") })).toBe("exited");
	});

	it("never reads running from a stale active fact: merged PR, finished agent (#361)", () => {
		// The reported case: the PR merged, the agent delivered its final
		// message, and the hooks never downgraded the activity fact. Past the
		// daemon's stall threshold the active claim has no signs of life behind
		// it, so the contract answers idle — a merged session with a dead agent
		// is never Working.
		const stale: SessionActivity = { ...activity("active"), turnStalled: true, turnSilentForMs: 15 * 60 * 1000 };
		expect(sessionLifecycleState({ status: "merged", activity: stale })).toBe("idle");
		expect(sessionLifecycleRunning({ status: "merged", activity: stale })).toBe(false);
		// A live active fact — no stall flag, older daemon or a fresh turn — still runs.
		expect(sessionLifecycleState({ status: "merged", activity: activity("active") })).toBe("running");
		// The daemon keeps the stall flag false while a tool call is in flight,
		// so a long call never demotes an honest turn.
		const inflight: SessionActivity = { ...stale, turnStalled: undefined, turnToolInflightName: "Bash" };
		expect(sessionLifecycleState({ status: "merged", activity: inflight })).toBe("running");
	});

	it("reserves provisioning for a real in-flight start", () => {
		expect(sessionLifecycleState({ provisionState: "provisioning" })).toBe("provisioning");
		expect(sessionLifecycleState({ provisionState: "ready", activity: activity("idle") })).toBe("idle");
		expect(sessionLifecycleState({ provisionState: "failed" })).toBe("blocked");
		// Row 4 precedes the needs_input rows: a failed start never reached the
		// point where an agent could ask for input, and the table reads top-down.
	});

	it("answers idle for an older daemon that sends no facts", () => {
		expect(sessionLifecycleState({})).toBe("idle");
		expect(sessionLifecycleState({ activity: activity("unknown") })).toBe("idle");
	});

	it("predicates agree with the state for every combination", () => {
		for (const terminated of TERMINATED) {
			for (const act of [...ACTIVITY_STATES, undefined]) {
				for (const provision of PROVISION_STATES) {
					for (const status of STATUSES) {
						const facts = {
							activity: act ? activity(act) : undefined,
							isTerminated: terminated || undefined,
							provisionState: provision,
							status: status as SessionStatus | undefined,
						};
						const state = sessionLifecycleState(facts);
						expect(sessionLifecycleRunning(facts)).toBe(state === "running");
						expect(sessionLifecycleWaitingInput(facts)).toBe(state === "waiting_input");
					}
				}
			}
		}
	});
});

describe("sessionLifecycleStateView", () => {
	it("labels and tones every state, breathing only while moving", () => {
		expect(SESSION_LIFECYCLE_STATES.map((state) => sessionLifecycleStateView(state).label)).toEqual([
			"Working",
			"Idle",
			"Input Needed",
			"Awaiting Decision",
			"Starting",
			"Exited",
		]);
		expect(sessionLifecycleStateView("running").dotClassName).toBe("bg-status-working");
		expect(sessionLifecycleStateView("idle").dotClassName).toBe("bg-status-idle");
		expect(sessionLifecycleStateView("waiting_input").dotClassName).toBe("bg-status-needs-you");
		expect(sessionLifecycleStateView("blocked").dotClassName).toBe("bg-status-needs-you");
		expect(sessionLifecycleStateView("provisioning").dotClassName).toBe("bg-status-working");
		expect(sessionLifecycleStateView("exited").dotClassName).toBe("bg-status-exited");
		expect(sessionLifecycleStateView("running").breathe).toBe(true);
		expect(sessionLifecycleStateView("provisioning").breathe).toBe(true);
		expect(sessionLifecycleStateView("idle").breathe).toBe(false);
		expect(sessionLifecycleStateView("exited").breathe).toBe(false);
	});

	it("keeps every label key translatable", () => {
		for (const [key, label] of Object.entries(lifecycleEnglishLabels)) {
			expect(defaultLifecycleTranslator(key as keyof typeof lifecycleEnglishLabels)).toBe(label);
		}
	});
});
