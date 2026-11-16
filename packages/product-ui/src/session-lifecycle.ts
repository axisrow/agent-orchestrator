import type { SessionActivity, SessionActivityState, SessionStatus } from "./session-models";

/**
 * The one lifecycle state a session is in, derived once from the daemon's raw
 * facts. Every surface that answers "what is this session doing right now" —
 * the sidebar dot, the overview Stage column, the tray badge, the Running
 * filter, the session card — reads this derivation, so they cannot disagree
 * (fork issue #355, closing the #335/#336/#338/#350 class of per-surface
 * rederivations).
 *
 * "Building" is deliberately absent: it is a delivery lane (a session with no
 * PR yet, `KanbanBuilding`), never a lifecycle state. A surface may only show
 * Building/provisioning progress while `provisionState === "provisioning"` —
 * real start-up work — never as a fallback for a session that is simply idle.
 */
export const SESSION_LIFECYCLE_STATES = [
	"running",
	"idle",
	"waiting_input",
	"blocked",
	"provisioning",
	"exited",
] as const;

export type SessionLifecycleState = (typeof SESSION_LIFECYCLE_STATES)[number];

/**
 * The raw facts the lifecycle reads. Everything is optional: an older daemon
 * may send none of them, and the derivation still answers.
 *
 * - `activity` is the hook-reported agent fact (`SessionActivity.state`) and
 *   the only source of "executing right now". An orchestrator's aggregated
 *   `status` stays "working" for its whole life, so it must never decide
 *   liveness — the hook facts do.
 * - `status` is the daemon's aggregated session status. Only its
 *   `terminated` and `needs_input` values move the lifecycle; the SCM
 *   delivery values (ci_failed, review_pending, mergeable, …) refine what a
 *   session is *delivering*, not what its agent is *doing*, and stay in the
 *   surfaces that present delivery (lanes, markers, the merge gate).
 * - `isTerminated` ends the session: a terminated row is history, whatever
 *   stale facts it still carries (#350).
 * - `provisionState` speaks only while a start is in flight or has failed;
 *   `ready`/absent mean the facts above decide.
 */
export type SessionLifecycleFacts = {
	activity?: SessionActivity | null;
	status?: SessionStatus;
	isTerminated?: boolean;
	provisionState?: string;
};

/**
 * The contract table: facts → state, first match wins.
 *
 * | # | isTerminated / status   | activity.state | provisionState | state          |
 * |---|-------------------------|----------------|----------------|----------------|
 * | 1 | terminated              | *              | *              | exited         |
 * | 2 | *                       | exited         | *              | exited         |
 * | 3 | *                       | *              | provisioning   | provisioning   |
 * | 4 | *                       | *              | failed         | blocked        |
 * | 5 | *                       | waiting_input  | *              | waiting_input  |
 * | 6 | status needs_input      | *              | *              | waiting_input  |
 * | 7 | *                       | blocked        | *              | blocked        |
 * | 8 | *                       | active         | *              | running        |
 * | 9 | *                       | idle/unknown/– | ready/–        | idle           |
 *
 * Rows 5+6 are one state reached from either fact: the daemon may report the
 * status first, the hook first, or both. Row 4: a start that failed is stuck
 * on the user — the surfaces show its `provisionError` — so it reads as
 * blocked rather than as a lying idle. Row 9 is the deliberate default: an
 * agent that is not executing, waiting, blocked, or gone is idle, whatever
 * its delivery lane says.
 */
export function sessionLifecycleState(facts: SessionLifecycleFacts): SessionLifecycleState {
	if (facts.isTerminated === true || facts.status === "terminated") return "exited";
	if (facts.activity?.state === "exited") return "exited";
	if (facts.provisionState === "provisioning") return "provisioning";
	if (facts.provisionState === "failed") return "blocked";
	if (facts.activity?.state === "waiting_input" || facts.status === "needs_input") return "waiting_input";
	if (facts.activity?.state === "blocked") return "blocked";
	if (facts.activity?.state === "active") return "running";
	return "idle";
}

/** True only when the agent is executing a turn right now (hook fact). The Running filter's predicate (#342/#343). */
export function sessionLifecycleRunning(facts: SessionLifecycleFacts): boolean {
	return sessionLifecycleState(facts) === "running";
}

/** True only when the agent is waiting on the user. The tray badge's predicate (#353): the badge is a human-input queue. */
export function sessionLifecycleWaitingInput(facts: SessionLifecycleFacts): boolean {
	return sessionLifecycleState(facts) === "waiting_input";
}

export type SessionLifecycleStateView = {
	state: SessionLifecycleState;
	label: string;
	tone: string;
	dotClassName: string;
	breathe: boolean;
};

export type SessionLifecycleMessageKey =
	| `activity.${Exclude<SessionActivityState, "unknown">}`
	| "lifecycle.provisioning";

type SessionLifecycleStateBase = Omit<SessionLifecycleStateView, "label"> & {
	labelKey: SessionLifecycleMessageKey;
};

// Reuse the activity vocabulary wherever the words already exist in every
// locale; only provisioning needs a new phrase. Running deliberately says
// "Working" (the agent is executing a turn), not "Running".
const lifecycleStateBases: Record<SessionLifecycleState, SessionLifecycleStateBase> = {
	running: {
		state: "running",
		labelKey: "activity.active",
		tone: "var(--color-status-working)",
		dotClassName: "bg-status-working",
		breathe: true,
	},
	idle: {
		state: "idle",
		labelKey: "activity.idle",
		tone: "var(--color-status-idle)",
		dotClassName: "bg-status-idle",
		breathe: false,
	},
	waiting_input: {
		state: "waiting_input",
		labelKey: "activity.waiting_input",
		tone: "var(--color-status-needs-you)",
		dotClassName: "bg-status-needs-you",
		breathe: false,
	},
	blocked: {
		state: "blocked",
		labelKey: "activity.blocked",
		tone: "var(--color-status-needs-you)",
		dotClassName: "bg-status-needs-you",
		breathe: false,
	},
	provisioning: {
		state: "provisioning",
		labelKey: "lifecycle.provisioning",
		tone: "var(--color-status-working)",
		dotClassName: "bg-status-working",
		breathe: true,
	},
	exited: {
		state: "exited",
		labelKey: "activity.exited",
		tone: "var(--color-status-exited)",
		dotClassName: "bg-status-exited",
		breathe: false,
	},
};

export const lifecycleEnglishLabels: Record<SessionLifecycleMessageKey, string> = {
	"activity.active": "Working",
	"activity.idle": "Idle",
	"activity.waiting_input": "Input Needed",
	"activity.blocked": "Awaiting Decision",
	"lifecycle.provisioning": "Starting",
	"activity.exited": "Exited",
};

export const defaultLifecycleTranslator: (key: SessionLifecycleMessageKey) => string = (key) =>
	lifecycleEnglishLabels[key];

export function sessionLifecycleStateView(
	state: SessionLifecycleState,
	translate: (key: SessionLifecycleMessageKey) => string = defaultLifecycleTranslator,
): SessionLifecycleStateView {
	const { labelKey, ...view } = lifecycleStateBases[state];
	return { ...view, label: translate(labelKey) };
}
