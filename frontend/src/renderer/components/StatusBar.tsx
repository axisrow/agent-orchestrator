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
import { MemoryPopover } from "./MemoryPopover";

// App-wide bottom strip, kept minimal: host RAM/Swap (the MemoryPopover
// segment) plus the orphan count with the batch kill entry point. Renders
// nothing while the inventory is unavailable, and hides entirely when the
// daemon provides neither a host snapshot nor orphans.
export function StatusBar() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [confirmOpen, setConfirmOpen] = useState(false);
	const [killError, setKillError] = useState<string | null>(null);
	const { data } = useProcessInventoryQuery();

	const orphans = (data?.trees ?? []).filter((tree) => tree.state === "orphan");

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
		// Failure keeps the dialog/popover open with the error inline — the
		// trees are still there to retry.
		onError: () => setKillError(t("statusBar.killFailed")),
	});

	if (!data || (!data.host && orphans.length === 0)) return null;

	const pendingSessionId =
		killMutation.isPending && killMutation.variables && killMutation.variables.length === 1
			? killMutation.variables[0].sessionId
			: undefined;

	return (
		<>
			<div
				data-testid="status-bar"
				className="flex h-7 shrink-0 items-center gap-3 border-t border-border/60 bg-sidebar px-3 text-caption text-muted-foreground"
			>
				{data.host && (
					<MemoryPopover
						host={data.host}
						trees={data.trees}
						totals={data.totals}
						pendingSessionId={pendingSessionId}
						killError={killError}
						onKillTree={(target) => {
							setKillError(null);
							killMutation.mutate([target]);
						}}
						onBatchKill={() => {
							setKillError(null);
							setConfirmOpen(true);
						}}
					/>
				)}
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
			</div>
			<ConfirmDialog
				open={confirmOpen}
				title={t("statusBar.killTitle")}
				description={
					<>
						<p>{t("statusBar.killLead", { count: orphans.length, rss: formatBytes(data.totals.orphansRssBytes) })}</p>
						<ul className="mt-2 space-y-0.5">
							{orphans.slice(0, 8).map((tree) => (
								<li key={tree.sessionId} className="tabular-nums">
									{tree.sessionId} · pid {tree.rootPid} · {formatBytes(tree.rssBytes)}
								</li>
							))}
							{orphans.length > 8 && <li>{t("statusBar.moreTrees", { count: orphans.length - 8 })}</li>}
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
