import { describe, expect, it } from "vitest";
import {
	attentionZone,
	getAgentActivityView,
	getAttentionZoneView,
	getSessionStatusDotView,
	getOrchestratorStatusDotView,
	getSessionStatusView,
	getSessionTimelinePillView,
	isAgentActivityWorking,
	isSessionIdle,
} from "./session-presentation";
import type { SessionStatus, WorkspaceSession } from "../types/workspace";

function sessionWith(overrides: Partial<WorkspaceSession>): WorkspaceSession {
	return {
		id: "sess-1",
		workspaceId: "ws-1",
		workspaceName: "my-app",
		title: "fix-bug",
		provider: "claude-code",
		branch: "feat/x",
		status: "working",
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}

const openPr: WorkspaceSession["prs"][number] = {
	number: 7,
	url: "https://github.com/acme/app/pull/7",
	state: "open",
	ci: "unknown",
	review: "none",
	mergeability: "unknown",
	reviewComments: false,
	updatedAt: "2026-01-01T00:00:00Z",
};

describe("session presentation", () => {
	it.each([
		["active", "Working", true, "bg-status-working animate-status-pulse"],
		["idle", "Idle", false, "bg-status-idle"],
		["waiting_input", "Input Needed", false, "bg-status-needs-you"],
		["blocked", "Awaiting Decision", false, "bg-status-needs-you"],
		["exited", "Exited", false, "bg-status-exited"],
		["unknown", "Unknown", false, "bg-status-unknown"],
	] as const)("maps %s agent activity to %s", (state, label, breathe, indicatorClassName) => {
		expect(getAgentActivityView({ state, lastActivityAt: "" })).toMatchObject({
			label,
			breathe,
			indicatorClassName,
		});
	});

	it("uses raw agent activity, not session status, for working indicators", () => {
		expect(isAgentActivityWorking({ state: "active", lastActivityAt: "" })).toBe(true);
		expect(isAgentActivityWorking({ state: "idle", lastActivityAt: "" })).toBe(false);
		expect(isAgentActivityWorking(undefined)).toBe(false);
	});

	it.each([
		["working", "Working"],
		["idle", "Idle"],
		["needs_input", "Input needed"],
		["no_signal", "No signal"],
		["ci_failed", "CI failed"],
		["changes_requested", "Changes requested"],
		["review_pending", "Review pending"],
		["draft", "Draft PR"],
		["pr_open", "PR open"],
		["approved", "Approved"],
		["mergeable", "Ready"],
		["merged", "Merged"],
		["exited", "Exited"],
		["terminated", "Terminated"],
		["unknown", "Unknown status"],
	] as const)("maps %s session status to %s", (status, label) => {
		expect(getSessionStatusView(status).label).toBe(label);
	});

	it("uses distinct session-card tones for idle, no signal, and PR waiting states", () => {
		expect(getSessionStatusView("idle").className).toBe("text-status-idle");
		expect(getSessionStatusView("no_signal").className).toBe("text-status-unknown");
		expect(getSessionStatusView("draft").className).toBe("text-status-in-review");
		expect(getSessionStatusView("pr_open").className).toBe("text-status-in-review");
		expect(getSessionStatusView("review_pending").className).toBe("text-status-in-review");
		expect(getSessionStatusView("exited").className).toBe("text-status-exited");
	});

	it.each([
		["approved", "merge", "Ready to merge"],
		["mergeable", "merge", "Ready to merge"],
		["needs_input", "action", "Needs you"],
		["exited", "action", "Needs you"],
		["no_signal", "action", "Needs you"],
		["ci_failed", "action", "Needs you"],
		["changes_requested", "action", "Needs you"],
		["unknown", "action", "Needs you"],
		["review_pending", "pending", "In review"],
		["merge_blocked", "pending", "In review"],
		["pr_open", "pending", "In review"],
		["draft", "pending", "In review"],
		["working", "working", "Working"],
		["idle", "working", "Working"],
		["merged", "merge", "Ready to merge"],
		["terminated", "done", "Terminated"],
	] as const)("maps %s to the %s attention zone", (status, zone, label) => {
		expect(attentionZone(sessionWith({ status }))).toBe(zone);
		expect(getAttentionZoneView(status)).toMatchObject({ zone, label });
	});

	// PR-less sessions (no SCM facts): the dot is the lifecycle contract's
	// state (#355) — the same answer the overview Stage cell and the tray
	// badge give.
	it.each([
		["idle", "bg-status-idle", "Idle"],
		["needs_input", "bg-status-needs-you", "Needs you"],
		["exited", "bg-passive", "Exited"],
		["terminated", "bg-passive", "Exited"],
		// An explicitly silent session keeps its alarm tone: a liveness
		// concern, not a lifecycle state.
		["no_signal", "bg-status-needs-you", "Needs you"],
		// An unparseable status is a transient client-side gap, not a fact:
		// it reads as the contract's idle.
		["unknown", "bg-status-idle", "Idle"],
	] as const)("paints a PR-less %s session dot from the lifecycle contract", (status, dotClassName, title) => {
		expect(getSessionStatusDotView(sessionWith({ status }))).toEqual({
			className: dotClassName,
			breathe: false,
			title,
		});
	});

	it("paints a working PR-less session from its live activity", () => {
		expect(
			getSessionStatusDotView(sessionWith({ status: "working", activity: { state: "active", lastActivityAt: "" } })),
		).toEqual({ className: "bg-status-working", breathe: true, title: "Working" });
	});

	// Sessions with delivery facts keep the board-section tone through the
	// SCM status — the dot's second channel — whatever the activity-first
	// aggregate status says.
	it.each([
		["ci_failed", "bg-status-needs-you", "Needs you"],
		["changes_requested", "bg-status-needs-you", "Needs you"],
		["draft", "bg-status-in-review", "In review"],
		["pr_open", "bg-status-in-review", "In review"],
		["review_pending", "bg-status-in-review", "In review"],
		// Merge gated (required review not submitted, provider BLOCKED) gets its
		// own tone: never the ready green a bare `mergeable` fact implies (#272).
		["merge_blocked", "bg-status-blocked", "Merge blocked"],
		["approved", "bg-status-ready", "Ready to merge"],
		["mergeable", "bg-status-ready", "Ready to merge"],
		["merged", "bg-status-merged", "Merged"],
	] as const)("paints the %s SCM section dot with its board-section tone", (scmStatus, dotClassName, title) => {
		expect(getSessionStatusDotView(sessionWith({ status: scmStatus, scmStatus }))).toEqual({
			className: dotClassName,
			breathe: false,
			title,
		});
	});

	it("paints the dot from the SCM status before the aggregate status", () => {
		expect(
			getSessionStatusDotView(sessionWith({ status: "merge_blocked", scmStatus: "merge_blocked" })).className,
		).toBe("bg-status-blocked");
	});

	it.each([
		["needs_input", "waiting_input", "Needs you · Input Needed"],
		["changes_requested", "active", "Needs you · Working"],
		["pr_open", "active", "In review · Working"],
		["working", "active", "Working"],
	] as const)("glues the zone with live activity into the %s dot tooltip", (status, activity, title) => {
		// The delivery statuses ride with their SCM fact, as the daemon sends
		// them for every PR session.
		const scmStatus: SessionStatus | undefined =
			status === "changes_requested" || status === "pr_open" ? status : undefined;
		expect(
			getSessionStatusDotView(sessionWith({ status, scmStatus, activity: { state: activity, lastActivityAt: "" } })).title,
		).toBe(title);
	});

	it.each([
		[{ state: "waiting_input" as const }, "bg-status-needs-you", "Needs you · Input Needed"],
		[{ state: "blocked" as const }, "bg-status-needs-you", "Needs you · Awaiting Decision"],
	])("tooltips the orchestrator dot for %s activity without absorbing the working tone", (activity, className, title) => {
		expect(
			getOrchestratorStatusDotView(
				sessionWith({ status: "working", activity: { ...activity, lastActivityAt: "" } }),
			),
		).toEqual({ className, breathe: false, title });
	});

	it("tooltips a terminated orchestrator dot as exited", () => {
		expect(
			getOrchestratorStatusDotView(sessionWith({ status: "working", isTerminated: true })),
		).toEqual({ className: "bg-status-exited", breathe: false, title: "Exited" });
		expect(
			getOrchestratorStatusDotView(
				sessionWith({ status: "working", isTerminated: true, activity: { state: "active", lastActivityAt: "" } }),
			).title,
		).toBe("Exited");
	});

	it("prefers SCM state over runtime status for the dot tone", () => {
		// A running agent drives status to `working`, which would erase every PR
		// tone in the sidebar. SCM state wins so the row keeps saying "merged".
		const merged = sessionWith({
			status: "working",
			scmStatus: "merged",
			activity: { state: "active", lastActivityAt: "" },
		});

		expect(getSessionStatusDotView(merged)).toEqual({
			className: "bg-status-merged",
			breathe: true,
			title: "Merged · Working",
		});
	});

	// Issue #281: a turn hung on an API call must not look like honest work.
	// The tone stays with the board section; the dot freezes and the tooltip
	// carries the silence and its duration.
	it("freezes the dot and names the silence for a stalled turn", () => {
		const stalled = sessionWith({
			status: "working",
			activity: { state: "active", lastActivityAt: "", turnStalled: true, turnSilentForMs: 60 * 60 * 1000 },
		});
		expect(getSessionStatusDotView(stalled)).toEqual({
			className: "bg-status-working",
			breathe: false,
			title: "No signs of life 1h · Working",
		});

		// An honest streaming turn (no stall fact) keeps breathing plain Working.
		const live = sessionWith({
			status: "working",
			activity: { state: "active", lastActivityAt: "" },
		});
		expect(getSessionStatusDotView(live)).toEqual({
			className: "bg-status-working",
			breathe: true,
			title: "Working",
		});
	});

	// Issue #344: a long in-flight tool call is life, not a stalled turn — the
	// dot keeps breathing and the hover names the call with its elapsed time.
	it("keeps breathing and names the call while a tool call is in flight", () => {
		const inflight = sessionWith({
			status: "working",
			activity: {
				state: "active",
				lastActivityAt: "",
				turnToolInflightName: "Bash",
				turnToolInflightForMs: 43 * 60 * 1000,
			},
		});
		expect(getSessionStatusDotView(inflight)).toEqual({
			className: "bg-status-working",
			breathe: true,
			title: "Working on Bash 43m",
		});
	});

	it("keeps board-section color while raw working activity starts the motion", () => {
		const scmStatus = "mergeable" as const;

		expect(
			getSessionStatusDotView(sessionWith({ status: "idle", scmStatus, activity: { state: "idle", lastActivityAt: "" } })),
		).toEqual({ className: "bg-status-ready", breathe: false, title: "Ready to merge · Idle" });
		expect(
			getSessionStatusDotView(
				sessionWith({ status: "working", scmStatus, activity: { state: "active", lastActivityAt: "" } }),
			),
		).toEqual({ className: "bg-status-ready", breathe: true, title: "Ready to merge · Working" });
	});

	it("uses a blinking blue dot when an idle-section session starts working", () => {
		expect(
			getSessionStatusDotView(
				sessionWith({ status: "idle", activity: { state: "active", lastActivityAt: "" } }),
			),
		).toEqual({ className: "bg-status-working", breathe: true, title: "Working" });
	});

	it("paints a closed-without-merge session red", () => {
		expect(
			getSessionStatusDotView(
				sessionWith({ status: "idle", displayStatus: "Closed without merge" }),
			),
		).toEqual({ className: "bg-status-exited", breathe: false, title: "Exited" });
	});

	// The lifecycle overrides read the hook facts first (#355): an agent that
	// stopped or started waiting on the user recolors the dot immediately,
	// instead of lagging behind the aggregated status recompute (#336 class).
	it("grays the dot the moment the agent exits, whatever the stale lane says", () => {
		expect(
			getSessionStatusDotView(
				sessionWith({ status: "pr_open", scmStatus: "pr_open", activity: { state: "exited", lastActivityAt: "" } }),
			),
		).toEqual({ className: "bg-passive", breathe: false, title: "Exited" });
	});

	it("paints needs-you the moment the agent waits, whatever the stale lane says", () => {
		expect(
			getSessionStatusDotView(
				sessionWith({ status: "mergeable", scmStatus: "mergeable", activity: { state: "waiting_input", lastActivityAt: "" } }),
			),
		).toEqual({ className: "bg-status-needs-you", breathe: false, title: "Needs you · Input Needed" });
	});

	it("paints a provisioning worker as starting, same as the orchestrator", () => {
		expect(
			getSessionStatusDotView(sessionWith({ status: "working", provisionState: "provisioning" })),
		).toEqual({ className: "bg-status-working", breathe: true, title: "Starting" });
	});

	// The live case behind #355: an orchestrator whose aggregated status stays
	// "working" for its whole life must read as idle on the dot, exactly as the
	// overview Stage column and the Running filter now read it.
	it("paints an idle orchestrator gray and a live one blue", () => {
		expect(
			getOrchestratorStatusDotView(sessionWith({ status: "working", activity: { state: "idle", lastActivityAt: "" } })),
		).toEqual({ className: "bg-status-idle", breathe: false, title: "Idle" });
		expect(
			getOrchestratorStatusDotView(sessionWith({ status: "working", activity: { state: "active", lastActivityAt: "" } })),
		).toEqual({ className: "bg-status-working", breathe: true, title: "Working" });
	});

	it("paints a provisioning orchestrator as starting, and freezes a stalled one", () => {
		expect(
			getOrchestratorStatusDotView(sessionWith({ status: "working", provisionState: "provisioning" })),
		).toEqual({ className: "bg-status-working", breathe: true, title: "Starting" });
		expect(
			getOrchestratorStatusDotView(
				sessionWith({
					status: "working",
					activity: { state: "active", lastActivityAt: "", turnStalled: true, turnSilentForMs: 60 * 60 * 1000 },
				}),
			),
		).toEqual({ className: "bg-status-working", breathe: false, title: "No signs of life 1h · Working" });
	});

	it("keeps activity indicator color independent from PR and CI presentation", () => {
		const active = getAgentActivityView({ state: "active", lastActivityAt: "" });
		const idle = getAgentActivityView({ state: "idle", lastActivityAt: "" });

		expect(active.indicatorClassName).toBe("bg-status-working animate-status-pulse");
		expect(idle.indicatorClassName).toBe("bg-status-idle");
	});

	it("uses a muted accent treatment for In Review instead of idle gray", () => {
		expect(getAttentionZoneView("review_pending")).toMatchObject({
			dot: "var(--color-status-in-review)",
			titleClassName: "text-status-in-review",
			dotClassName: "bg-status-in-review",
		});
	});

	it("classifies only backend-derived idle sessions for the work lane", () => {
		expect(isSessionIdle(sessionWith({ status: "idle" }))).toBe(true);
		expect(
			isSessionIdle(
				sessionWith({
					status: "idle",
					activity: { state: "active", lastActivityAt: "" },
					prs: [openPr],
				}),
			),
		).toBe(true);
		expect(
			isSessionIdle(
				sessionWith({
					status: "working",
					activity: { state: "idle", lastActivityAt: "" },
					prs: [openPr],
				}),
			),
		).toBe(false);
		expect(
			isSessionIdle(
				sessionWith({
					status: "working",
					activity: { state: "active", lastActivityAt: "" },
				}),
			),
		).toBe(false);
		expect(isSessionIdle(sessionWith({ status: "working" }))).toBe(false);
	});

	it.each([
		["no_signal", "No Signal", "var(--color-status-unknown)"],
		["ci_failed", "CI Failed", "var(--color-status-exited)"],
		["changes_requested", "Changes Requested", "var(--color-status-needs-you)"],
	] as const)("centralizes the %s timeline pill", (status, label, tone) => {
		expect(getSessionTimelinePillView(status)).toMatchObject({ label, tone, breathe: false });
	});
});
