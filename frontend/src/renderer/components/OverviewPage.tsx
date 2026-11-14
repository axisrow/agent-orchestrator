import { useMemo, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { ScrollArea } from "radix-ui";
import { useTranslation } from "react-i18next";
import { Archive, ChevronRight, Folder, FolderOpen, GitPullRequest, Trash2, X } from "lucide-react";
import { getKanbanColumnView } from "@aoagents/product-ui";
import { ConfirmDialog } from "./ConfirmDialog";
import { OverviewOrphans, OverviewResources } from "./OverviewResources";
import { OrchestratorIcon } from "./icons";
import { NavRowHighlight } from "./NavRowHighlight";
import { ProductExternalLink } from "./ProductExternalLink";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Checkbox } from "./ui/checkbox";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Skeleton } from "./ui/skeleton";
import { useTerminateSession } from "../hooks/useTerminateSession";
import { useCleanupWorktrees, useWorktreesQuery, type WorktreeUsage } from "../hooks/useWorktrees";
import { useStuckDeliveriesBySession } from "../hooks/useStuckDeliveries";
import { useProcessInventoryQuery } from "../hooks/useProcessInventoryQuery";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import type { KanbanColumn, WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { openPRs, sortedPRs } from "../types/workspace";
import { apiErrorMessage } from "../lib/api-client";
import { formatBytes } from "../lib/format-bytes";
import { formatTimeCompact, formatTimeTerse } from "../lib/format-time";
import { projectNavigateTarget, sessionNavigateTarget } from "../lib/navigate-to-session";
import { prCanMerge, sessionPRDisplaySummaries } from "../lib/pr-display";
import { getAgentActivityView, getOrchestratorStatusDotView, getSessionStatusDotView } from "../lib/session-presentation";
import { hidesShellTopbar } from "../lib/platform";
import { useUiStore } from "../stores/ui-store";
import type { MessageKey } from "../i18n";
import { cn } from "../lib/utils";

// The exited view for a terminated session whose last recorded activity may
// still say "active": the session is over, so the status column must not.
const EXITED_ACTIVITY = { state: "exited", lastActivityAt: "" } as const;

type StatusFilter = "all" | "working" | "idle" | "blocked" | "exited";

// One column of the issue's lifecycle: what the daemon's kanbanColumn already
// answers. Spawning is the pre-worktree provisioning slice of "building".
const STAGE_FILTERS: KanbanColumn[] = ["building", "validating", "needs_review", "ready", "archive"];

// Row chrome mirrors the left Projects panel (issue #330): the same 32px rows
// and hover-reveal action buttons. A copy of the sidebar's action-button class
// so the overview carries the identical visual language without importing the
// sidebar's module graph.
const ROW_ACTION_BUTTON_CLASS =
	"sidebar-icon-action grid size-6 shrink-0 place-items-center rounded-md !bg-transparent text-passive hover:!bg-interactive-hover focus:!bg-transparent focus-visible:!bg-interactive-hover active:!bg-interactive-hover data-[state=open]:!bg-interactive-hover hover:text-foreground focus-visible:text-foreground disabled:pointer-events-none disabled:opacity-50 data-[state=open]:text-foreground [&_svg]:size-icon-md";

// The activity-level status the table scans by: working / idle / blocked /
// exited. Deliberately coarser than the delivery facts, which live in the
// stage and merge-gate columns.
function overviewStatus(session: WorkspaceSession): Exclude<StatusFilter, "all"> {
	if (session.isTerminated || session.activity?.state === "exited") return "exited";
	const state = session.activity?.state;
	if (state === "waiting_input" || state === "blocked") return "blocked";
	if (state === "active") return "working";
	return "idle";
}

// Whether the session's merge gate is open, and the one fact holding it shut
// when it is not. Reads the same facts the PR card's ao merge flow reads:
// PR open, checks green, no conflicts, review approved (or not required), no
// unresolved comments. Returns undefined when no fact holds a verdict —
// a closed PR shows the split-archive phrase, and an unknown CI state (the
// fork maps cancelled checks to unknown) must not read as provider-blocked.
function overviewMergeGate(session: WorkspaceSession, t: (key: MessageKey, values?: Record<string, unknown>) => string): { open: boolean; label: string } | undefined {
	const [pr] = sessionPRDisplaySummaries(session);
	if (!pr) return undefined;
	if (pr.state === "merged") return { open: true, label: t("displayStatus.merged") };
	if (pr.state === "closed") return { open: false, label: t("displayStatus.closedWithoutMerge") };
	if (pr.state === "draft") return { open: false, label: t("overview.gateDraft") };
	if (prCanMerge(pr)) return { open: true, label: t("overview.gateOpen") };
	if (pr.review.decision === "changes_requested") return { open: false, label: t("overview.gateChanges") };
	if (pr.review.decision === "review_required") return { open: false, label: t("overview.gateReview") };
	if (pr.review.hasUnresolvedHumanComments) return { open: false, label: t("overview.gateComments") };
	if (pr.ci.state === "failing") return { open: false, label: t("overview.gateCi") };
	if (pr.mergeability.state === "conflicting") return { open: false, label: t("overview.gateConflicts") };
	if (pr.ci.state === "pending") return { open: false, label: t("overview.gateChecks") };
	if (pr.ci.state === "unknown") return undefined;
	return { open: false, label: t("overview.gateProvider") };
}

// A terminated session hides from the default view (issue #330) unless it
// still carries a signal: a kept dirty worktree cleanup would discard, an
// open PR still waiting, or undelivered messages. Everything else about it is
// history the "show archived" toggle can reveal.
function hidesCleanlyTerminatedSession(session: WorkspaceSession, undelivered: number): boolean {
	if (!session.isTerminated) return false;
	if (session.workspaceCleanup === "preserved_dirty") return false;
	if (openPRs(session).length > 0) return false;
	if (undelivered > 0) return false;
	return true;
}

function translator(t: (key: MessageKey, values?: Record<string, unknown>) => string) {
	return (key: string, values?: Record<string, unknown>) => t(key as MessageKey, values);
}

export function OverviewPage() {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const showPageTitle = hidesShellTopbar();
	const workspacesQuery = useWorkspaceQuery({ includeCloud: false, refetchOnWindowFocus: true });
	const worktreesQuery = useWorktreesQuery();
	const cleanup = useCleanupWorktrees();
	const stuckBySession = useStuckDeliveriesBySession();
	const inventoryQuery = useProcessInventoryQuery();
	const [projectFilter, setProjectFilter] = useState("all");
	const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
	const [stageFilter, setStageFilter] = useState<KanbanColumn | "all">("all");
	const [showArchived, setShowArchived] = useState(false);
	const [collapsedProjects, setCollapsedProjects] = useState<ReadonlySet<string>>(new Set());
	const [selectedIds, setSelectedIds] = useState<ReadonlySet<string>>(new Set());
	const [cleanupTarget, setCleanupTarget] = useState<WorkspaceSummary | null>(null);
	const [terminateTargets, setTerminateTargets] = useState<WorkspaceSession[]>([]);
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);

	const workspaces = workspacesQuery.data ?? [];
	const worktreesBySession = useMemo(() => {
		const bySession = new Map<string, WorktreeUsage>();
		for (const ws of worktreesQuery.data ?? []) bySession.set(ws.sessionId, ws);
		return bySession;
	}, [worktreesQuery.data]);
	const orphanTrees = useMemo(
		() => (inventoryQuery.data?.trees ?? []).filter((tree) => tree.state === "orphan"),
		[inventoryQuery.data],
	);

	const groups = useMemo(() => {
		const visible = workspaces.filter((project) => projectFilter === "all" || project.id === projectFilter);
		return visible.map((project) => {
			const matches = project.sessions.filter((session) => {
				if (statusFilter !== "all" && overviewStatus(session) !== statusFilter) return false;
				if (stageFilter !== "all" && (session.kanbanColumn ?? "building") !== stageFilter) return false;
				if (!showArchived && hidesCleanlyTerminatedSession(session, stuckBySession.get(session.id) ?? 0)) return false;
				return true;
			});
			const orchestrators = matches.filter((session) => session.kind === "orchestrator");
			const workers = matches.filter((session) => session.kind !== "orchestrator");
			let bytes = 0;
			let reclaimable = 0;
			let undelivered = 0;
			for (const session of project.sessions) {
				undelivered += stuckBySession.get(session.id) ?? 0;
				const ws = worktreesBySession.get(session.id);
				if (!ws) continue;
				bytes += ws.bytes;
				if (ws.terminated) reclaimable += 1;
			}
			return { project, sessions: [...orchestrators, ...workers], bytes, reclaimable, undelivered };
		});
	}, [workspaces, projectFilter, statusFilter, stageFilter, showArchived, stuckBySession, worktreesBySession]);

	// Selection operates on live rows only: a session that got terminated or
	// filtered away silently drops out of the batch instead of killing a ghost.
	const selectedSessions = useMemo(
		() =>
			workspaces
				.flatMap((project) => project.sessions)
				.filter((session) => selectedIds.has(session.id) && !session.isTerminated),
		[workspaces, selectedIds],
	);
	const selectableIds = useMemo(
		() => groups.flatMap((group) => group.sessions.filter((session) => !session.isTerminated).map((session) => session.id)),
		[groups],
	);
	const allSelected = selectableIds.length > 0 && selectableIds.every((id) => selectedIds.has(id));
	const someSelected = selectableIds.some((id) => selectedIds.has(id));

	const toggleSelectAll = () => {
		setSelectedIds((current) => {
			const next = new Set(current);
			if (allSelected) {
				for (const id of selectableIds) next.delete(id);
			} else {
				for (const id of selectableIds) next.add(id);
			}
			return next;
		});
	};

	const toggleSelect = (id: string) => {
		setSelectedIds((current) => {
			const next = new Set(current);
			if (next.has(id)) next.delete(id);
			else next.add(id);
			return next;
		});
	};

	const cleanupTargetStats = useMemo(() => {
		if (!cleanupTarget) return { count: 0, size: 0 };
		let count = 0;
		let size = 0;
		for (const session of cleanupTarget.sessions) {
			const ws = worktreesBySession.get(session.id);
			if (ws?.terminated) {
				count += 1;
				size += ws.bytes;
			}
		}
		return { count, size };
	}, [cleanupTarget, worktreesBySession]);

	const runCleanup = () => {
		if (!cleanupTarget) return;
		const project = cleanupTarget;
		cleanup.mutate(project.id, {
			onSuccess: (result) => {
				setCleanupTarget(null);
				const freed = (result?.reclaimed ?? []).reduce((total, item) => total + item.bytes, 0);
				showGlobalToast(
					t("overview.cleanupDone"),
					freed > 0 ? t("overview.cleanupFreed", { size: formatBytes(freed), project: project.name }) : t("overview.cleanupNothing", { project: project.name }),
				);
			},
		});
	};

	// One kill endpoint call per target through the shared terminate mutation —
	// optimistic drop, restorable archive, and the rollback-on-error semantics
	// every other surface uses. The dialog stays open with the error when any
	// leg fails; the successful legs keep their optimistic rows.
	const terminate = useTerminateSession();
	const runTerminate = async () => {
		const targets = [...terminateTargets];
		try {
			for (const target of targets) await terminate.mutateAsync(target);
		} catch {
			return;
		}
		setSelectedIds(new Set());
		setTerminateTargets([]);
		showGlobalToast(t("overview.terminateDone", { count: targets.length }));
	};

	const openSession = (session: WorkspaceSession) => {
		void navigate(sessionNavigateTarget(session.workspaceId, session.id));
	};

	// The PR card lives in the session inspector's summary view: navigate to the
	// session with that view forced open, the same ui-store state a manual
	// inspector toggle writes.
	const openPrCard = (session: WorkspaceSession) => {
		const ui = useUiStore.getState();
		ui.setInspectorView(session.id, "summary");
		ui.setInspectorOpen(session.id, true);
		void navigate(sessionNavigateTarget(session.workspaceId, session.id));
	};

	const filterSelect = (value: string, onChange: (next: string) => void, label: string, items: { value: string; label: string }[]) => (
		<Select value={value} onValueChange={onChange}>
			<SelectTrigger className="h-8 w-36 text-xs" aria-label={label}>
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				{items.map((item) => (
					<SelectItem key={item.value} value={item.value} className="text-xs">
						{item.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);

	const stageLabel = (column: KanbanColumn) => getKanbanColumnView(column, translator(t)).label;

	const terminateBlockedCount = terminateTargets.filter((session) => overviewStatus(session) === "blocked").length;
	const terminateSingle = terminateTargets.length === 1;

	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			<header className="workspace-topbar-container center-panel-titlebar flex h-toolbar shrink-0 items-center justify-between gap-3 border-b border-border-strong pr-2">
				<h1 className={showPageTitle ? "text-base font-semibold" : "sr-only"}>{t("overview.title")}</h1>
				<div className="flex shrink-0 items-center gap-2">
					{selectedSessions.length > 0 && (
						<div className="flex items-center gap-1" data-testid="overview-bulk-bar">
							<span className="text-xs text-muted-foreground">{t("overview.selectedCount", { count: selectedSessions.length })}</span>
							<Button
								variant="ghost"
								size="sm"
								className="h-7 px-2 text-xs text-error hover:bg-error/10 hover:text-error"
								onClick={() => setTerminateTargets(selectedSessions)}
							>
								{t("overview.terminate")}
							</Button>
							<Button
								variant="ghost"
								size="sm"
								className="size-7 p-0 text-muted-foreground"
								aria-label={t("overview.clearSelection")}
								title={t("overview.clearSelection")}
								onClick={() => setSelectedIds(new Set())}
							>
								<X className="size-3.5" aria-hidden="true" />
							</Button>
						</div>
					)}
					<Checkbox
						checked={allSelected ? true : someSelected ? "indeterminate" : false}
						onCheckedChange={toggleSelectAll}
						aria-label={t("overview.selectAll")}
						className="ml-1"
					/>
					<Button
						variant="ghost"
						size="sm"
						className="h-7 px-2 text-xs"
						aria-pressed={showArchived}
						onClick={() => setShowArchived((current) => !current)}
					>
						{showArchived ? t("overview.hideArchived") : t("overview.showArchived")}
					</Button>
					{filterSelect(projectFilter, setProjectFilter, t("overview.filterProject"), [
						{ value: "all", label: t("overview.filterAll") },
						...workspaces.map((project) => ({ value: project.id, label: project.name })),
					])}
					{filterSelect(statusFilter, (next) => setStatusFilter(next as StatusFilter), t("overview.columnStatus"), [
						{ value: "all", label: t("overview.filterAll") },
						{ value: "working", label: t("overview.filterWorking") },
						{ value: "idle", label: t("overview.filterIdle") },
						{ value: "blocked", label: t("overview.filterBlocked") },
						{ value: "exited", label: t("overview.filterExited") },
					])}
					{filterSelect(stageFilter, (next) => setStageFilter(next as KanbanColumn | "all"), t("overview.columnStage"), [
						{ value: "all", label: t("overview.filterAll") },
						...STAGE_FILTERS.map((column) => ({ value: column, label: stageLabel(column) })),
					])}
				</div>
			</header>
			<OverviewResources />
			<ScrollArea.Root asChild type="hover" scrollHideDelay={500}>
				<main className="relative min-h-0 flex-1 overflow-hidden">
					<ScrollArea.Viewport className="h-full w-full">
						<div className="px-4 py-3">
							{workspacesQuery.isError ? (
								<p role="alert" className="p-4 text-sm text-error">{t("shell.couldNotLoadProjects")}</p>
							) : workspacesQuery.isLoading ? (
								<div className="flex flex-col gap-2 p-4">
									<Skeleton className="h-8 w-1/3" />
									<Skeleton className="h-9 w-full" />
									<Skeleton className="h-9 w-full" />
									<Skeleton className="h-9 w-2/3" />
								</div>
							) : workspaces.length === 0 ? (
								<p className="p-4 text-heading-sm text-muted-foreground">{t("overview.empty")}</p>
							) : (
								<div>
									{orphanTrees.length > 0 && (
										<div className="mb-2">
											<OverviewOrphans trees={orphanTrees} onKilled={refreshInventory} />
										</div>
									)}
									{groups.map(({ project, sessions, bytes, reclaimable, undelivered }) => (
										<ProjectGroup
											key={project.id}
											bytes={bytes}
											cleanupBusy={cleanup.isPending}
											collapsed={collapsedProjects.has(project.id)}
											onToggleCollapsed={() => toggleCollapsed(project.id)}
											onOpenOrchestrator={openSession}
											project={project}
											reclaimable={reclaimable}
											sessions={sessions}
											undelivered={undelivered}
											worktreesBySession={worktreesBySession}
											onCleanup={() => setCleanupTarget(project)}
											onOpenPrCard={openPrCard}
											onOpenSession={openSession}
											onSelect={toggleSelect}
											selectedIds={selectedIds}
											stuckBySession={stuckBySession}
											onTerminate={(session) => setTerminateTargets([session])}
										/>
									))}
								</div>
							)}
						</div>
					</ScrollArea.Viewport>
				</main>
			</ScrollArea.Root>
			<ConfirmDialog
				open={cleanupTarget !== null}
				destructive
				busy={cleanup.isPending}
				title={t("overview.cleanupTitle", { project: cleanupTarget?.name ?? "" })}
				description={t("overview.cleanupDescription", { count: cleanupTargetStats.count, size: formatBytes(cleanupTargetStats.size) })}
				confirmLabel={t("overview.cleanupConfirm")}
				error={cleanup.isError ? apiErrorMessage(cleanup.error) : null}
				onConfirm={runCleanup}
				onOpenChange={(open) => {
					if (!open) setCleanupTarget(null);
				}}
			/>
			<ConfirmDialog
				open={terminateTargets.length > 0}
				destructive
				busy={terminate.isPending}
				title={
					terminateSingle
						? t("overview.terminateOneTitle", { title: terminateTargets[0]?.title ?? "" })
						: t("overview.terminateManyTitle", { count: terminateTargets.length })
				}
				description={
					terminateSingle
						? t("overview.terminateOneDescription", {
								title: terminateTargets[0]?.title ?? "",
								project: terminateTargets[0]?.workspaceName ?? "",
							})
						: t("overview.terminateManyDescription")
				}
				confirmLabel={t("overview.terminate")}
				confirmAriaLabel={t("overview.terminateAria", { count: terminateTargets.length })}
				error={terminate.isError && !terminate.isPending ? apiErrorMessage(terminate.error) : null}
				onConfirm={() => void runTerminate()}
				onOpenChange={(open) => {
					if (!open) setTerminateTargets([]);
				}}
			>
				{terminateBlockedCount > 0 && (
					<p className="text-xs leading-5 text-warning">
						{terminateBlockedCount === 1
							? t("overview.terminateBlockedWarning")
							: t("overview.terminateBlockedWarningMany", { count: terminateBlockedCount })}
					</p>
				)}
				{!terminateSingle && (
					<ul className="mt-1 max-h-40 space-y-0.5 overflow-y-auto text-xs text-muted-foreground">
						{terminateTargets.map((session) => (
							<li key={session.id} className="truncate">
								{t("overview.terminateListRow", { title: session.title, project: session.workspaceName })}
								{overviewStatus(session) === "blocked" && (
									<span className="text-warning"> · {t("overview.terminateWaitingLabel")}</span>
								)}
							</li>
						))}
					</ul>
				)}
			</ConfirmDialog>
		</div>
	);

	function refreshInventory() {
		void inventoryQuery.refetch();
	}

	function toggleCollapsed(projectId: string) {
		setCollapsedProjects((current) => {
			const next = new Set(current);
			if (next.has(projectId)) next.delete(projectId);
			else next.add(projectId);
			return next;
		});
	}
}

// One project band: a sidebar project row (folder disclosure, orchestrator
// status dot, name, per-project counters, cleanup action) with its sessions
// as nested sidebar-style rows.
function ProjectGroup({
	project,
	sessions,
	bytes,
	reclaimable,
	undelivered,
	collapsed,
	onToggleCollapsed,
	onOpenOrchestrator,
	cleanupBusy,
	worktreesBySession,
	selectedIds,
	stuckBySession,
	onCleanup,
	onOpenSession,
	onOpenPrCard,
	onSelect,
	onTerminate,
}: {
	project: WorkspaceSummary;
	sessions: WorkspaceSession[];
	bytes: number;
	reclaimable: number;
	undelivered: number;
	collapsed: boolean;
	onToggleCollapsed: () => void;
	onOpenOrchestrator: (session: WorkspaceSession) => void;
	cleanupBusy: boolean;
	worktreesBySession: Map<string, WorktreeUsage>;
	selectedIds: ReadonlySet<string>;
	stuckBySession: Map<string, number>;
	onCleanup: () => void;
	onOpenSession: (session: WorkspaceSession) => void;
	onOpenPrCard: (session: WorkspaceSession) => void;
	onSelect: (id: string) => void;
	onTerminate: (session: WorkspaceSession) => void;
}) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const orchestrator = sessions.find((session) => session.kind === "orchestrator");
	const orchestratorDot = orchestrator ? getOrchestratorStatusDotView(orchestrator) : undefined;
	return (
		<section data-project-id={project.id} className="mb-1">
			<div
				className={cn(
					"group/nav-row relative flex h-8 w-full items-center gap-2 rounded-lg bg-surface px-2.5 text-sm font-medium",
				)}
				data-project-row=""
			>
				<NavRowHighlight />
				<button
					type="button"
					tabIndex={-1}
					aria-label={t("overview.toggleProject", { name: project.name })}
					aria-expanded={!collapsed}
					className="relative z-[1] -ml-1 grid size-6 shrink-0 place-items-center rounded-md text-passive hover:text-foreground focus-visible:text-foreground"
					onClick={onToggleCollapsed}
				>
					<ChevronRight aria-hidden="true" className={cn("size-icon-md transition-transform", !collapsed && "rotate-90")} />
				</button>
				<span aria-hidden="true" className="relative z-[1] inline-flex size-icon-md shrink-0 items-center justify-center text-muted-foreground">
					{collapsed ? <Folder strokeWidth={1.75} /> : <FolderOpen strokeWidth={1.75} />}
				</span>
				{orchestratorDot && (
					<span
						aria-hidden="true"
						className={cn("relative z-[1] size-2 shrink-0 rounded-full", orchestratorDot.className, orchestratorDot.breathe && "animate-status-pulse")}
						title={orchestratorDot.title}
					/>
				)}
				<button
					type="button"
					className="relative z-[1] min-w-0 flex-1 truncate text-left hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
					onClick={() => void navigate(projectNavigateTarget(project.id))}
					data-project-name=""
				>
					{project.name}
				</button>
				{undelivered > 0 && (
					<BadgeCount count={undelivered} label={t("overview.undeliveredCount", { count: undelivered })} />
				)}
				{bytes > 0 && (
					<span className="relative z-[1] shrink-0 font-mono text-xs tabular-nums text-muted-foreground">
						{formatBytes(bytes)}
						{reclaimable > 0 ? ` · ${t("overview.reclaimable", { count: reclaimable })}` : ""}
					</span>
				)}
				<Button
					variant="ghost"
					size="sm"
					className="relative z-[1] h-7 shrink-0 gap-1 px-2 text-xs"
					disabled={reclaimable === 0 || cleanupBusy}
					title={reclaimable === 0 ? t("overview.nothingToClean") : undefined}
					onClick={onCleanup}
				>
					<Trash2 className="size-3.5" aria-hidden="true" />
					{t("overview.cleanup")}
				</Button>
				{orchestrator && (
					<span className="relative z-[1] ml-1 flex shrink-0 items-center">
						<button
							type="button"
							aria-label={t("overview.openOrchestrator", { name: project.name })}
							title={t("overview.openOrchestrator", { name: project.name })}
							className={ROW_ACTION_BUTTON_CLASS}
							onClick={() => onOpenOrchestrator(orchestrator)}
						>
							<OrchestratorIcon aria-hidden="true" />
						</button>
					</span>
				)}
			</div>
			{!collapsed && (
				<div className="pl-0.5">
					{sessions.length === 0 ? (
						<div className="flex h-8 w-full items-center rounded-lg px-2.5 pl-7 text-xs text-muted-foreground">
							{t("overview.projectEmpty")}
						</div>
					) : (
						sessions.map((session) => (
							<SessionRow
								key={session.id}
								session={session}
								selected={selectedIds.has(session.id)}
								undelivered={stuckBySession.get(session.id) ?? 0}
								worktree={worktreesBySession.get(session.id)}
								onOpen={onOpenSession}
								onOpenPrCard={onOpenPrCard}
								onSelect={onSelect}
								onTerminate={onTerminate}
							/>
						))
					)}
				</div>
			)}
		</section>
	);
}

// One session as a sidebar-style row: checkbox, status dot, title, then the
// overview's facts as quiet right-aligned suffixes (undelivered count, stage,
// PR link, merge gate, worktree size, last touch), then the row actions.
function SessionRow({
	session,
	selected,
	undelivered,
	worktree,
	onOpen,
	onOpenPrCard,
	onSelect,
	onTerminate,
}: {
	session: WorkspaceSession;
	selected: boolean;
	undelivered: number;
	worktree: WorktreeUsage | undefined;
	onOpen: (session: WorkspaceSession) => void;
	onOpenPrCard: (session: WorkspaceSession) => void;
	onSelect: (id: string) => void;
	onTerminate: (session: WorkspaceSession) => void;
}) {
	const { t } = useTranslation();
	const activityView = getAgentActivityView(session.isTerminated ? EXITED_ACTIVITY : session.activity);
	const dot = getSessionStatusDotView(session);
	const stage = getKanbanColumnView(session.kanbanColumn ?? "building", translator(t));
	const gate = overviewMergeGate(session, t);
	const pr = sortedPRs(session)[0];
	const mergedStage = pr?.state === "merged" || session.status === "merged";
	const lastTouchAt = session.lastInteractionAt ?? session.lastUserMessageAt;
	return (
		<div
			className="group/nav-row relative flex h-8 w-full items-center gap-1.5 rounded-lg py-0 pl-1.5 pr-1 text-sm"
			data-session-row=""
			data-session-id={session.id}
		>
			<NavRowHighlight />
			{!session.isTerminated && (
				<Checkbox
					checked={selected}
					onCheckedChange={() => onSelect(session.id)}
					aria-label={t("overview.selectSession", { title: session.title })}
					className="relative z-[1] mr-0.5 shrink-0"
				/>
			)}
			<span className="relative z-[1] inline-flex shrink-0 items-center justify-center px-1">
				<span
					aria-hidden="true"
					className={cn("size-2 rounded-full", dot.className, dot.breathe && "animate-status-pulse")}
					title={dot.title}
					data-session-status={session.status}
				/>
			</span>
			<span className="sr-only">{activityView.label}</span>
			<button
				type="button"
				className={cn(
					"relative z-[1] h-8 min-w-0 flex-1 truncate text-left text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
					session.isTerminated ? "text-muted-foreground" : "text-muted-foreground group-hover/nav-row:text-foreground",
				)}
				onClick={() => onOpen(session)}
			>
				<span className="truncate">{session.title}</span>
				{session.kind === "orchestrator" && <OrchestratorIcon className="ml-1.5 inline size-3.5 align-[-2px] text-muted-foreground" aria-hidden="true" />}
			</button>
			{undelivered > 0 && (
				<span className="relative z-[1] shrink-0">
					<BadgeCount count={undelivered} label={t("overview.undeliveredCount", { count: undelivered })} />
				</span>
			)}
			<span className="relative z-[1] w-24 shrink-0 truncate text-xs text-muted-foreground" title={session.displayStatus}>
				{session.provisionState === "provisioning" ? t("overview.stageSpawning") : mergedStage ? t("displayStatus.merged") : stage.label}
			</span>
			<span className="relative z-[1] w-12 shrink-0 text-right">
				{pr ? (
					<ProductExternalLink
						href={pr.url}
						stopPropagation
						className="rounded-xs font-mono text-xs text-foreground underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
					>
						#{pr.number}
					</ProductExternalLink>
				) : (
					<span className="text-xs text-passive">—</span>
				)}
			</span>
			<span className="relative z-[1] w-36 shrink-0 truncate text-xs" title={gate?.label}>
				{gate ? (
					<span className="inline-flex max-w-full items-center gap-1.5">
						<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", gate.open ? "bg-status-ready" : "bg-passive")} />
						<span className={cn("truncate", gate.open ? "text-status-ready" : "text-muted-foreground")}>{gate.label}</span>
					</span>
				) : (
					<span className="text-passive">—</span>
				)}
			</span>
			<span className="relative z-[1] w-16 shrink-0 text-right">
				{worktree ? (
					<span className="font-mono text-xs tabular-nums text-muted-foreground" title={worktree.path}>
						{formatBytes(worktree.bytes)}
					</span>
				) : (
					<span className="text-xs text-passive">—</span>
				)}
			</span>
			<span className="relative z-[1] w-10 shrink-0 text-right font-sans text-micro tabular-nums text-passive">
				{lastTouchAt ? <time dateTime={lastTouchAt} title={t("shell.lastMessageAt", { time: formatTimeCompact(lastTouchAt) })}>{formatTimeTerse(lastTouchAt)}</time> : null}
			</span>
			<span className="relative z-[1] ml-1 flex shrink-0 items-center gap-0.5">
				{pr && (
					<button
						type="button"
						aria-label={t("overview.openPR")}
						title={t("overview.openPR")}
						className={ROW_ACTION_BUTTON_CLASS}
						onClick={() => onOpenPrCard(session)}
					>
						<GitPullRequest aria-hidden="true" />
					</button>
				)}
				{!session.isTerminated && (
					<button
						type="button"
						aria-label={t("overview.terminateNamed", { title: session.title })}
						title={t("overview.terminate")}
						className={cn(ROW_ACTION_BUTTON_CLASS, "hover:!text-error")}
						onClick={() => onTerminate(session)}
					>
						<Archive aria-hidden="true" />
					</button>
				)}
			</span>
		</div>
	);
}

// A small warning-tone counter chip for undelivered messages (project bands
// and session rows share it).
function BadgeCount({ count, label }: { count: number; label: string }) {
	return (
		<Badge variant="warning" className="h-4 shrink-0 px-1.5 font-mono text-2xs tabular-nums" title={label}>
			{count}
		</Badge>
	);
}
