import type { BrowserWindow, IpcMainEvent, WebContents } from "electron";
import type { TrayController } from "./tray";
import { TRAY_OPEN_SESSION_CHANNEL, type TrayAttentionState, type TrayOpenSessionTarget, type TraySessionEntry } from "../shared/tray";

export function isTrayEnabled(platform: NodeJS.Platform): boolean {
	return platform === "darwin";
}

export type TrayNotificationEvent = "tray_notification_added" | "tray_notification_removed";

export type TrayNotificationFields = {
	sessionId: string;
	projectId: string;
	title: string;
	count: number;
	reason: string;
};

export type TrayLifecycleDeps = {
	getWindow: () => BrowserWindow | null;
	getContents?: () => WebContents | null;
	getTrayController: () => TrayController | null;
	focusWindow: () => void;
	/** Structured local record of badge transitions (tray-notifications.jsonl, #353). */
	logNotification?: (event: TrayNotificationEvent, fields: TrayNotificationFields) => void;
};

export type TrayLifecycle = {
	openSession(target: TrayOpenSessionTarget): void;
	handleSetAttentionState(event: IpcMainEvent, state: TrayAttentionState): void;
	handleRendererReady(event: IpcMainEvent): void;
	clearPendingTarget(): void;
	clear(): void;
	dispose(): void;
};

export function createTrayLifecycle(deps: TrayLifecycleDeps): TrayLifecycle {
	let pendingTarget: TrayOpenSessionTarget | null = null;
	// The sessions this process last accepted from the renderer. The tray badge
	// can only change through a pushed set or a lifecycle reset, so the diff of
	// this list is every add/remove transition the user observes.
	let logged: TraySessionEntry[] = [];

	function getContents(): WebContents | null {
		return deps.getContents?.() ?? deps.getWindow()?.webContents ?? null;
	}

	function isFromMainWindow(event: IpcMainEvent): boolean {
		const contents = getContents();
		return contents !== null && event.sender === contents;
	}

	function logTransition(next: TraySessionEntry[], removalReason: string): void {
		if (!deps.logNotification) {
			logged = next;
			return;
		}
		const nextIds = new Set(next.map((entry) => entry.sessionId));
		for (const prev of logged) {
			if (nextIds.has(prev.sessionId)) continue;
			deps.logNotification("tray_notification_removed", {
				sessionId: prev.sessionId,
				projectId: prev.projectId,
				title: prev.title,
				count: next.length,
				reason: removalReason,
			});
		}
		const prevIds = new Set(logged.map((entry) => entry.sessionId));
		for (const entry of next) {
			if (prevIds.has(entry.sessionId)) continue;
			deps.logNotification("tray_notification_added", {
				sessionId: entry.sessionId,
				projectId: entry.projectId,
				title: entry.title,
				count: next.length,
				reason: "waiting_input",
			});
		}
		logged = next;
	}

	function clearPendingTarget(): void {
		pendingTarget = null;
	}

	return {
		openSession(target) {
			const contents = getContents();
			deps.focusWindow();
			if (contents && !contents.isLoading()) {
				contents.send(TRAY_OPEN_SESSION_CHANNEL, target);
				return;
			}
			pendingTarget = target;
		},
		handleSetAttentionState(event, state) {
			const tray = deps.getTrayController();
			if (!tray || !isFromMainWindow(event)) return;
			// Opening or closing the tray menu never reaches this channel: the set
			// moves only when the renderer's workspace facts move, so a menu that is
			// opened and closed without a selection cannot reset the badge (#353).
			const next = state.sessions ?? [];
			logTransition(next, "no_longer_waiting_input");
			tray.setState(state);
		},
		handleRendererReady(event) {
			if (!isFromMainWindow(event)) return;
			const target = pendingTarget;
			pendingTarget = null;
			if (target) getContents()?.send(TRAY_OPEN_SESSION_CHANNEL, target);
		},
		clear() {
			clearPendingTarget();
			logTransition([], "renderer_reload");
			deps.getTrayController()?.clear();
		},
		clearPendingTarget,
		dispose() {
			logTransition([], "app_quit");
			deps.getTrayController()?.dispose();
		},
	};
}
