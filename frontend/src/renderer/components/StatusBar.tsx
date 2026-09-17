import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle } from "lucide-react";
import { apiClient } from "../lib/api-client";
import { formatBytes } from "../lib/format-bytes";
import {
	processInventoryQueryKey,
	useProcessInventoryQuery,
	type ProcessKillTarget,
} from "../hooks/useProcessInventoryQuery";
import { Badge } from "./ui/badge";
import { ConfirmDialog } from "./ConfirmDialog";

// A confirm dialog listing every tree gets unreadable; past this, the list
// truncates with an "and N more" line.
const maxTreesInDialog = 8;

// App-wide bottom strip: one glanceable line of AO's process footprint —
// daemon, tmux server, live sessions, orphaned trees — with the destructive
// kill flow for orphans. Renders nothing while the inventory is unavailable
// (headless daemon, older daemon, Windows) so it can never block the shell.
export function StatusBar() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [confirmOpen, setConfirmOpen] = useState(false);
	const [killError, setKillError] = useState<string | null>(null);
	const { data } = useProcessInventoryQuery();

	const orphans = (data?.trees ?? []).filter((tree) => tree.state === "orphan");
	const totalBytes = data
		? data.totals.daemonRssBytes + data.totals.tmuxRssBytes + data.totals.sessionsRssBytes + data.totals.orphansRssBytes
		: 0;

	const killMutation = useMutation({
		mutationFn: async (targets: ProcessKillTarget[]) => {
			const { error } = await apiClient.POST("/api/v1/system/processes/kill", { body: { targets } });
			if (error) throw new Error("kill failed");
		},
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: processInventoryQueryKey });
			setKillError(null);
			setConfirmOpen(false);
		},
		onError: () => setKillError(t("statusBar.killFailed")),
	});

	if (!data) return null;

	return (
		<>
			<div
				data-testid="status-bar"
				className="flex h-7 shrink-0 items-center gap-3 border-t border-border/60 bg-sidebar px-3 text-caption text-muted-foreground"
			>
				<span className="font-medium text-foreground">AO</span>
				<span className="tabular-nums">{formatBytes(data.totals.daemonRssBytes)}</span>
				{data.tmux.present && (
					<>
						<span aria-hidden="true">·</span>
						<span>
							tmux <span className="tabular-nums">{formatBytes(data.totals.tmuxRssBytes)}</span>
						</span>
					</>
				)}
				<span aria-hidden="true">·</span>
				<span>
					{t("statusBar.sessions")} <span className="tabular-nums">{data.totals.sessionsCount}</span>
				</span>
				{orphans.length > 0 && (
					<>
						<Badge variant="warning">
							{t("statusBar.orphans")}
							<span className="tabular-nums">{orphans.length}</span>
							<AlertTriangle aria-hidden="true" />
						</Badge>
						<button
							type="button"
							data-testid="status-bar-kill"
							className="text-caption text-destructive hover:underline focus-visible:underline disabled:pointer-events-none disabled:opacity-50"
							onClick={() => {
								setKillError(null);
								setConfirmOpen(true);
							}}
						>
							{t("statusBar.kill")}
						</button>
					</>
				)}
				<span className="ml-auto tabular-nums" title={t("statusBar.footprintTitle")}>
					Σ {formatBytes(totalBytes)}
				</span>
			</div>
			<ConfirmDialog
				open={confirmOpen}
				title={t("statusBar.killTitle")}
				description={
					<>
						<p>{t("statusBar.killLead", { count: orphans.length, rss: formatBytes(data.totals.orphansRssBytes) })}</p>
						<ul className="mt-2 space-y-0.5">
							{orphans.slice(0, maxTreesInDialog).map((tree) => (
								<li key={tree.sessionId} className="tabular-nums">
									{tree.sessionId} · pid {tree.rootPid} · {formatBytes(tree.rssBytes)}
								</li>
							))}
							{orphans.length > maxTreesInDialog && (
								<li>{t("statusBar.moreTrees", { count: orphans.length - maxTreesInDialog })}</li>
							)}
						</ul>
					</>
				}
				confirmLabel={t("statusBar.killConfirm")}
				destructive
				busy={killMutation.isPending}
				error={killError}
				onConfirm={() =>
					killMutation.mutate(
						orphans.map((tree) => ({ sessionId: tree.sessionId, rootPid: tree.rootPid, rootLstart: tree.rootLstart })),
					)
				}
				onOpenChange={(open) => {
					setConfirmOpen(open);
					if (!open) setKillError(null);
				}}
			/>
		</>
	);
}
