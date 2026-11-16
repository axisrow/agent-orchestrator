import { useQuery } from "@tanstack/react-query";
import type { CloudCpSessionChild, CloudCpSessionPullRequest } from "../lib/cloud-cp";
import { mockOrchestratorChildren } from "../lib/mock-data";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import {
	type AgentProvider,
	type PRState,
	type PullRequestFacts,
	type SessionActivity,
	type SessionStatus,
	toAgentProvider,
	toSessionActivity,
	toSessionStatus,
	type WorkspaceSession,
} from "../types/workspace";
import { useCloudCp } from "./useCloudCp";
import { useCloudOrg } from "./useCloudOrg";

export const orchestratorChildrenQueryKey = ["cloud-session-children"] as const;

/**
 * One worker session as the orchestrator inspector renders it. Structurally
 * compatible with the session shape getSessionStatusDotView consumes, so the
 * sidebar's status-dot presentation applies unchanged.
 */
export type OrchestratorChildView = {
	id: string;
	title: string;
	provider: AgentProvider;
	status: SessionStatus;
	/** The delivery tone the child's dot keeps: the wire sends no separate SCM
	 * status, so the aggregated status is that signal here (#355 keeps the
	 * lifecycle overrides on top of it). */
	scmStatus: SessionStatus;
	activity: SessionActivity | null;
	isTerminated: boolean;
	updatedAt: string;
	prs: PullRequestFacts[];
};

function toPullRequestFacts(pr: CloudCpSessionPullRequest): PullRequestFacts {
	return {
		url: pr.url,
		number: pr.number,
		state: pr.state as PRState,
		ci: pr.ci,
		review: pr.review,
		mergeability: pr.mergeability,
		failingChecks: pr.failingChecks,
		reviewComments: pr.reviewComments,
		updatedAt: pr.updatedAt,
	};
}

export function toOrchestratorChildView(child: CloudCpSessionChild): OrchestratorChildView {
	return {
		id: child.id,
		title: child.displayName || child.id,
		provider: toAgentProvider(child.harness),
		status: toSessionStatus(child.status, child.isTerminated),
		scmStatus: toSessionStatus(child.status, child.isTerminated),
		activity: toSessionActivity({ state: child.activityState }) ?? null,
		isTerminated: child.isTerminated,
		updatedAt: child.updatedAt,
		prs: child.prs.map(toPullRequestFacts),
	};
}

/** Live children first (server order: newest activity first), history last. */
export function sortChildViews(children: OrchestratorChildView[]): OrchestratorChildView[] {
	return [...children].sort((a, b) => Number(a.isTerminated) - Number(b.isTerminated));
}

/**
 * The sessions a cloud orchestrator spawned, for the inspector's Workers
 * section. Enabled only for cloud orchestrator sessions; polls on the same
 * cadence as the cloud session list so status and PR chips track the board.
 * In browser preview mode the demo orchestrator serves mock children so the
 * section is designable without a control plane.
 */
export function useOrchestratorChildren(session: WorkspaceSession) {
	const { client, ready, baseUrl } = useCloudCp();
	const { org } = useCloudOrg();
	const orgId = session.cloud?.orgId ?? org?.id;
	const preview = usesPreviewWorkspaceData && session.kind === "orchestrator";
	const enabled = preview || (ready && session.kind === "orchestrator" && session.cloud !== undefined && orgId !== undefined);
	return useQuery({
		queryKey: [...orchestratorChildrenQueryKey, baseUrl, orgId ?? "", session.id],
		enabled,
		retry: 1,
		refetchInterval: preview ? false : 5000,
		queryFn: async (): Promise<OrchestratorChildView[]> => {
			if (preview) {
				return sortChildViews((mockOrchestratorChildren[session.id] ?? []).map(toOrchestratorChildView));
			}
			if (orgId === undefined) return [];
			// First page only (control-plane max page size), matching the cloud
			// session list's pagination stance.
			const response = await client.listSessionChildren(orgId, session.id, { limit: 100 });
			return sortChildViews(response.items.map(toOrchestratorChildView));
		},
	});
}
