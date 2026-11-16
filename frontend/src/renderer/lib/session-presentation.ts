import {
	attentionZone,
	attentionZoneOrder,
	boardAttentionZoneOrder,
	getAgentActivityView as getPortableAgentActivityView,
	getAttentionZoneView as getPortableAttentionZoneView,
	getAttentionZoneViewForZone as getPortableAttentionZoneViewForZone,
	getSessionStatusView as getPortableSessionStatusView,
	getSessionTimelinePillView as getPortableSessionTimelinePillView,
	isAgentActivityWorking,
	isSessionIdle,
	sessionLifecycleState,
	sessionLifecycleStateView,
	sessionStatusTooltip,
	type AgentActivityView,
	type AttentionZone,
	type AttentionZoneView,
	type ProductUITranslator,
	type SessionStatusView,
	type SessionTimelinePillStatus,
	type SessionTimelinePillView,
} from "@aoagents/product-ui";
import type { TFunction } from "i18next";
import { appI18n, type MessageKey } from "../i18n";
import type { SessionActivity, SessionStatus } from "../types/workspace";

function translator(t: TFunction): ProductUITranslator {
	return (key, values) => t(key as MessageKey, values);
}

export function getAgentActivityView(
	activity?: SessionActivity | null,
	t: TFunction = appI18n.t,
): AgentActivityView {
	return getPortableAgentActivityView(activity, translator(t));
}

export function getSessionStatusView(
	status: SessionStatus,
	t: TFunction = appI18n.t,
): SessionStatusView {
	return getPortableSessionStatusView(status, translator(t));
}

export function getAttentionZoneView(
	status: SessionStatus,
	t: TFunction = appI18n.t,
): AttentionZoneView {
	return getPortableAttentionZoneView(status, translator(t));
}

export function getAttentionZoneViewForZone(
	zone: AttentionZone,
	t: TFunction = appI18n.t,
): AttentionZoneView {
	return getPortableAttentionZoneViewForZone(zone, translator(t));
}

export type SessionStatusDotView = {
	className: string;
	breathe: boolean;
	/** Localized hover text: what the tone means, plus the agent's live activity. */
	title: string;
};

// The session dot carries two independent signals. Colour comes from the board
// section represented by the SCM state, which survives a running agent —
// `status` is activity-first, so it collapses to `working` the moment an agent
// wakes and would otherwise take every pull request tone with it. Merged keeps
// its split-section tone instead of sharing Ready to merge's tone.
//
// The lifecycle overrides read through the session state contract (#355), so
// the dot answers waiting/exited the moment the hook facts say so instead of
// lagging behind the aggregated status recompute (#336 class), and every
// surface reads the same answer. Motion stays on raw agent activity. A no-PR
// idle session is the exception to the preserved section colour: when its
// agent starts working it blinks blue.
export function getSessionStatusDotView(
	session: {
		activity?: SessionActivity | null;
		displayStatus?: string;
		isTerminated?: boolean;
		provisionState?: string;
		scmStatus?: SessionStatus;
		status: SessionStatus;
	},
	t: TFunction = appI18n.t,
): SessionStatusDotView {
	const working = isAgentActivityWorking(session.activity);
	const closedWithoutMerge = session.displayStatus === "Closed without merge";
	// Waiting on the user overrides the lane: the red tone is the contract's
	// answer, reached from either the hook fact or the status (#335's rule
	// applied to the lifecycle rather than the merge gate).
	const lifecycle = sessionLifecycleState(session);
	if (lifecycle === "waiting_input" || lifecycle === "blocked") {
		const label = getAttentionZoneView("needs_input", t).label;
		return {
			className: getAttentionZoneView("needs_input", t).dotClassName,
			breathe: false,
			title: sessionStatusTooltip(label, session.activity, translator(t)),
		};
	}
	// The agent is gone and the row is not: quiet gray, whatever lane it was in.
	if (lifecycle === "exited") {
		return {
			className: "bg-passive",
			breathe: false,
			title: sessionStatusTooltip(getSessionStatusView("exited", t).label, session.activity, translator(t)),
		};
	}
	// A start still in flight is the one honest "Building" (contract:
	// provisioning), the same answer the orchestrator dot and the overview
	// Stage column give.
	if (lifecycle === "provisioning") {
		return {
			className: getSessionStatusView("working", t).dotClassName,
			breathe: true,
			title: sessionLifecycleStateView("provisioning", (key) => t(key as MessageKey)).label,
		};
	}
	// No SCM facts: the dot IS the lifecycle state (#355) — the same answer
	// every other surface gives for this session. One documented exception:
	// an idle session the daemon explicitly reports as silent (no_signal)
	// keeps its alarm tone — a liveness concern, not a lifecycle state, and
	// it must not read as plain idle. An unparseable status (unknown) is a
	// transient client-side gap, not a fact: it reads as the contract's idle.
	if (session.scmStatus === undefined && !closedWithoutMerge) {
		if (lifecycle === "idle" && session.status === "no_signal") {
			return {
				className: getAttentionZoneView(session.status, t).dotClassName,
				breathe: working && session.activity?.turnStalled !== true,
				title: sessionStatusTooltip(getAttentionZoneView(session.status, t).label, session.activity, translator(t)),
			};
		}
		if (lifecycle === "running") {
			return {
				className: getSessionStatusView("working", t).dotClassName,
				breathe: session.activity?.turnStalled !== true,
				title: sessionStatusTooltip(getSessionStatusView("working", t).label, session.activity, translator(t)),
			};
		}
		return {
			className: getSessionStatusView("idle", t).dotClassName,
			breathe: false,
			title: sessionStatusTooltip(getSessionStatusView("idle", t).label, session.activity, translator(t)),
		};
	}
	const sectionStatus: SessionStatus =
		closedWithoutMerge ? "exited" : (session.scmStatus ?? session.status);
	const toneStatus = sectionStatus === "idle" && working ? "working" : sectionStatus;
	// Merge blocked carries its own tone: routing it through the zone would
	// paint a gated PR with the in-review amber, and the tone exists precisely
	// so a gated PR never shares ready/in-review/needs-you (#272).
	const ownTone = toneStatus === "idle" || toneStatus === "merged" || toneStatus === "merge_blocked";
	const className =
		closedWithoutMerge
			? getSessionStatusView("exited", t).dotClassName
			: toneStatus === "exited"
				? "bg-passive"
				: ownTone
					? getSessionStatusView(toneStatus, t).dotClassName
					: getAttentionZoneView(toneStatus, t).dotClassName;
	// The tooltip names the same branch that picked the colour, so the text
	// always explains the tone actually painted.
	const toneLabel =
		toneStatus === "exited" || ownTone
			? getSessionStatusView(toneStatus, t).label
			: getAttentionZoneView(toneStatus, t).label;

	return {
		className,
		// Motion is the liveness signal (DESIGN.md: the pulse shows liveness), so
		// a stalled turn — active with no signs of life past the daemon's
		// threshold — freezes the dot instead of breathing. The colour stays with
		// the board section; the tooltip carries "no signs of life N min".
		breathe: working && session.activity?.turnStalled !== true,
		title: sessionStatusTooltip(toneLabel, session.activity, translator(t)),
	};
}

// The orchestrator's `status` does not track its activity the way a worker's
// does — it stays "working" for the session's whole life, so running that
// status through getSessionStatusDotView would silently absorb waiting_input
// and blocked into the working tone, and an exited orchestrator would read as
// "needs you" instead of gray. Read the tone off the lifecycle contract
// (#355): the same state the overview Stage column and the Running filter
// answer to, so an idle orchestrator is idle everywhere at once.
export function getOrchestratorStatusDotView(
	session: {
		activity?: SessionActivity | null;
		isTerminated?: boolean;
		provisionState?: string;
		scmStatus?: SessionStatus;
		status: SessionStatus;
	},
	t: TFunction = appI18n.t,
): SessionStatusDotView {
	const lifecycle = sessionLifecycleState(session);
	if (lifecycle === "waiting_input" || lifecycle === "blocked") {
		return {
			className: getAttentionZoneView("needs_input", t).dotClassName,
			breathe: false,
			title: sessionStatusTooltip(getAttentionZoneView("needs_input", t).label, session.activity, translator(t)),
		};
	}
	if (lifecycle === "exited") {
		// Bare label: the session is over, so any stale "active" activity must
		// not glue a "· Working" onto a dead session's tooltip.
		return {
			className: getSessionStatusView("exited", t).dotClassName,
			breathe: false,
			title: getSessionStatusView("exited", t).label,
		};
	}
	if (lifecycle === "provisioning") {
		return {
			className: getSessionStatusView("working", t).dotClassName,
			breathe: true,
			title: sessionLifecycleStateView("provisioning", (key) => t(key as MessageKey)).label,
		};
	}
	if (lifecycle === "running") {
		return {
			className: getSessionStatusView("working", t).dotClassName,
			// Motion is the liveness signal, so a stalled turn freezes the dot.
			breathe: session.activity?.turnStalled !== true,
			title: sessionStatusTooltip(getSessionStatusView("working", t).label, session.activity, translator(t)),
		};
	}
	return {
		className: getSessionStatusView("idle", t).dotClassName,
		breathe: false,
		title: sessionStatusTooltip(getSessionStatusView("idle", t).label, session.activity, translator(t)),
	};
}

export function getSessionTimelinePillView(
	status: SessionTimelinePillStatus,
	t: TFunction = appI18n.t,
): SessionTimelinePillView {
	return getPortableSessionTimelinePillView(status, translator(t));
}

/** Live labels for the current locale (getters re-resolve on each access). */
export const attentionZoneLabel: Record<AttentionZone, string> = {
	get merge() {
		return getAttentionZoneViewForZone("merge").label;
	},
	get action() {
		return getAttentionZoneViewForZone("action").label;
	},
	get pending() {
		return getAttentionZoneViewForZone("pending").label;
	},
	get working() {
		return getAttentionZoneViewForZone("working").label;
	},
	get done() {
		return getAttentionZoneViewForZone("done").label;
	},
};

export {
	attentionZone,
	attentionZoneOrder,
	boardAttentionZoneOrder,
	isAgentActivityWorking,
	isSessionIdle,
};
export type {
	AgentActivityView,
	AttentionZone,
	AttentionZoneView,
	SessionStatusView,
	SessionTimelinePillStatus,
	SessionTimelinePillView,
};
