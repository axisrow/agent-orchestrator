import { useMemo, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { ScrollArea } from "radix-ui";
import { useTranslation } from "react-i18next";
import { Trash2 } from "lucide-react";
import { getKanbanColumnView } from "@aoagents/product-ui";
import { ConfirmDialog } from "./ConfirmDialog";
import { OrchestratorIcon } from "./icons";
import { ProductExternalLink } from "./ProductExternalLink";
import { Button } from "./ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Skeleton } from "./ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";
import { useCleanupWorktrees, useWorktreesQuery, type WorktreeUsage } from "../hooks/useWorktrees";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import type { KanbanColumn, WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { sortedPRs } from "../types/workspace";
import { apiErrorMessage } from "../lib/api-client";
import { formatBytes } from "../lib/format-bytes";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { prCanMerge, sessionPRDisplaySummaries } from "../lib/pr-display";
import { getAgentActivityView } from "../lib/session-presentation";
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
// unresolved comments.
function overviewMergeGate(session: WorkspaceSession, t: (key: MessageKey, values?: Record<string, unknown>) => string): { open: boolean; label: string } | undefined {
	const [pr] = sessionPRDisplaySummaries(session);
	if (!pr) return undefined;
	if (pr.state === "merged") return { open: true, label: t("displayStatus.merged") };
	if (pr.state === "draft") return { open: false, label: t("overview.gateDraft") };
	if (prCanMerge(pr)) return { open: true, label: t("overview.gateOpen") };
	if (pr.review.decision === "changes_requested") return { open: false, label: t("overview.gateChanges") };
	if (pr.review.decision === "review_required") return { open: false, label: t("overview.gateReview") };
	if (pr.review.hasUnresolvedHumanComments) return { open: false, label: t("overview.gateComments") };
	if (pr.ci.state === "failing") return { open: false, label: t("overview.gateCi") };
	if (pr.mergeability.state === "conflicting") return { open: false, label: t("overview.gateConflicts") };
	if (pr.ci.state === "pending") return { open: false, label: t("overview.gateChecks") };
	return { open: false, label: t("overview.gateProvider") };
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
	const [projectFilter, setProjectFilter] = useState("all");
	const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
	const [stageFilter, setStageFilter] = useState<KanbanColumn | "all">("all");
	const [cleanupTarget, setCleanupTarget] = useState<WorkspaceSummary | null>(null);
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);

	const workspaces = workspacesQuery.data ?? [];
	const worktreesBySession = useMemo(() => {
		const bySession = new Map<string, WorktreeUsage>();
		for (const ws of worktreesQuery.data ?? []) bySession.set(ws.sessionId, ws);
		return bySession;
	}, [worktreesQuery.data]);

	const groups = useMemo(() => {
		const visible = workspaces.filter((project) => projectFilter === "all" || project.id === projectFilter);
		return visible.map((project) => {
			const matches = project.sessions.filter(
				(session) => (statusFilter === "all" || overviewStatus(session) === statusFilter) &&
					(stageFilter === "all" || (session.kanbanColumn ?? "building") === stageFilter),
			);
			const orchestrators = matches.filter((session) => session.kind === "orchestrator");
			const workers = matches.filter((session) => session.kind !== "orchestrator");
			let bytes = 0;
			let reclaimable = 0;
			for (const session of project.sessions) {
				const ws = worktreesBySession.get(session.id);
				if (!ws) continue;
				bytes += ws.bytes;
				if (ws.terminated) reclaimable += 1;
			}
			return { project, sessions: [...orchestrators, ...workers], bytes, reclaimable };
		});
	}, [workspaces, projectFilter, statusFilter, stageFilter, worktreesBySession]);

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

	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			<header className="workspace-topbar-container center-panel-titlebar flex h-toolbar shrink-0 items-center justify-between gap-3 border-b border-border-strong pr-2">
				<h1 className={showPageTitle ? "text-base font-semibold" : "sr-only"}>{t("overview.title")}</h1>
				<div className="flex shrink-0 items-center gap-2">
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
								<Table>
									<TableHeader>
										<TableRow className="hover:bg-transparent">
											<TableHead className="text-caption text-muted-foreground">{t("overview.columnSession")}</TableHead>
											<TableHead className="text-caption text-muted-foreground">{t("overview.columnStatus")}</TableHead>
											<TableHead className="text-caption text-muted-foreground">{t("overview.columnStage")}</TableHead>
											<TableHead className="text-caption text-muted-foreground">{t("overview.columnPR")}</TableHead>
											<TableHead className="text-caption text-muted-foreground">{t("overview.columnGate")}</TableHead>
											<TableHead className="text-right text-caption text-muted-foreground">{t("overview.columnWorktree")}</TableHead>
										</TableRow>
									</TableHeader>
									<TableBody>
										{groups.map(({ project, sessions, bytes, reclaimable }) => (
											sessionRows(project, sessions, bytes, reclaimable)
										))}
									</TableBody>
								</Table>
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
		</div>
	);

	// Rows for one project group: a band carrying the project's name and its
	// worktree footprint with the cleanup action, then one row per session.
	function sessionRows(project: WorkspaceSummary, sessions: WorkspaceSession[], bytes: number, reclaimable: number) {
		return [
			<TableRow key={`${project.id}-group`} className="bg-surface hover:bg-surface">
				<TableCell colSpan={6} className="px-2 py-1.5">
					<div className="flex items-center justify-between gap-2">
						<span className="min-w-0 truncate text-sm font-medium">{project.name}</span>
						<div className="flex shrink-0 items-center gap-2">
							{bytes > 0 ? (
								<span className="font-mono text-xs tabular-nums text-muted-foreground">
									{formatBytes(bytes)}
									{reclaimable > 0 ? ` · ${t("overview.reclaimable", { count: reclaimable })}` : ""}
								</span>
							) : null}
							<Button
								variant="ghost"
								size="sm"
								className="h-7 gap-1 px-2 text-xs"
								disabled={reclaimable === 0 || cleanup.isPending}
								title={reclaimable === 0 ? t("overview.nothingToClean") : undefined}
								onClick={() => setCleanupTarget(project)}
							>
								<Trash2 className="size-3.5" aria-hidden="true" />
								{t("overview.cleanup")}
							</Button>
						</div>
					</div>
				</TableCell>
			</TableRow>,
			sessions.length === 0 ? (
				<TableRow key={`${project.id}-empty`}>
					<TableCell colSpan={6} className="px-2 py-2 text-xs text-muted-foreground">{t("overview.projectEmpty")}</TableCell>
				</TableRow>
			) : sessions.map((session) => {
				const activityView = getAgentActivityView(session.isTerminated ? EXITED_ACTIVITY : session.activity);
				const stage = getKanbanColumnView(session.kanbanColumn ?? "building", translator(t));
				const gate = overviewMergeGate(session, t);
				const pr = sortedPRs(session)[0];
				const worktree = worktreesBySession.get(session.id);
				return (
					<TableRow key={session.id}>
						<TableCell className="max-w-72 px-2 py-2">
							<button
								type="button"
								className="flex min-w-0 items-center gap-1.5 rounded-xs text-left text-sm text-foreground transition-colors hover:text-markdown-link focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
								onClick={() => void navigate(sessionNavigateTarget(project.id, session.id))}
							>
								{session.kind === "orchestrator" ? (
									<OrchestratorIcon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
								) : null}
								<span className={cn("truncate", session.isTerminated && "text-muted-foreground")}>{session.title}</span>
								{session.kind === "orchestrator" ? <span className="sr-only">{t("overview.orchestrator")}</span> : null}
							</button>
						</TableCell>
						<TableCell className="px-2 py-2">
							<span className="inline-flex items-center gap-2">
								<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", activityView.indicatorClassName)} />
								<span className="text-sm text-muted-foreground">{activityView.label}</span>
							</span>
						</TableCell>
						<TableCell className="px-2 py-2">
							<span className="inline-flex items-center gap-2" title={session.displayStatus}>
								<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", stage.dotClassName)} />
								<span className="text-sm">
									{session.provisionState === "provisioning" ? t("overview.stageSpawning") : stage.label}
								</span>
							</span>
						</TableCell>
						<TableCell className="px-2 py-2">
							{pr ? (
								<ProductExternalLink
									href={pr.url}
									stopPropagation
									className="rounded-xs font-mono text-xs text-foreground underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
								>
									#{pr.number}
								</ProductExternalLink>
							) : (
								<span className="text-sm text-passive">—</span>
							)}
						</TableCell>
						<TableCell className="px-2 py-2">
							{gate ? (
								<span className="inline-flex items-center gap-2">
									<span
										aria-hidden="true"
										className={cn("size-1.5 shrink-0 rounded-full", gate.open ? "bg-status-ready" : "bg-passive")}
									/>
									<span className={cn("text-sm", gate.open ? "text-status-ready" : "text-muted-foreground")}>{gate.label}</span>
								</span>
							) : (
								<span className="text-sm text-passive">—</span>
							)}
						</TableCell>
						<TableCell className="px-2 py-2 text-right">
							{worktree ? (
								<span className="font-mono text-xs tabular-nums text-muted-foreground" title={worktree.path}>
									{formatBytes(worktree.bytes)}
								</span>
							) : (
								<span className="text-sm text-passive">—</span>
							)}
						</TableCell>
					</TableRow>
				);
			}),
		];
	}
}
