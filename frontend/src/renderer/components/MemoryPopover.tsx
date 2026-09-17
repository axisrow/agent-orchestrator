import { useState } from "react";
import { useTranslation } from "react-i18next";
import { formatBytes } from "../lib/format-bytes";
import type { HostMemory, ProcessInventory, ProcessKillTarget, ProcessTreeRow } from "../hooks/useProcessInventoryQuery";
import { Badge } from "./ui/badge";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type MemoryPopoverProps = {
	host: HostMemory;
	trees: ProcessTreeRow[];
	totals: ProcessInventory["totals"];
	/** sessionId whose kill is currently in flight, if any. */
	pendingSessionId?: string;
	killError: string | null;
	onKillTree: (target: ProcessKillTarget) => void;
	onBatchKill: () => void;
};

// The status bar's RAM/Swap segment: a hover tooltip names the AO footprint
// (basic stats), a click opens the extended popover — full host memory
// breakdown plus every AO process tree, with individual kill buttons for
// orphaned trees. Radix tooltip + popover both asChild onto one button (the
// NotificationCenter composition); the tooltip suppresses itself while the
// popover is open so they never stack.
export function MemoryPopover({ host, trees, totals, pendingSessionId, killError, onKillTree, onBatchKill }: MemoryPopoverProps) {
	const { t } = useTranslation();
	const [tooltipOpen, setTooltipOpen] = useState(false);
	const [popoverOpen, setPopoverOpen] = useState(false);
	const orphans = trees.filter((tree) => tree.state === "orphan");
	const pressureFreePercent = host.pressureFreePercent;
	// darwin-only kinds arrive as optional in the generated schema (omitempty).
	const wiredBytes = host.wiredBytes ?? 0;
	const appBytes = host.appBytes ?? 0;
	const compressedBytes = host.compressedBytes ?? 0;

	return (
		<Popover open={popoverOpen} onOpenChange={setPopoverOpen}>
			<Tooltip open={tooltipOpen && !popoverOpen} onOpenChange={setTooltipOpen}>
				<TooltipTrigger asChild>
					<PopoverTrigger asChild>
						<button
							type="button"
							data-testid="status-bar-memory"
							className="flex items-center gap-2 rounded-md px-1 py-0.5 text-caption text-muted-foreground hover:bg-interactive-hover hover:text-foreground"
						>
							<span>
								{t("statusBar.ramLabel")}{" "}
								<span className="tabular-nums">
									{formatBytes(host.usedBytes)} / {formatBytes(host.totalBytes)}
								</span>
							</span>
							{host.swapTotalBytes > 0 && (
								<span>
									{t("statusBar.swapLabel")}{" "}
									<span className="tabular-nums">
										{formatBytes(host.swapUsedBytes)} / {formatBytes(host.swapTotalBytes)}
									</span>
								</span>
							)}
						</button>
					</PopoverTrigger>
				</TooltipTrigger>
				<TooltipContent side="top">
					<p>{t("statusBar.tooltipDaemon", { rss: formatBytes(totals.daemonRssBytes) })}</p>
					<p>{t("statusBar.tooltipSessions", { count: totals.sessionsCount, rss: formatBytes(totals.sessionsRssBytes) })}</p>
					{totals.orphansCount > 0 && (
						<p>{t("statusBar.tooltipOrphans", { count: totals.orphansCount, rss: formatBytes(totals.orphansRssBytes) })}</p>
					)}
				</TooltipContent>
			</Tooltip>
			<PopoverContent side="top" align="end" className="w-80 space-y-3 p-3 text-caption">
				<div>
					<p className="font-medium text-foreground">{t("statusBar.hostMemoryTitle")}</p>
					<div className="mt-1 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
						<span>{t("statusBar.ramLabel")}</span>
						<span className="tabular-nums">
							{formatBytes(host.usedBytes)} / {formatBytes(host.totalBytes)}
						</span>
						{wiredBytes > 0 && (
							<>
								<span>{t("statusBar.kindWired")}</span>
								<span className="tabular-nums">{formatBytes(wiredBytes)}</span>
							</>
						)}
						{appBytes > 0 && (
							<>
								<span>{t("statusBar.kindApp")}</span>
								<span className="tabular-nums">{formatBytes(appBytes)}</span>
							</>
						)}
						{compressedBytes > 0 && (
							<>
								<span>{t("statusBar.kindCompressed")}</span>
								<span className="tabular-nums">{formatBytes(compressedBytes)}</span>
							</>
						)}
						<span>{t("statusBar.kindCached")}</span>
						<span className="tabular-nums">{formatBytes(host.cachedBytes)}</span>
						<span>{t("statusBar.kindFree")}</span>
						<span className="tabular-nums">{formatBytes(host.freeBytes)}</span>
						<span>{t("statusBar.swapLabel")}</span>
						<span className="tabular-nums">
							{formatBytes(host.swapUsedBytes)} / {formatBytes(host.swapTotalBytes)}
						</span>
					</div>
					{host.swapMaxBytes > host.swapTotalBytes && (
						<p className="mt-1 text-muted-foreground">
							{t("statusBar.swapCeiling", { limit: formatBytes(host.swapMaxBytes - host.swapTotalBytes) })}
						</p>
					)}
					{pressureFreePercent != null && (
						<p className="text-muted-foreground">{t("statusBar.tooltipPressure", { percent: pressureFreePercent })}</p>
					)}
				</div>
				<div>
					<div className="flex items-center justify-between gap-2">
						<p className="font-medium text-foreground">{t("statusBar.treesTitle")}</p>
						{orphans.length > 0 && (
							<button
								type="button"
								data-testid="status-bar-kill-all"
								className="text-caption text-destructive hover:underline disabled:pointer-events-none disabled:opacity-50"
								onClick={onBatchKill}
							>
								{t("statusBar.killAllOrphans")}
							</button>
						)}
					</div>
					{killError && (
						<p role="alert" className="mt-1 text-error">
							{killError}
						</p>
					)}
					<ul className="mt-1 max-h-48 space-y-0.5 overflow-y-auto">
						{trees.map((tree) => (
							<li key={`${tree.sessionId}-${tree.rootPid}`} className="flex items-center justify-between gap-2">
								<span className="truncate tabular-nums">
									{tree.sessionId} · {tree.rootPid} · {formatBytes(tree.rssBytes)}
								</span>
								{tree.state === "orphan" ? (
									<button
										type="button"
										aria-label={t("statusBar.killTreeAria", { session: tree.sessionId })}
										disabled={pendingSessionId === tree.sessionId}
										className="shrink-0 text-caption text-destructive hover:underline disabled:pointer-events-none disabled:opacity-50"
										onClick={() =>
											onKillTree({ sessionId: tree.sessionId, rootPid: tree.rootPid, rootLstart: tree.rootLstart })
										}
									>
										{pendingSessionId === tree.sessionId ? "…" : t("statusBar.killTree")}
									</button>
								) : (
									<Badge variant={tree.state === "owned" ? "neutral" : "outline"} className="shrink-0">
										{tree.state === "owned" && !tree.attached
											? `${t("statusBar.stateOwned")} · ${t("statusBar.stateAdopted")}`
											: tree.state === "foreign"
												? t("statusBar.stateForeign")
												: t("statusBar.stateOwned")}
									</Badge>
								)}
							</li>
						))}
						{trees.length === 0 && <li className="text-muted-foreground">{t("statusBar.noTrees")}</li>}
					</ul>
				</div>
			</PopoverContent>
		</Popover>
	);
}
