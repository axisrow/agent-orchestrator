import { act, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

const h = vi.hoisted(() => ({
	setAttentionState: vi.fn(),
	navigateToSession: vi.fn(),
	workspaces: [] as WorkspaceSummary[],
	listener: null as null | ((target: { projectId: string; sessionId: string }) => void),
}));

vi.mock("../hooks/useWorkspaceQuery", async (importOriginal) => {
	// The real selection: the runtime renders whatever the workspace facts
	// select, so the test cannot drift from the badge's actual predicate.
	const actual = await importOriginal<typeof import("../hooks/useWorkspaceQuery")>();
	return {
		...actual,
		useWorkspaceTraySessions: () => ({
			data: actual.selectTraySessions(h.workspaces),
		}),
	};
});

vi.mock("../lib/navigate-to-session", () => ({
	useNavigateToSession: () => h.navigateToSession,
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		tray: {
			setAttentionState: h.setAttentionState,
			onOpenSession: (listener: (target: { projectId: string; sessionId: string }) => void) => {
				h.listener = listener;
				return () => {
					h.listener = null;
				};
			},
		},
	},
}));

import { TrayRuntime } from "./TrayRuntime";

function worker(overrides: Partial<WorkspaceSession> & { id: string }): WorkspaceSession {
	return {
		workspaceId: "proj-1",
		workspaceName: "note-tauri",
		title: overrides.id,
		provider: "codex",
		kind: "worker",
		branch: `feature/${overrides.id}`,
		status: "working",
		updatedAt: "2026-06-10T00:00:00Z",
		prs: [],
		...overrides,
	};
}

function workspaces(): WorkspaceSummary[] {
	return [
		{
			id: "proj-1",
			name: "note-tauri",
			path: "/repos/note",
			type: "main",
			sessions: [
				worker({ id: "s-need", title: "needs it", status: "needs_input" }),
				worker({ id: "s-work", title: "working", status: "working" }),
				worker({ id: "s-merge", title: "merge me", status: "mergeable" }),
				worker({ id: "s-merged", title: "already merged", status: "merged" }),
				worker({ id: "orch", title: "orchestrator", kind: "orchestrator", status: "needs_input" }),
			],
		},
	];
}

afterEach(() => {
	h.setAttentionState.mockReset();
	h.navigateToSession.mockReset();
	h.listener = null;
});

describe("TrayRuntime", () => {
	it("pushes only waiting-input worker sessions to the tray", () => {
		h.workspaces = workspaces();
		render(<TrayRuntime />);
		expect(h.setAttentionState).toHaveBeenLastCalledWith({
			sessions: [
				{ projectId: "proj-1", projectName: "note-tauri", sessionId: "s-need", title: "needs it" },
			],
		});
	});

	it("navigates when main reports a tray open-session", () => {
		h.workspaces = workspaces();
		render(<TrayRuntime />);
		act(() => h.listener?.({ projectId: "proj-1", sessionId: "s-need" }));
		expect(h.navigateToSession).toHaveBeenCalledWith("proj-1", "s-need");
	});

	// Issue #353: merge, review and action zones never reach the badge.
	it("never counts a merge-ready session even when it needs attention elsewhere", () => {
		h.workspaces = workspaces();
		render(<TrayRuntime />);
		const ids = (h.setAttentionState.mock.calls.at(-1)?.[0]?.sessions ?? []).map((entry: { sessionId: string }) => entry.sessionId);
		expect(ids).toEqual(["s-need"]);
	});

	// Issue #328: interacting with the menu must never mutate the attention set.
	it("selecting a session only navigates and leaves the pushed set untouched", () => {
		h.workspaces = workspaces();
		render(<TrayRuntime />);
		const pushesAfterRender = h.setAttentionState.mock.calls.length;
		act(() => h.listener?.({ projectId: "proj-1", sessionId: "s-merge" }));
		expect(h.navigateToSession).toHaveBeenCalledWith("proj-1", "s-merge");
		expect(h.setAttentionState).toHaveBeenCalledTimes(pushesAfterRender);
	});

	it("re-renders over unchanged facts without re-pushing the set", () => {
		h.workspaces = workspaces();
		const { rerender } = render(<TrayRuntime />);
		const pushesAfterRender = h.setAttentionState.mock.calls.length;
		rerender(<TrayRuntime />);
		expect(h.setAttentionState).toHaveBeenCalledTimes(pushesAfterRender);
	});
});
