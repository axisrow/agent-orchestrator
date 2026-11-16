import { agentSwitchErrorLabelKeys, type AgentSwitchErrorCode } from "../i18n/key-maps";
import type { MessageKey } from "../i18n/messages";
import type { AgentSwitchSummary, WorkspaceSession } from "../types/workspace";
import { agentLabel } from "./agent-options";
import { formatTimeCompact } from "./format-time";

export type AgentSwitchPresentation = {
	stage:
		| "preparing"
		| "stopping_source"
		| "starting_target"
		| "confirming_takeover"
		| "needs_attention"
		| null;
	outcome: "in_progress" | "success" | "failure" | "recovery" | "historical";
	compactLabelKey: MessageKey;
	titleKey: MessageKey;
	descriptionKey: MessageKey;
	values: {
		source: string;
		target: string;
		/** Relative completion time ("13d ago"); empty when the switch carries no timestamp. */
		when: string;
	};
	tone: "working" | "success" | "warning" | "danger" | "muted";
	animate: boolean;
	lockAgentTerminal: boolean;
	allowSourceInput: boolean;
};

export type AgentSwitchPresentationInput = {
	agentSwitch: AgentSwitchSummary;
	currentHarness: string;
	activityState?: string;
	terminalHandleId?: string;
	isTerminated: boolean;
};

export function agentSwitchVisibilityPresentationKind(
	presentation: Pick<AgentSwitchPresentation, "outcome"> | undefined,
): "terminal_failure" | "recovery_required" | undefined {
	if (presentation?.outcome === "failure") return "terminal_failure";
	if (presentation?.outcome === "recovery") return "recovery_required";
	return undefined;
}

export type AgentSwitchStatusVisual = {
	className: string;
	indicatorClassName: string;
	tone: string;
	breathe: boolean;
};

const inProgressDescriptions: Partial<Record<string, MessageKey>> = {
	preparing_handoff: "switchAgent.state.preparingHandoff",
	stopping_source: "switchAgent.state.stoppingSource",
	source_stopped: "switchAgent.state.sourceStopped",
	starting_target: "switchAgent.state.startingTarget",
	target_ready: "switchAgent.state.targetReady",
	delivering_context: "switchAgent.state.deliveringContext",
};

function failureDescriptionKey(errorCode?: string): MessageKey {
	if (!errorCode) return "switchAgent.state.failed";
	return agentSwitchErrorLabelKeys[errorCode as AgentSwitchErrorCode] ?? "switchAgent.state.failed";
}

// A terminated session can never advance a switch, so every state it carries is
// history, not a live transition (issue #350). The label follows the record —
// completed and failed say so; an unfinished switch says interrupted, never a
// success the session never reached and never a present-progressive state that
// would read as still running. The card carries the completion time; without a
// timestamp the plain label stands alone.
function historicalPresentation(agentSwitch: AgentSwitchSummary): AgentSwitchPresentation {
	const when = agentSwitch.updatedAt ? formatTimeCompact(agentSwitch.updatedAt) : "";
	const values = {
		source: agentLabel(agentSwitch.fromHarness),
		target: agentLabel(agentSwitch.targetHarness),
		when,
	};
	const labels =
		agentSwitch.state === "completed"
			? {
					compactLabelKey: "switchAgent.success.compact" as MessageKey,
					descriptionKey: when ? ("switchAgent.historical.completed" as MessageKey) : ("switchAgent.state.completed" as MessageKey),
				}
			: agentSwitch.state === "failed"
				? {
						compactLabelKey: "switchAgent.failure.compact" as MessageKey,
						descriptionKey: when ? ("switchAgent.historical.failed" as MessageKey) : ("switchAgent.state.failed" as MessageKey),
					}
				: {
						compactLabelKey: "switchAgent.interrupted.compact" as MessageKey,
						descriptionKey: when ? ("switchAgent.historical.interrupted" as MessageKey) : ("switchAgent.interrupted.compact" as MessageKey),
					};
	return {
		stage: null,
		outcome: "historical",
		titleKey: "switchAgent.progressTitle",
		...labels,
		values,
		tone: "muted",
		animate: false,
		lockAgentTerminal: false,
		allowSourceInput: false,
	};
}

export function deriveAgentSwitchPresentation({
	agentSwitch,
	currentHarness,
	activityState,
	terminalHandleId,
	isTerminated,
}: AgentSwitchPresentationInput): AgentSwitchPresentation {
	const values = {
		source: agentLabel(agentSwitch.fromHarness),
		target: agentLabel(agentSwitch.targetHarness),
		when: "",
	};

	if (isTerminated) return historicalPresentation(agentSwitch);

	if (agentSwitch.state === "stopping_source" && agentSwitch.errorCode === "source_stop_unconfirmed") {
		return {
			stage: "needs_attention",
			outcome: "recovery",
			compactLabelKey: "switchAgent.recovery.compact",
			titleKey: "switchAgent.sourceStopRecovery.title",
			descriptionKey: "switchAgent.sourceStopRecovery.description",
			values,
			tone: "warning",
			animate: false,
			lockAgentTerminal: true,
			allowSourceInput: false,
		};
	}

	if (
		(agentSwitch.state === "source_stopped" || agentSwitch.state === "starting_target") &&
		agentSwitch.errorCode === "source_restore_unconfirmed"
	) {
		return {
			stage: "needs_attention",
			outcome: "recovery",
			compactLabelKey: "switchAgent.sourceRecovery.compact",
			titleKey: "switchAgent.sourceRecovery.title",
			descriptionKey: "switchAgent.sourceRecovery.description",
			values,
			tone: "warning",
			animate: false,
			lockAgentTerminal: true,
			allowSourceInput: false,
		};
	}

	if (agentSwitch.state === "starting_target" && agentSwitch.errorCode === "target_start_unconfirmed") {
		return {
			stage: "needs_attention",
			outcome: "recovery",
			compactLabelKey: "switchAgent.recovery.compact",
			titleKey: "switchAgent.recovery.title",
			descriptionKey: "switchAgent.recovery.description",
			values,
			tone: "warning",
			animate: false,
			lockAgentTerminal: true,
			allowSourceInput: false,
		};
	}

	if (agentSwitch.state === "failed") {
		return {
			stage: "needs_attention",
			outcome: "failure",
			compactLabelKey: "switchAgent.failure.compact",
			titleKey: "switchAgent.state.failed",
			descriptionKey: failureDescriptionKey(agentSwitch.errorCode),
			values,
			tone: "danger",
			animate: false,
			lockAgentTerminal: false,
			allowSourceInput: false,
		};
	}

	if (agentSwitch.state === "completed") {
		// Terminated sessions short-circuited to history above, so settling
		// here only needs a live target harness with a live terminal.
		const settled =
			currentHarness === agentSwitch.targetHarness &&
			Boolean(terminalHandleId?.trim());
		return {
			stage: settled ? null : "confirming_takeover",
			outcome: settled ? "success" : "in_progress",
			compactLabelKey: settled ? "switchAgent.success.compact" : "switchAgent.compact.switching",
			titleKey: settled ? "switchAgent.success.compact" : "switchAgent.progressTitle",
			descriptionKey: "switchAgent.state.completed",
			values,
			tone: settled ? "success" : "working",
			animate: !settled,
			lockAgentTerminal: !settled,
			allowSourceInput: false,
		};
	}

	if (agentSwitch.state === "preparing_handoff" && agentSwitch.agentHandoffStatus === "requested") {
		const allowSourceInput = activityState === "blocked" || activityState === "waiting_input";
		return {
			stage: "preparing",
			outcome: "in_progress",
			compactLabelKey: allowSourceInput ? "switchAgent.sourceInput.compact" : "switchAgent.compact.switching",
			titleKey: "switchAgent.progressTitle",
			descriptionKey: allowSourceInput
				? "switchAgent.sourceInput.description"
				: "switchAgent.state.preparingHandoff",
			values,
			tone: allowSourceInput ? "warning" : "working",
			animate: true,
			lockAgentTerminal: !allowSourceInput,
			allowSourceInput,
		};
	}

	let stage: AgentSwitchPresentation["stage"] | undefined;
	switch (agentSwitch.state) {
		case "preparing_handoff":
			stage = "preparing";
			break;
		case "stopping_source":
			stage = "stopping_source";
			break;
		case "source_stopped":
		case "starting_target":
			stage = "starting_target";
			break;
		case "target_ready":
		case "delivering_context":
			stage = "confirming_takeover";
			break;
	}

	if (stage) {
		return {
			stage,
			outcome: "in_progress",
			compactLabelKey: "switchAgent.compact.switching",
			titleKey: "switchAgent.progressTitle",
			descriptionKey: inProgressDescriptions[agentSwitch.state] ?? "switchAgent.checkingStatus",
			values,
			tone: "working",
			animate: true,
			lockAgentTerminal: true,
			allowSourceInput: false,
		};
	}

	return {
		stage: "preparing",
		outcome: "in_progress",
		compactLabelKey: "switchAgent.refreshOnly.compact",
		titleKey: "switchAgent.checkingStatus",
		descriptionKey: "switchAgent.checkingStatus",
		values,
		tone: "working",
		animate: true,
		lockAgentTerminal: true,
		allowSourceInput: false,
	};
}

export function deriveSessionAgentSwitchPresentation(
	session: WorkspaceSession,
): AgentSwitchPresentation | undefined {
	if (!session.activeAgentSwitch) return undefined;
	return deriveAgentSwitchPresentation({
		agentSwitch: session.activeAgentSwitch,
		activityState: session.activity?.state,
		currentHarness: session.provider,
		isTerminated: Boolean(session.isTerminated),
		terminalHandleId: session.terminalHandleId,
	});
}

export function agentSwitchStatusVisual(presentation: AgentSwitchPresentation): AgentSwitchStatusVisual {
	switch (presentation.tone) {
		case "success":
			return {
				className: "text-status-ready",
				indicatorClassName: "bg-status-ready",
				tone: "var(--color-status-ready)",
				breathe: false,
			};
		case "warning":
			return {
				className: "text-status-needs-you",
				indicatorClassName: "bg-status-needs-you",
				tone: "var(--color-status-needs-you)",
				breathe: false,
			};
		case "danger":
			return {
				className: "text-status-exited",
				indicatorClassName: "bg-status-exited",
				tone: "var(--color-status-exited)",
				breathe: false,
			};
		case "muted":
			// History is not an operational state: the neutral text tone reads as
			// a record, never as an actionable success or failure (DESIGN.md).
			return {
				className: "text-muted-foreground",
				indicatorClassName: "bg-muted-foreground",
				tone: "var(--color-text-muted)",
				breathe: false,
			};
		default:
			return {
				className: "text-status-working",
				indicatorClassName: "bg-status-working",
				tone: "var(--color-status-working)",
				breathe: presentation.animate,
			};
	}
}
