// The tray surfaces only sessions whose agent is waiting on the user. Merge
// and action zones stay in the sidebar and board; they never reach the badge
// or the menu, so the count means "someone is blocked on you" and nothing else.
export type TraySessionEntry = {
	projectId: string;
	projectName: string;
	sessionId: string;
	title: string;
};

export type TrayAttentionState = {
	sessions: TraySessionEntry[];
};

export type TrayOpenSessionTarget = {
	projectId: string;
	sessionId: string;
};

export const TRAY_SET_ATTENTION_STATE_CHANNEL = "tray:setAttentionState";
export const TRAY_OPEN_SESSION_CHANNEL = "tray:openSession";
export const TRAY_RENDERER_READY_CHANNEL = "tray:rendererReady";
