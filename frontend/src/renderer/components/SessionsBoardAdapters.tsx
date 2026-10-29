import { useEffect, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
	scmUserAvatarUrl,
	SessionCardView,
	type BoardPullRequestLabels,
	type BoardPullRequestProgress,
	type BoardSessionPresentation,
	type ProductUITranslator,
} from "@aoagents/product-ui";
import { Check, Copy, GitBranch, RotateCcw } from "lucide-react";
import type { MessageKey } from "../i18n";
import { aoBridge } from "../lib/bridge";
import { sessionBranchPresentation } from "../lib/session-branch";
import { BranchNamingLabel } from "./BranchNamingLabel";
import { formatTimeCompact } from "../lib/format-time";
import { prBrowserUrl, sessionPRDisplaySummaries } from "../lib/pr-display";
import {
	agentSwitchStatusVisual,
	deriveSessionAgentSwitchPresentation,
} from "../lib/agent-switch-presentation";
import { agentLabel } from "../lib/agent-options";
import type { WorkspaceSession } from "../types/workspace";
import { canonicalTrackerIssueId } from "../types/workspace";
import { useSessionScmSummary } from "../hooks/useSessionScmSummary";
import { cn } from "../lib/utils";
import { AgentAvatar } from "./AgentAvatar";
import { ProductExternalLink } from "./ProductExternalLink";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

export function toBoardSessionPresentation(
	session: WorkspaceSession,
	t?: TFunction,
): BoardSessionPresentation {
	const switchPresentation = deriveSessionAgentSwitchPresentation(session);
	const switchVisual = switchPresentation ? agentSwitchStatusVisual(switchPresentation) : undefined;
	const provisioningStatus =
		session.provisionState === "provisioning"
			? {
					className: "text-status-working",
					indicatorClassName: "bg-status-working animate-status-pulse",
					label: `Starting ${agentLabel(session.provider)}…`,
					tone: "var(--color-status-working)",
				}
			: undefined;
	const failedStartStatus =
		session.provisionState === "failed"
			? {
					className: "text-status-exited",
					indicatorClassName: "bg-status-exited",
					label: "Start failed",
					tone: "var(--color-status-exited)",
				}
			: undefined;
	return {
		activity: session.activity,
		branch: sessionBranchPresentation(session).branch,
		id: session.id,
		isTerminated: session.isTerminated,
		kanbanColumn: session.kanbanColumn,
		displayStatus: session.displayStatus,
		provider: session.provider,
		status: session.status,
		statusReadiness: session.statusReadiness,
		statusPresentation:
			provisioningStatus ?? failedStartStatus ??
			(t && switchPresentation && switchVisual
				? {
						className: switchVisual.className,
						indicatorClassName: `${switchVisual.indicatorClassName}${switchVisual.breathe ? " animate-status-pulse" : ""}`,
						label: t(switchPresentation.compactLabelKey, switchPresentation.values),
						tone: switchVisual.tone,
					}
				: undefined),
		title: session.title,
		trackerIssueId: canonicalTrackerIssueId(session.issueId),
		updatedAt: session.updatedAt,
		lastUserMessageAt: session.lastUserMessageAt,
	};
}

export function ArchivedSessionCardAdapter({
	hideTerminatedStatus = false,
	isRestoreDisabled,
	isRestoring,
	restoreAction,
	restoreError,
	session,
}: {
	hideTerminatedStatus?: boolean;
	isRestoreDisabled: boolean;
	isRestoring: boolean;
	restoreAction: (event: MouseEvent<HTMLButtonElement>) => void;
	restoreError?: string;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const branch = sessionBranchPresentation(session).branch ?? "";
	return (
		<DesktopSessionCard
			hideTerminatedStatus={hideTerminatedStatus}
			action={
				<ArchiveRestoreButton
					isDisabled={isRestoreDisabled}
					isRestoring={isRestoring}
					label={t("shell.restoreNamed", { title: session.title })}
					onClick={restoreAction}
				/>
			}
			branchAction={
				branch ? (
					<CopyActionButton
						copiedLabel={t("shell.copiedBranchNamed", { branch })}
						copyLabel={t("shell.copyBranchNamed", { branch })}
						value={branch}
					/>
				) : undefined
			}
			footer={<ArchiveRestoreError message={restoreError} />}
			session={session}
		/>
	);
}

function DesktopSessionCard({
	action,
	branchAction,
	footer,
	hideTerminatedStatus = false,
	session,
}: {
	action?: ReactNode;
	branchAction?: ReactNode;
	footer?: ReactNode;
	hideTerminatedStatus?: boolean;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const summaries = sessionPRDisplaySummaries(
		session,
		useSessionScmSummary(session.id, true, session.cloud?.orgId, session.cloud?.orgId ? session.autoInjectCI === true : false, session.hostId).data?.prs,
	);
	const translate: ProductUITranslator = (key, values) => t(key as MessageKey, values);

	return (
		<SessionCardView
			action={action}
			branchAction={branchAction}
			branchLabel={sessionBranchPresentation(session).naming ? <BranchNamingLabel /> : undefined}
			branchIcon={<GitBranch aria-hidden="true" className="size-icon-2xs shrink-0" />}
			externalLink={ProductExternalLink}
			footer={
				<>
					{footer}
				</>
			}
			interactive={false}
			labels={{
				formatTime: formatTimeCompact,
				intakeIssue: (id) => t("shell.intakeIssue", { id }),
				pr: pullRequestLabels(t),
				updatedAt: (timestamp: string) =>
					t("shell.lastMessageAt", { time: formatTimeCompact(timestamp) }),
			}}
			prs={summaries.map((pr) => ({
				commentCount: pr.review.unresolvedBy.reduce((count, reviewer) => count + reviewer.count, 0),
				number: pr.number,
				reviewers: Array.from(
					new Map(
						pr.review.unresolvedBy.map((reviewer) => [reviewer.reviewerId, reviewer]),
					).values(),
				).map((reviewer) => ({
					avatarUrl: scmUserAvatarUrl(pr.provider, prBrowserUrl(pr), reviewer.reviewerId),
					id: reviewer.reviewerId,
				})),
				state: pr.state,
				url: prBrowserUrl(pr),
			}))}
			renderAvatar={(provider) => <AgentAvatar provider={provider} />}
			session={
				hideTerminatedStatus
					? hideTerminatedCardStatus(toBoardSessionPresentation(session, t))
					: toBoardSessionPresentation(session, t)
			}
			translate={translate}
		/>
	);
}

function hideTerminatedCardStatus(session: BoardSessionPresentation): BoardSessionPresentation {
	const terminated =
		session.displayStatus === "Terminated" ||
		(session.status === "terminated" && !session.displayStatus);
	if (!terminated) return session;
	return {
		...session,
		displayStatus: undefined,
		statusPresentation: {
			className: "hidden",
			indicatorClassName: "hidden",
			label: "",
			tone: "transparent",
		},
	};
}

function pullRequestLabels(t: TFunction): BoardPullRequestLabels {
	return {
		progress: (progress) => pullRequestProgressLabel(progress, t),
		short: t("pr.short"),
		states: {
			closed: t("pr.state.closed"),
			draft: t("pr.state.draft"),
			merged: t("pr.state.merged"),
			open: t("pr.state.open"),
		},
	};
}

function pullRequestProgressLabel(
	{ closed, draft, merged, open, total }: BoardPullRequestProgress,
	t: TFunction,
): string {
	return [
		t("pr.progress.merged", { count: total, merged }),
		open > 0 ? t("pr.progress.open", { count: open }) : undefined,
		draft > 0 ? t("pr.progress.draft", { count: draft }) : undefined,
		closed > 0 ? t("pr.progress.closed", { count: closed }) : undefined,
	]
		.filter((part): part is string => part !== undefined)
		.join(" · ");
}

function ArchiveRestoreButton({
	label,
	onClick,
	isRestoring,
	isDisabled,
}: {
	label: string;
	onClick: (event: MouseEvent<HTMLButtonElement>) => void;
	isRestoring: boolean;
	isDisabled: boolean;
}) {
	const { t } = useTranslation();
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex">
					<button
						aria-label={label}
						className="grid size-control-board-sm shrink-0 place-items-center rounded-md text-passive transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent/50 disabled:cursor-not-allowed disabled:opacity-35"
						disabled={isDisabled}
						onClick={onClick}
						type="button"
					>
						<RotateCcw className={cn("size-icon-md", isRestoring && "animate-spin")} aria-hidden="true" />
					</button>
				</span>
			</TooltipTrigger>
			<TooltipContent side="top">
				{isRestoring ? t("shell.restoringSession") : t("shell.restoreSession")}
			</TooltipContent>
		</Tooltip>
	);
}

function ArchiveRestoreError({ message }: { message?: string }) {
	return message ? (
		<div className="border-t border-border px-2 py-1.5 text-2xs text-destructive" role="alert">
			{message}
		</div>
	) : null;
}

function CopyActionButton({
	copiedLabel,
	copyLabel,
	value,
}: {
	copiedLabel: string;
	copyLabel: string;
	value: string;
}) {
	const [copied, setCopied] = useState(false);
	const copiedTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
	useEffect(
		() => () => {
			if (copiedTimeoutRef.current !== null) clearTimeout(copiedTimeoutRef.current);
		},
		[],
	);
	const buttonLabel = copied ? copiedLabel : copyLabel;
	const copyValue = async (event: MouseEvent<HTMLButtonElement>) => {
		event.stopPropagation();
		try {
			await aoBridge.clipboard.writeText(value);
		} catch {
			return;
		}
		setCopied(true);
		if (copiedTimeoutRef.current !== null) clearTimeout(copiedTimeoutRef.current);
		copiedTimeoutRef.current = setTimeout(() => {
			setCopied(false);
			copiedTimeoutRef.current = null;
		}, 1_500);
	};
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					aria-label={buttonLabel}
					className="inline-flex size-4 shrink-0 items-center justify-center rounded-sm text-passive transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
					onClick={(event) => void copyValue(event)}
					type="button"
				>
					{copied ? (
						<Check className="size-icon-2xs text-success" aria-hidden="true" />
					) : (
						<Copy className="size-icon-2xs" aria-hidden="true" />
					)}
				</button>
			</TooltipTrigger>
			<TooltipContent side="bottom">{buttonLabel}</TooltipContent>
		</Tooltip>
	);
}
