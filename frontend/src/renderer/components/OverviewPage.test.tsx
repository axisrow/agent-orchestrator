import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

const h = vi.hoisted(() => ({
	workspaces: [] as WorkspaceSummary[],
	worktrees: [] as { sessionId: string; projectId?: string; path: string; bytes: number; terminated: boolean }[],
	navigate: vi.fn(),
	toast: vi.fn(),
	cleanupMutate: vi.fn(),
	cleanupPending: false,
}));

vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => h.navigate,
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: h.workspaces, isLoading: false, isError: false }),
}));

vi.mock("../hooks/useWorktrees", () => ({
	useWorktreesQuery: () => ({ data: h.worktrees }),
	useCleanupWorktrees: () => ({
		mutate: h.cleanupMutate,
		isPending: h.cleanupPending,
		isError: false,
		error: null,
	}),
}));

vi.mock("../stores/ui-store", () => ({
	useUiStore: (selector: (state: { showGlobalToast: unknown }) => unknown) =>
		selector({ showGlobalToast: h.toast }),
}));

vi.mock("./ProductExternalLink", () => ({
	ProductExternalLink: ({ children, href }: { children: React.ReactNode; href: string }) => (
		<a href={href}>{children}</a>
	),
}));

import { OverviewPage } from "./OverviewPage";

function session(overrides: Partial<WorkspaceSession> & { id: string }): WorkspaceSession {
	return {
		workspaceId: "proj-beam",
		workspaceName: "beamcraft",
		title: overrides.id,
		provider: "claude-code",
		kind: "worker",
		status: "idle",
		updatedAt: "2026-11-12T00:00:00Z",
		activity: { state: "idle", lastActivityAt: "2026-11-12T00:00:00Z" },
		prs: [],
		...overrides,
	};
}

function pr(overrides: Partial<WorkspaceSession["prs"][number]> & { url: string; number: number }): WorkspaceSession["prs"][number] {
	return {
		state: "open",
		ci: "passing",
		review: "approved",
		mergeability: "mergeable",
		reviewComments: false,
		updatedAt: "2026-11-12T00:00:00Z",
		...overrides,
	};
}

function workspaces(): WorkspaceSummary[] {
	return [
		{
			id: "proj-beam",
			name: "beamcraft",
			path: "/repos/beamcraft",
			type: "main",
			sessions: [
				session({ id: "s-orch", title: "orchestrator", kind: "orchestrator", activity: { state: "active", lastActivityAt: "" } }),
				session({
					id: "s-gate-open",
					title: "gate open",
					kanbanColumn: "ready",
					prs: [pr({ url: "https://github.com/aoagents/agent-orchestrator/pull/1", number: 1 })],
				}),
				session({
					id: "s-checks",
					title: "checks running",
					prs: [pr({ url: "https://github.com/aoagents/agent-orchestrator/pull/2", number: 2, ci: "pending" })],
				}),
				session({
					id: "s-changes",
					title: "changes requested",
					prs: [pr({ url: "https://github.com/aoagents/agent-orchestrator/pull/3", number: 3, review: "changes_requested" })],
				}),
				session({
					id: "s-closed",
					title: "closed without merge",
					kanbanColumn: "archive",
					prs: [pr({ url: "https://github.com/aoagents/agent-orchestrator/pull/4", number: 4, state: "closed" })],
				}),
				session({
					id: "s-unknown-ci",
					title: "cancelled checks",
					prs: [pr({ url: "https://github.com/aoagents/agent-orchestrator/pull/5", number: 5, ci: "unknown" })],
				}),
				session({ id: "s-blocked", title: "needs you", activity: { state: "waiting_input", lastActivityAt: "" } }),
				session({
					id: "s-exited",
					title: "old worker",
					isTerminated: true,
					kanbanColumn: "archive",
					activity: { state: "active", lastActivityAt: "" },
				}),
				session({ id: "s-spawning", title: "just spawned", provisionState: "provisioning" }),
			],
		},
		{
			id: "proj-note",
			name: "note-tauri",
			path: "/repos/note",
			type: "main",
			sessions: [],
		},
	];
}

function worktrees(): typeof h.worktrees {
	return [
		{ sessionId: "s-exited", projectId: "proj-beam", path: "/wt/s-exited", bytes: 4096, terminated: true },
		{ sessionId: "s-gate-open", projectId: "proj-beam", path: "/wt/s-gate-open", bytes: 2048, terminated: false },
	];
}

afterEach(() => {
	h.navigate.mockReset();
	h.toast.mockReset();
	h.cleanupMutate.mockReset();
});

describe("OverviewPage", () => {
	it("groups sessions under project bands with worktree totals and a cleanup action", () => {
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		render(<OverviewPage />);

		expect(screen.getByText("beamcraft")).toBeInTheDocument();
		expect(screen.getByText("note-tauri")).toBeInTheDocument();
		const band = screen.getByText("6 KB · 1 to reclaim");
		expect(band).toBeInTheDocument();
		// note-tauri has no worktrees: nothing to reclaim, so cleanup is off.
		const beamRow = band.closest("tr")!;
		expect(beamRow.textContent).toContain("Clean up");
		const cleanupButtons = screen.getAllByRole("button", { name: "Clean up" });
		expect(cleanupButtons.some((button) => (button as HTMLButtonElement).disabled)).toBe(true);
		expect(screen.getByText("No sessions.", { selector: "td" })).toBeInTheDocument();
	});

	it("renders the lifecycle stage and merge gate per session", () => {
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		render(<OverviewPage />);

		// Stages from kanbanColumn/status: building → Working, ready → Ready,
		// provisioning → Spawning; terminated sessions land in Archive even
		// when their stale activity still says active.
		expect(screen.getByText("Spawning")).toBeInTheDocument();
		expect(screen.getAllByText("Working").length).toBeGreaterThan(0);
		expect(screen.getAllByText("Ready").length).toBeGreaterThan(0);
		expect(screen.getAllByText("Archive").length).toBe(2);
		expect(screen.getByText("Exited")).toBeInTheDocument();

		// Merge gate: open only when the PR facts allow a merge; a closed PR
		// shows the split-archive phrase, and unknown CI (cancelled checks on
		// the fork) renders no verdict instead of a false provider-block.
		expect(screen.getByText("Gate open")).toBeInTheDocument();
		expect(screen.getByText("Checks running")).toBeInTheDocument();
		expect(screen.getByText("Changes requested")).toBeInTheDocument();
		expect(screen.getByText("Closed without merge")).toBeInTheDocument();
		expect(screen.getByText("#1")).toBeInTheDocument();
		expect(screen.getByText("#2")).toBeInTheDocument();
		const unknownCiRow = screen.getByRole("button", { name: "cancelled checks" }).closest("tr")!;
		expect(unknownCiRow.textContent).toContain("#5");
		expect(unknownCiRow.textContent).not.toContain("Blocked by provider");
	});

	it("links a session through the session route target", () => {
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		render(<OverviewPage />);

		fireEvent.click(screen.getByRole("button", { name: "gate open" }));
		expect(h.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-beam", sessionId: "s-gate-open" },
		});
	});

	it("filters by status without losing the project bands", async () => {
		const user = userEvent.setup();
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		render(<OverviewPage />);

		await user.click(screen.getByRole("combobox", { name: "Status" }));
		await user.click(screen.getByRole("option", { name: "Exited" }));

		expect(screen.queryByRole("button", { name: "gate open" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "old worker" })).toBeInTheDocument();
		expect(screen.getByText("beamcraft")).toBeInTheDocument();
	});

	it("cleans up a project's worktrees after confirmation and reports the freed size", async () => {
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		h.cleanupMutate.mockImplementation((_projectId: string, options: { onSuccess: (result: unknown) => void }) => {
			options.onSuccess({ reclaimed: [{ bytes: 4096 }] });
		});
		render(<OverviewPage />);

		const band = screen.getByText("6 KB · 1 to reclaim").closest("tr")!;
		fireEvent.click(within(band).getByRole("button", { name: "Clean up" }));

		const dialog = await screen.findByRole("dialog");
		expect(dialog.textContent).toContain("beamcraft");

		fireEvent.click(screen.getByRole("button", { name: "Clean up" }));
		expect(h.cleanupMutate).toHaveBeenCalledWith("proj-beam", expect.anything());
		await waitFor(() => {
			expect(h.toast).toHaveBeenCalledWith("Worktrees cleaned", "Freed 4 KB in beamcraft.");
		});
	});
});
