import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { AlertTriangle } from "lucide-react";
import { apiClient } from "../lib/api-client";
import { formatBytes } from "../lib/format-bytes";
import {
	processInventoryQueryKey,
	useProcessInventoryQuery,
	isStoppableOwnedTree,
	type ProcessKillTarget,
} from "../hooks/useProcessInventoryQuery";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { ConfirmDialog } from "./ConfirmDialog";
import { MemoryPopover } from "./MemoryPopover";

// The resource facts the bottom status bar used to carry (issue #330): host
// RAM/Swap with the AO footprint (MemoryPopover), the agent count, and the
// batch Stop entry. Orphaned trees render as the warning-marked section in the
// overview list body rather than a badge; idle workers are ordinary list rows.
// Hides entirely when the daemon reports the surface disabled
// (AO_NO_PROCINVENTORY) or provides neither a host snapshot nor stoppable
// trees — same rules the status bar had.
export function OverviewResources() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [confirmOpen, setConfirmOpen] = useState(false);
	const [busy, setBusy] = useState(false);
	const [killError, setKillError] = useState<string | null>(null);
	const [stopError, setStopError] = useState<string | null>(null);
	const [pendingKillSessionId, setPendingKillSessionId] = useState<string | undefined>();
	const [pendingStopSessionId, setPendingStopSessionId] = useState<string | undefined>();
	const { data } = useProcessInventoryQuery();

	// AO_NO_PROCINVENTORY (the daemon reports disabled): the feature is off.
	if (data?.disabled) return null;

	const orphans = (data?.trees ?? []).filter((tree) => tree.state === "orphan");
	const idleOwned = (data?.trees ?? []).filter(isStoppableOwnedTree);
	const stoppableBytes =
		orphans.reduce((sum, tree) => sum + tree.rssBytes, 0) + idleOwned.reduce((sum, tree) => sum + tree.rssBytes, 0);

	const refresh = () => {
		void queryClient.invalidateQueries({ queryKey: processInventoryQueryKey });
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
	};

	const killOrphans = async (targets: ProcessKillTarget[]) => {
		const { error } = await apiClient.POST("/api/v1/system/processes/kill", { body: { targets } });
		if (error) throw new Error("kill failed");
	};

	const stopSessions = async (sessionIds: string[]) => {
		for (const sessionId of sessionIds) {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/kill", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(`stop ${sessionId} failed`);
		}
	};

	// The screen-level Stop covers everything stoppable in one confirm: true
	// orphans go through the process-group kill, idle owned sessions through
	// the daemon's own session kill (terminate + resumable later).
	const runStopAll = async () => {
		setBusy(true);
		try {
			if (orphans.length > 0) {
				await killOrphans(orphans.map(toTarget));
			}
			await stopSessions(idleOwned.map((tree) => tree.sessionId));
			refresh();
			setKillError(null);
			setStopError(null);
			setConfirmOpen(false);
		} catch {
			setStopError(t("statusBar.stopFailed"));
		} finally {
			setBusy(false);
		}
	};

	if (!data || (!data.host && orphans.length === 0 && idleOwned.length === 0)) return null;

	return (
		<>
			<div
				data-testid="overview-resources"
				className="flex h-7 shrink-0 items-center gap-3 border-b border-border/60 px-4 text-caption text-muted-foreground"
			>
				{data.host && (
					<MemoryPopover
						host={data.host}
						trees={data.trees}
						totals={data.totals}
						pendingKillSessionId={pendingKillSessionId}
						pendingStopSessionId={pendingStopSessionId}
						killError={killError}
						stopError={stopError}
						onKillTrees={(targets) => {
							setKillError(null);
							setPendingKillSessionId(targets[0]?.sessionId);
							void killOrphans(targets)
								.then(() => refresh())
								.catch(() => setKillError(t("statusBar.killFailed")))
								.finally(() => setPendingKillSessionId(undefined));
						}}
						onStopSessions={(sessionIds) => {
							setStopError(null);
							setPendingStopSessionId(sessionIds[0]);
							void stopSessions(sessionIds)
								.then(() => refresh())
								.catch(() => setStopError(t("statusBar.stopFailed")))
								.finally(() => setPendingStopSessionId(undefined));
						}}
						onBatchKill={() => {
							setKillError(null);
							setConfirmOpen(true);
						}}
					/>
				)}
				<span>
					{t("statusBar.agentsCount")} <span className="tabular-nums">{data.trees.length}</span>
				</span>
				{orphans.length + idleOwned.length > 0 && (
					<button
						type="button"
						data-testid="overview-stop-all"
						className="text-caption text-destructive hover:underline focus-visible:underline disabled:pointer-events-none disabled:opacity-50"
						onClick={() => {
							setKillError(null);
							setStopError(null);
							setConfirmOpen(true);
						}}
					>
						{t("statusBar.stopAll")}
					</button>
				)}
			</div>
			<ConfirmDialog
				open={confirmOpen}
				title={t("statusBar.confirmStopTitle")}
				description={
					<>
						<p>
							{t("statusBar.confirmStopLead", {
								count: orphans.length + idleOwned.length,
								rss: formatBytes(stoppableBytes),
							})}
						</p>
						<ul className="mt-2 space-y-0.5">
							{orphans.map((tree) => (
								<li key={tree.sessionId} className="tabular-nums">
									{t("statusBar.treeRow", { session: tree.sessionId, pid: tree.rootPid, rss: formatBytes(tree.rssBytes) })}
								</li>
							))}
							{idleOwned.slice(0, Math.max(0, 8 - orphans.length)).map((tree) => (
								<li key={tree.sessionId} className="tabular-nums">
									{t("statusBar.treeRow", { session: tree.sessionId, pid: tree.rootPid, rss: formatBytes(tree.rssBytes) })}
								</li>
							))}
							{orphans.length + idleOwned.length > 8 && (
								<li>{t("statusBar.moreTrees", { count: orphans.length + idleOwned.length - 8 })}</li>
							)}
						</ul>
					</>
				}
				confirmLabel={t("statusBar.confirmStop")}
				destructive
				busy={busy}
				error={killError ?? stopError}
				onConfirm={() => {
					void runStopAll();
				}}
				onOpenChange={(open) => {
					setConfirmOpen(open);
					if (!open) {
						setKillError(null);
						setStopError(null);
					}
				}}
			/>
		</>
	);
}

function toTarget(tree: { sessionId: string; rootPid: number; rootLstart: string }): ProcessKillTarget {
	return { sessionId: tree.sessionId, rootPid: tree.rootPid, rootLstart: tree.rootLstart };
}

// The orphan section of the overview list body: one warning-marked row per
// orphaned process tree with its own kill action (process-group kill, the
// MemoryPopover's exact target shape). Session rows above carry the sessions;
// these are the left-behind trees the list must not lose (issue #330).
export function OverviewOrphans({ trees, onKilled }: {
	trees: { sessionId: string; rootPid: number; rootLstart: string; rssBytes: number }[];
	onKilled?: () => void;
}) {
	const { t } = useTranslation();
	const [pendingSessionId, setPendingSessionId] = useState<string | undefined>();
	const [error, setError] = useState<string | null>(null);
	const bytes = trees.reduce((sum, tree) => sum + tree.rssBytes, 0);

	const kill = (tree: { sessionId: string; rootPid: number; rootLstart: string }) => {
		setError(null);
		setPendingSessionId(tree.sessionId);
		void apiClient
			.POST("/api/v1/system/processes/kill", { body: { targets: [toTarget(tree)] } })
			.then(({ error }) => {
				if (error) throw new Error("kill failed");
			})
			.catch(() => setError(t("statusBar.killFailed")))
			.finally(() => {
				setPendingSessionId(undefined);
				onKilled?.();
			});
	};

	return (
		<section data-testid="overview-orphans" aria-label={t("overview.orphansSection", { count: trees.length })}>
			<div className="flex h-8 items-center gap-2 rounded-md px-2.5 text-sm font-medium text-warning">
				<AlertTriangle aria-hidden="true" className="size-icon-md shrink-0" />
				<span className="min-w-0 flex-1 truncate">
					{t("overview.orphansSection", { count: trees.length })}
					<span className="ml-2 font-normal tabular-nums text-muted-foreground">{formatBytes(bytes)}</span>
				</span>
				{error ? (
					<span role="alert" className="text-caption text-error">{error}</span>
				) : null}
			</div>
			<ul className="space-y-px pl-0.5">
				{trees.map((tree) => (
					<li
						key={`${tree.sessionId}-${tree.rootPid}`}
						className="group/nav-row relative flex h-8 w-full items-center gap-2 rounded-lg px-2.5 pl-6 text-sm text-muted-foreground"
					>
						<AlertTriangle aria-hidden="true" className="size-3.5 shrink-0 text-warning" />
						<span className="min-w-0 flex-1 truncate font-mono text-xs tabular-nums" title={tree.sessionId}>
							{t("statusBar.treeRow", { session: tree.sessionId, pid: tree.rootPid, rss: formatBytes(tree.rssBytes) })}
						</span>
						<button
							type="button"
							aria-label={t("statusBar.killTreeAria", { session: tree.sessionId })}
							disabled={pendingSessionId === tree.sessionId}
							className="shrink-0 text-caption text-destructive hover:underline focus-visible:underline disabled:pointer-events-none disabled:opacity-50"
							onClick={() => kill(tree)}
						>
							{pendingSessionId === tree.sessionId ? "…" : t("statusBar.killTree")}
						</button>
					</li>
				))}
			</ul>
		</section>
	);
}
