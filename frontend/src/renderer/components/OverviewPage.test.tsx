import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

const h = vi.hoisted(() => ({
	workspaces: [] as WorkspaceSummary[],
	worktrees: [] as { sessionId: string; projectId?: string; path: string; bytes: number; terminated: boolean }[],
	stuckBySession: new Map<string, number>(),
	inventory: undefined as Record<string, unknown> | undefined,
	navigate: vi.fn(),
	toast: vi.fn(),
	cleanupMutate: vi.fn(),
	cleanupPending: false,
	terminateMutate: vi.fn(),
	terminatePending: false,
	terminateError: null as unknown,
	setInspectorView: vi.fn(),
	setInspectorOpen: vi.fn(),
	processKill: vi.fn(),
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

vi.mock("../hooks/useStuckDeliveries", () => ({
	useStuckDeliveriesBySession: () => h.stuckBySession,
}));

vi.mock("../hooks/useProcessInventoryQuery", () => ({
	useProcessInventoryQuery: () => ({ data: h.inventory, refetch: vi.fn() }),
	isStoppableOwnedTree: (tree: { state: string; activityState?: string }) =>
		tree.state === "owned" && (tree.activityState === "idle" || tree.activityState === "exited"),
	activityStateLabelKey: (state: string | undefined) =>
		state === "active" ? "statusBar.activityActive" : state ? `statusBar.activity${state}` : "",
}));

vi.mock("../hooks/useTerminateSession", () => ({
	useTerminateSession: () => ({
		mutateAsync: h.terminateMutate,
		isPending: h.terminatePending,
		isError: h.terminateError !== null,
		error: h.terminateError,
	}),
}));

vi.mock("../stores/ui-store", () => ({
	useUiStore: Object.assign(
		(selector: (state: { showGlobalToast: unknown; setInspectorView: unknown; setInspectorOpen: unknown }) => unknown) =>
			selector({ showGlobalToast: h.toast, setInspectorView: h.setInspectorView, setInspectorOpen: h.setInspectorOpen }),
		{
			getState: () => ({
				showGlobalToast: h.toast,
				setInspectorView: h.setInspectorView,
				setInspectorOpen: h.setInspectorOpen,
			}),
		},
	),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: vi.fn(),
		POST: h.processKill,
	},
	apiErrorMessage: (error: unknown) => `api error: ${String(error)}`,
}));

vi.mock("./ProductExternalLink", () => ({
	ProductExternalLink: ({ children, href }: { children: React.ReactNode; href: string }) => (
		<a href={href}>{children}</a>
	),
}));

import { TooltipProvider } from "./ui/tooltip";
import { OverviewPage } from "./OverviewPage";

// OverviewResources reads the shared query client for inventory invalidation,
// and its MemoryPopover needs the tooltip provider; wrap the page like the
// shell does.
function renderPage() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<OverviewPage />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

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
		// Fresh by default so fixtures never rot past the visibility window.
		updatedAt: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
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
				session({
					id: "s-merged",
					title: "merged worker",
					kanbanColumn: "ready",
					prs: [pr({ url: "https://github.com/aoagents/agent-orchestrator/pull/6", number: 6, state: "merged" })],
				}),
				session({ id: "s-blocked", title: "needs you", activity: { state: "waiting_input", lastActivityAt: "" } }),
				session({
					id: "s-exited",
					title: "old worker",
					isTerminated: true,
					kanbanColumn: "archive",
					activity: { state: "active", lastActivityAt: "" },
				}),
				session({
					id: "s-archived-dirty",
					title: "dirty archive",
					isTerminated: true,
					workspaceCleanup: "preserved_dirty",
					kanbanColumn: "archive",
				}),
				session({
					id: "s-archived-pr",
					title: "archive with pr",
					isTerminated: true,
					kanbanColumn: "archive",
					prs: [pr({ url: "https://github.com/aoagents/agent-orchestrator/pull/7", number: 7 })],
				}),
				session({
					id: "s-archived-stale-pr",
					title: "stale review",
					isTerminated: true,
					kanbanColumn: "archive",
					// The issue's flood case: an open PR whose "Review required"
					// has been sitting for weeks. A fixed date only grows staler,
					// so the fixture stays valid as the wall clock moves on.
					prs: [pr({ url: "https://github.com/aoagents/agent-orchestrator/pull/8", number: 8, updatedAt: "2026-10-18T00:00:00Z" })],
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
		{ sessionId: "s-archived-dirty", projectId: "proj-beam", path: "/wt/dirty", bytes: 8192, terminated: true },
		{ sessionId: "s-gate-open", projectId: "proj-beam", path: "/wt/s-gate-open", bytes: 2048, terminated: false },
	];
}

function inventoryWithOrphans() {
	return {
		disabled: false,
		host: { totalBytes: 24, usedBytes: 8, freeBytes: 16, cachedBytes: 1, swapTotalBytes: 0, swapUsedBytes: 0, swapMaxBytes: 0 },
		trees: [
			{ sessionId: "legacy-x", rootPid: 8123, rootLstart: "Mon Sep 14 00:13:02 2026", pidCount: 3, rssBytes: 1153433600, kind: "worker", state: "orphan", attached: false },
		],
		totals: { sessionsCount: 0, sessionsRssBytes: 0, orphansCount: 1, orphansRssBytes: 1153433600, foreignCount: 0, foreignRssBytes: 0, daemonRssBytes: 0, tmuxRssBytes: 0 },
	};
}

afterEach(() => {
	h.navigate.mockReset();
	h.toast.mockReset();
	h.cleanupMutate.mockReset();
	h.terminateMutate.mockReset();
	h.setInspectorView.mockReset();
	h.setInspectorOpen.mockReset();
	h.processKill.mockReset();
	h.stuckBySession = new Map();
	h.inventory = undefined;
});

describe("OverviewPage", () => {
	it("groups sessions under project bands with worktree totals and a cleanup action", () => {
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		renderPage();

		expect(screen.getByText("beamcraft")).toBeInTheDocument();
		expect(screen.getByText("note-tauri")).toBeInTheDocument();
		expect(screen.getByText("10 KB · 1 to reclaim")).toBeInTheDocument();
		// note-tauri has no worktrees: nothing to reclaim, so cleanup is off.
		const cleanupButtons = screen.getAllByRole("button", { name: "Clean up" });
		expect(cleanupButtons.some((button) => (button as HTMLButtonElement).disabled)).toBe(true);
		expect(screen.getByText("No sessions.")).toBeInTheDocument();
	});

	it("renders the lifecycle stage and merge gate per session row", () => {
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		renderPage();

		// Stages from kanbanColumn/status: building → Working, ready → Ready,
		// provisioning → Spawning; a merged PR reads as Merged, and the one
		// archive-column survivor (closed PR, not terminated) stays Archive.
		expect(screen.getByText("Spawning")).toBeInTheDocument();
		expect(screen.getAllByText("Working").length).toBeGreaterThan(0);
		expect(screen.getAllByText("Ready").length).toBeGreaterThan(0);
		expect(screen.getAllByText("Merged").length).toBeGreaterThan(0);
		expect(screen.getAllByText("Archive").length).toBeGreaterThanOrEqual(1);

		// Merge gate: open only when the PR facts allow a merge; a closed PR
		// shows the split-archive phrase, and unknown CI (cancelled checks on
		// the fork) renders no verdict instead of a false provider-block.
		expect(screen.getAllByText("Gate open").length).toBeGreaterThanOrEqual(1);
		expect(screen.getByText("Checks running")).toBeInTheDocument();
		expect(screen.getByText("Changes requested")).toBeInTheDocument();
		expect(screen.getByText("Closed without merge")).toBeInTheDocument();
		expect(screen.getByText("#1")).toBeInTheDocument();
		const unknownCiRow = screen.getByRole("button", { name: "cancelled checks" }).closest("div")!;
		expect(unknownCiRow.textContent).toContain("#5");
		expect(unknownCiRow.textContent).not.toContain("Blocked by provider");
	});

	it("hides cleanly terminated sessions behind the archived toggle", async () => {
		const user = userEvent.setup();
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		renderPage();

		// A plain terminated session with no PR, no undelivered messages, and no
		// kept-dirty worktree is history: hidden until the toggle turns.
		expect(screen.queryByRole("button", { name: "old worker" })).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Show archived" }));
		expect(screen.getByRole("button", { name: "old worker" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Hide archived" })).toBeInTheDocument();
	});

	it("keeps signal-bearing terminated sessions visible without the toggle", () => {
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		renderPage();

		// Kept-dirty worktree (cleanup would discard it), an open PR, and
		// undelivered messages are the GC and merge signals the screen exists for.
		expect(screen.getByRole("button", { name: "dirty archive" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "archive with pr" })).toBeInTheDocument();
	});

	it("hides a terminated review session whose open PR went stale past the activity window", async () => {
		const user = userEvent.setup();
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		renderPage();

		// The issue's flood case: the PR is still open, but it has not moved
		// in weeks, so a fresh PR keeps its session visible while the stale
		// one hides with the toggle off and returns once the toggle asks.
		expect(screen.getByRole("button", { name: "archive with pr" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "stale review" })).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Show archived" }));
		expect(screen.getByRole("button", { name: "stale review" })).toBeInTheDocument();
	});

	it("shows the hidden archive scale next to the toggle until the toggle reveals it", async () => {
		const user = userEvent.setup();
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		renderPage();

		// The plain terminated row and the stale-review row are what the
		// default view currently hides.
		expect(screen.getByTestId("overview-archived-count").textContent).toBe("Archived (2)");
		await user.click(screen.getByRole("button", { name: "Show archived" }));
		expect(screen.queryByTestId("overview-archived-count")).not.toBeInTheDocument();
	});

	it("treats an Archive stage filter as an explicit request for archived rows", async () => {
		const user = userEvent.setup();
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		renderPage();

		await user.click(screen.getByRole("combobox", { name: "Stage" }));
		await user.click(screen.getByRole("option", { name: "Archive" }));

		expect(screen.getByRole("button", { name: "stale review" })).toBeInTheDocument();
		expect(screen.queryByTestId("overview-archived-count")).not.toBeInTheDocument();
	});

	it("treats an Exited status filter as an explicit request for archived rows", async () => {
		const user = userEvent.setup();
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		renderPage();

		await user.click(screen.getByRole("combobox", { name: "Status" }));
		await user.click(screen.getByRole("option", { name: "Exited" }));

		expect(screen.getByRole("button", { name: "stale review" })).toBeInTheDocument();
		expect(screen.queryByTestId("overview-archived-count")).not.toBeInTheDocument();
	});

	it("shows undelivered counters from the delivery journal", () => {
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		h.stuckBySession = new Map([["s-blocked", 2]]);
		renderPage();

		// The session row carries the count chip and the project band sums it.
		expect(screen.getAllByTitle("2 undelivered")).toHaveLength(2);
	});

	it("links a session through the session route target", () => {
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		renderPage();

		fireEvent.click(screen.getByRole("button", { name: "gate open" }));
		expect(h.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-beam", sessionId: "s-gate-open" },
		});
	});

	it("opens the PR card by navigating with the inspector summary forced open", () => {
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		renderPage();

		// The first PR row is s-gate-open; its card action navigates and opens
		// the inspector's summary view where the PR card lives.
		fireEvent.click(screen.getAllByRole("button", { name: "Open pull request" })[0]);
		expect(h.setInspectorView).toHaveBeenCalledWith("s-gate-open", "summary");
		expect(h.setInspectorOpen).toHaveBeenCalledWith("s-gate-open", true);
		expect(h.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-beam", sessionId: "s-gate-open" },
		});
	});

	it("filters by status without losing the project bands", async () => {
		const user = userEvent.setup();
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		renderPage();

		await user.click(screen.getByRole("combobox", { name: "Status" }));
		await user.click(screen.getByRole("option", { name: "Blocked" }));

		expect(screen.queryByRole("button", { name: "gate open" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "needs you" })).toBeInTheDocument();
		expect(screen.getByText("beamcraft")).toBeInTheDocument();
	});

	it("cleans up a project worktrees after confirmation and reports the freed size", async () => {
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		h.cleanupMutate.mockImplementation((_projectId: string, options: { onSuccess: (result: unknown) => void }) => {
			options.onSuccess({ reclaimed: [{ bytes: 8192 }] });
		});
		renderPage();

		fireEvent.click(screen.getAllByRole("button", { name: "Clean up" })[0]);

		const dialog = await screen.findByRole("dialog");
		expect(dialog.textContent).toContain("beamcraft");

		fireEvent.click(within(dialog).getByRole("button", { name: "Clean up" }));
		expect(h.cleanupMutate).toHaveBeenCalledWith("proj-beam", expect.anything());
		await waitFor(() => {
			expect(h.toast).toHaveBeenCalledWith("Worktrees cleaned", "Freed 8 KB in beamcraft.");
		});
	});

	it("terminates one session after a confirmation naming the session and project", async () => {
		const user = userEvent.setup();
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		h.terminateMutate.mockResolvedValue(undefined);
		renderPage();

		await user.click(screen.getByRole("button", { name: "Terminate gate open" }));

		const dialog = await screen.findByRole("dialog");
		expect(dialog.textContent).toContain("Terminate gate open?");
		expect(dialog.textContent).toContain("beamcraft");

		await user.click(within(dialog).getByRole("button", { name: "Terminate selected sessions" }));
		expect(h.terminateMutate).toHaveBeenCalledWith(expect.objectContaining({ id: "s-gate-open" }));
	});

	it("warns before terminating a session that is waiting for the human", async () => {
		const user = userEvent.setup();
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		h.terminateMutate.mockResolvedValue(undefined);
		renderPage();

		await user.click(screen.getByRole("button", { name: "Terminate needs you" }));

		const dialog = await screen.findByRole("dialog");
		expect(dialog.textContent).toContain("waiting for your input");
		expect(dialog.textContent).toContain("silence the question");
	});

	it("terminates a batch after a confirmation listing every affected session", async () => {
		const user = userEvent.setup();
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		h.terminateMutate.mockResolvedValue(undefined);
		renderPage();

		await user.click(screen.getByRole("checkbox", { name: "Select gate open" }));
		await user.click(screen.getByRole("checkbox", { name: "Select needs you" }));
		expect(screen.getByTestId("overview-bulk-bar").textContent).toContain("2 selected");

		await user.click(within(screen.getByTestId("overview-bulk-bar")).getByRole("button", { name: "Terminate" }));

		const dialog = await screen.findByRole("dialog");
		expect(dialog.textContent).toContain("Terminate 2 sessions?");
		expect(dialog.textContent).toContain("gate open — beamcraft");
		expect(dialog.textContent).toContain("needs you — beamcraft");
		// The blocked session in the batch carries the human-input warning.
		expect(dialog.textContent).toContain("waiting for your input");

		await user.click(within(dialog).getByRole("button", { name: "Terminate selected sessions" }));
		await waitFor(() => {
			expect(h.terminateMutate).toHaveBeenCalledTimes(2);
		});
	});

	it("drops filtered-away rows from the batch instead of terminating invisible sessions", async () => {
		const user = userEvent.setup();
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		h.terminateMutate.mockResolvedValue(undefined);
		renderPage();

		await user.click(screen.getByRole("checkbox", { name: "Select gate open" }));
		await user.click(screen.getByRole("checkbox", { name: "Select needs you" }));
		expect(screen.getByTestId("overview-bulk-bar").textContent).toContain("2 selected");

		// Filtering hides one of the two rows: the batch shrinks to what the
		// screen still shows, so Terminate can never reach the hidden session.
		await user.click(screen.getByRole("combobox", { name: "Status" }));
		await user.click(screen.getByRole("option", { name: "Blocked" }));

		expect(screen.getByTestId("overview-bulk-bar").textContent).toContain("1 selected");
		await user.click(within(screen.getByTestId("overview-bulk-bar")).getByRole("button", { name: "Terminate" }));

		const dialog = await screen.findByRole("dialog");
		// One surviving row renders the single-session dialog naming session
		// and project; the filtered-away row is not part of it.
		expect(dialog.textContent).toContain("Terminate needs you?");
		expect(dialog.textContent).toContain("beamcraft");
		expect(dialog.textContent).not.toContain("gate open");

		await user.click(within(dialog).getByRole("button", { name: "Terminate selected sessions" }));
		await waitFor(() => {
			expect(h.terminateMutate).toHaveBeenCalledTimes(1);
		});
		expect(h.terminateMutate).toHaveBeenCalledWith(expect.objectContaining({ id: "s-blocked" }));
	});

	it("selects every visible non-terminated session from the header checkbox", async () => {
		const user = userEvent.setup();
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		renderPage();

		await user.click(screen.getByRole("checkbox", { name: "Select sessions" }));

		expect(screen.getByTestId("overview-bulk-bar").textContent).not.toContain("0 selected");
		const bar = screen.getByTestId("overview-bulk-bar").textContent ?? "";
		expect(bar).toMatch(/\bselected/);
		// Terminated rows have no checkbox, so the batch cannot pick them.
		expect(screen.queryByRole("checkbox", { name: "Select dirty archive" })).not.toBeInTheDocument();
	});

	it("renders the orphan section with warning-marked rows and a kill action", async () => {
		const user = userEvent.setup();
		h.workspaces = workspaces();
		h.worktrees = worktrees();
		h.inventory = inventoryWithOrphans();
		h.processKill.mockResolvedValue({ data: { results: [] }, error: undefined });
		renderPage();

		expect(screen.getByTestId("overview-orphans")).toBeInTheDocument();
		expect(screen.getByTestId("overview-orphans").textContent).toContain("legacy-x · pid 8123");

		await user.click(screen.getByRole("button", { name: "Kill orphan legacy-x" }));
		await waitFor(() => {
			expect(h.processKill).toHaveBeenCalledWith("/api/v1/system/processes/kill", {
				body: { targets: [{ sessionId: "legacy-x", rootPid: 8123, rootLstart: "Mon Sep 14 00:13:02 2026" }] },
			});
		});
	});
});
