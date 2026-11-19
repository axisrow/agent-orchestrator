// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
	bellUnreadCount,
	dockBounceType,
	loudNotificationsEnabled,
	needsHumanFeedback,
	osToastChimeControllable,
	panelShowsNotification,
	shouldReplaceBounce,
	shouldSignalAttention,
	shouldToast,
	toastSilent,
	trayNotificationsDisabled,
	type NotificationType,
} from "./notification-signals";

// The decision classes only (fork issue #370, owner policy decision
// 2026-11-20): an agent blocked on the user, a failed cloud agent, and the
// edge-triggered stuck-delivery incident (fork issue #211).
const SIGNAL_TYPES: string[] = [
	"needs_input",
	"agent_failed",
	// Edge-triggered once per (project, cause-class) incident (fork issue #211).
	"stuck_delivery",
];
const SILENT_TYPES: NotificationType[] = [
	"ready_to_merge",
	"pr_merged",
	"pr_closed_unmerged",
	"review_completed",
	// Review findings: the fork's workers address them automatically, so they
	// need no owner decision (fork issue #370).
	"review_changes_requested",
	// The terminal delivery record is a fact, not a call for feedback.
	"delivery_died",
];
// The cloud control plane's name for the review_changes_requested verdict
// (fork issue #370): quiet like its local counterpart.
const CLOUD_REVIEW_FEEDBACK = "review_feedback";

describe("needsHumanFeedback", () => {
	it("classifies the human-feedback types as signals", () => {
		for (const type of SIGNAL_TYPES) {
			expect(needsHumanFeedback(type)).toBe(true);
		}
	});

	it("keeps routine lifecycle types quiet", () => {
		for (const type of SILENT_TYPES) {
			expect(needsHumanFeedback(type)).toBe(false);
		}
	});

	it("defaults unknown and missing types to quiet", () => {
		expect(needsHumanFeedback("some_future_type")).toBe(false);
		expect(needsHumanFeedback(undefined)).toBe(false);
	});
});

describe("loudNotificationsEnabled", () => {
	it.each(["1", "true", " 1 ", "TRUE"])("opts back into the upstream behavior for %s", (value) => {
		expect(loudNotificationsEnabled(value)).toBe(true);
	});

	it.each([undefined, "", "0", "false", "loud", "yes"])("stays quiet for %s", (value) => {
		expect(loudNotificationsEnabled(value)).toBe(false);
	});
});

describe("trayNotificationsDisabled", () => {
	it.each(["1", "true", " 1 ", "TRUE"])("mutes the OS notification surface for %s", (value) => {
		expect(trayNotificationsDisabled(value)).toBe(true);
	});

	it.each([undefined, "", "0", "false", "off", "yes"])("keeps signaling for %s", (value) => {
		expect(trayNotificationsDisabled(value)).toBe(false);
	});
});

describe("shouldToast", () => {
	it("toasts only the human-feedback types", () => {
		for (const type of SIGNAL_TYPES) {
			expect(shouldToast({ title: `${type} title`, type }, true)).toBe(true);
		}
	});

	it("does not toast routine types or review findings, even with a title", () => {
		for (const type of SILENT_TYPES) {
			expect(shouldToast({ title: `${type} title`, type }, true)).toBe(false);
		}
		expect(shouldToast({ title: `${CLOUD_REVIEW_FEEDBACK} title`, type: CLOUD_REVIEW_FEEDBACK }, true)).toBe(false);
	});

	it("in loud mode toasts every backend type, like upstream", () => {
		for (const type of [...SIGNAL_TYPES, ...SILENT_TYPES, CLOUD_REVIEW_FEEDBACK]) {
			expect(shouldToast({ title: `${type} title`, type }, true, true)).toBe(true);
		}
	});

	it("does not toast without a title or when notifications are unsupported", () => {
		expect(shouldToast({ title: "", type: "needs_input" }, true)).toBe(false);
		expect(shouldToast({ type: "needs_input" }, true)).toBe(false);
		expect(shouldToast({ title: "needs input", type: "needs_input" }, false)).toBe(false);
	});
});

describe("shouldSignalAttention", () => {
	it("in quiet mode signals for every decision class, never for review findings", () => {
		for (const type of SIGNAL_TYPES) {
			expect(shouldSignalAttention(type, false)).toBe(true);
		}
		expect(shouldSignalAttention("review_changes_requested", false)).toBe(false);
		expect(shouldSignalAttention(CLOUD_REVIEW_FEEDBACK, false)).toBe(false);
		expect(shouldSignalAttention("ready_to_merge", false)).toBe(false);
	});

	it("in loud mode restores the upstream actionable set", () => {
		expect(shouldSignalAttention("needs_input", true)).toBe(true);
		expect(shouldSignalAttention("ready_to_merge", true)).toBe(true);
		expect(shouldSignalAttention("review_changes_requested", true)).toBe(false);
		expect(shouldSignalAttention("pr_merged", true)).toBe(false);
		expect(shouldSignalAttention(undefined, true)).toBe(false);
	});
});

describe("bellUnreadCount", () => {
	it("counts every unread row in loud mode, like upstream", () => {
		expect(
			bellUnreadCount(
				[
					{ type: "needs_input", status: "unread" },
					{ type: "ready_to_merge", status: "unread" },
					{ type: "pr_merged", status: "unread" },
					{ type: "needs_input", status: "read" },
				],
				true,
			),
		).toBe(3);
	});

	it("is quiet by default", () => {
		expect(
			bellUnreadCount([{ type: "ready_to_merge", status: "unread" }], false),
		).toBe(0);
	});
});

describe("panelShowsNotification", () => {
	// The quiet panel's keep-set (fork issues #283 and #370): the decision
	// classes, the error classes, and future types, mirroring
	// backend/internal/domain/notification.go.
	const LISTED_TYPES: string[] = [
		"needs_input",
		"agent_failed",
		"orphan_workers",
		"stuck_delivery",
		"delivery_died",
	];
	const HIDDEN_TYPES: string[] = [
		"pr_merged",
		"review_completed",
		"pr_closed_unmerged",
		// Outcomes and review findings need no owner decision (fork issue #370).
		"ready_to_merge",
		"review_changes_requested",
		CLOUD_REVIEW_FEEDBACK,
	];

	it("lists the decision and error types in quiet mode", () => {
		for (const type of LISTED_TYPES) {
			expect(panelShowsNotification({ type }, false)).toBe(true);
		}
	});

	it("keeps outcomes and review findings out of the quiet feed (fork issue #370)", () => {
		for (const type of HIDDEN_TYPES) {
			expect(panelShowsNotification({ type }, false)).toBe(false);
		}
	});

	// A hide-list, not an allow-list: a future action-needed type must stay
	// visible by default, never silently dropped.
	it("lists unknown and future types in quiet mode", () => {
		expect(panelShowsNotification({ type: "some_future_type" }, false)).toBe(true);
		expect(panelShowsNotification({}, false)).toBe(true);
	});

	it("lists every type in loud mode, like upstream", () => {
		for (const type of [...LISTED_TYPES, ...HIDDEN_TYPES]) {
			expect(panelShowsNotification({ type }, true)).toBe(true);
		}
	});
});

// The owner's policy matrix, pinned per fork issue #370 (2026-11-20): for
// every backend and cloud type, what each quiet-mode surface does — the bell
// badge count, the OS toast, the dock bounce style, and the bell panel.
describe("quiet-mode policy matrix (fork issue #370)", () => {
	const MATRIX: Record<string, { badge: boolean; toast: boolean; dock: "critical" | "informational"; panel: boolean }> = {
		needs_input: { badge: true, toast: true, dock: "critical", panel: true },
		stuck_delivery: { badge: true, toast: true, dock: "informational", panel: true },
		agent_failed: { badge: true, toast: true, dock: "informational", panel: true },
		review_changes_requested: { badge: false, toast: false, dock: "informational", panel: false },
		ready_to_merge: { badge: false, toast: false, dock: "informational", panel: false },
		pr_merged: { badge: false, toast: false, dock: "informational", panel: false },
		pr_closed_unmerged: { badge: false, toast: false, dock: "informational", panel: false },
		review_completed: { badge: false, toast: false, dock: "informational", panel: false },
		delivery_died: { badge: false, toast: false, dock: "informational", panel: true },
		[CLOUD_REVIEW_FEEDBACK]: { badge: false, toast: false, dock: "informational", panel: false },
	};

	it("pins badge, toast, dock, and panel per notification type", () => {
		for (const [type, expected] of Object.entries(MATRIX)) {
			expect(bellUnreadCount([{ type, status: "unread" }], false), `badge: ${type}`).toBe(expected.badge ? 1 : 0);
			expect(shouldToast({ title: `${type} title`, type }, true), `toast: ${type}`).toBe(expected.toast);
			expect(dockBounceType(type), `dock: ${type}`).toBe(expected.dock);
			expect(panelShowsNotification({ type }, false), `panel: ${type}`).toBe(expected.panel);
		}
	});
});

describe("shouldReplaceBounce", () => {
	it("replaces no bounce or a pending informational bounce", () => {
		expect(shouldReplaceBounce(null)).toBe(true);
		expect(shouldReplaceBounce({ critical: false })).toBe(true);
	});

	it("never replaces a pending critical bounce, so a blocked agent stays loud", () => {
		expect(shouldReplaceBounce({ critical: true })).toBe(false);
	});
});

describe("osToastChimeControllable", () => {
	// Platform contract: Electron's mac and win presenters read `silent`;
	// the Linux libnotify presenter does not (no suppress-sound hint).
	it.each(["darwin", "win32"] as const)("is controllable on %s", (platform) => {
		expect(osToastChimeControllable(platform)).toBe(true);
	});

	it.each(["linux", "freebsd", "openbsd"] as const)("is not controllable on %s", (platform) => {
		expect(osToastChimeControllable(platform)).toBe(false);
	});
});

describe("toastSilent", () => {
	const HONOURING: NodeJS.Platform[] = ["darwin", "win32"];

	it.each(HONOURING)("on %s mutes the toast whenever sound notifications are off", (platform) => {
		expect(toastSilent(platform, false, false)).toBe(true);
		expect(toastSilent(platform, false, true)).toBe(true);
	});

	it.each(HONOURING)("on %s mutes the toast when AO plays its own sound", (platform) => {
		expect(toastSilent(platform, true, true)).toBe(true);
	});

	it.each(HONOURING)("on %s keeps the native chime for an informational toast when sound is on", (platform) => {
		expect(toastSilent(platform, true, false)).toBe(false);
	});

	// On Linux the value would be ignored, so no combination may claim a mute.
	it.each([
		[false, false],
		[false, true],
		[true, false],
		[true, true],
	])("on linux never claims control (enabled=%s, playsSound=%s)", (enabled, playsSound) => {
		expect(toastSilent("linux", enabled, playsSound)).toBeUndefined();
	});
});

describe("dockBounceType", () => {
	it("bounces critically for a blocked agent waiting on the user", () => {
		expect(dockBounceType("needs_input")).toBe("critical");
	});

	it("bounces once for the other signal types", () => {
		for (const type of SIGNAL_TYPES.filter((t) => t !== "needs_input")) {
			expect(dockBounceType(type)).toBe("informational");
		}
	});
});
