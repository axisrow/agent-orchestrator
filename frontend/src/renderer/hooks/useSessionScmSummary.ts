import type { PROpener } from "@aoagents/product-ui";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef } from "react";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { clientForHost, clientForSessionHost } from "../lib/host-clients";
import { prFactsStale } from "../lib/pr-display";
import { LOCAL_HOST } from "../lib/hosts";
import type { CloudCpPullRequestSummary } from "../lib/cloud-cp";
import { createRendererCloudCpClient } from "../lib/cloud-cp/renderer-client";
import { subscribeSessionEventsBridged } from "../lib/cloud-cp/stream-bridge";
import { useSettings } from "./useSettings";
import { workspaceQueryKeyForHost } from "./useWorkspaceQuery";
import type { PullRequestFacts, WorkspaceSummary } from "../types/workspace";

// openedBy only comes from AO Cloud, which names the person behind a PR its
// GitHub App opened for them.
export type SessionPRSummary = components["schemas"]["SessionPRSummary"] & { openedBy?: PROpener };
export type SessionPRReference = components["schemas"]["SessionPRReference"];

// The session's zone renders from the workspace query while a PR card renders
// from this query — two cache entries over the same daemon facts. Whichever
// refetches first can show a verdict the other still contradicts (issue #336).
// When their gate facts disagree, one invalidation pulls both back to the same
// answer, so the divergence never outlives one refetch. The per-cache,
// per-session stamp bounds it when the workspace list is slow: at most one
// sync per interval.
const FACTS_SYNC_INTERVAL_MS = 1000;
const lastFactsSyncAt = new WeakMap<QueryClient, Map<string, number>>();

function prGateFactsMatch(facts: PullRequestFacts[], summaries: SessionPRSummary[]): boolean {
	if (facts.length !== summaries.length) return false;
	const byUrl = new Map(facts.map((fact) => [fact.url, fact]));
	return summaries.every((summary) => {
		const fact = byUrl.get(summary.url);
		return Boolean(fact) &&
			fact!.state === summary.state &&
			fact!.ci === summary.ci.state &&
			fact!.review === summary.review.decision &&
			fact!.mergeability === summary.mergeability.state &&
			Boolean(fact!.reviewComments) === Boolean(summary.review.hasUnresolvedHumanComments);
	});
}

export const sessionScmSummaryQueryKey = (sessionId?: string, hostId?: string) =>
	sessionId ? (["session-scm-summary", hostId ?? LOCAL_HOST, sessionId] as const) : (["session-scm-summary"] as const);

export async function fetchSessionScmSummary(sessionId: string, hostId?: string) {
	const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/sessions/{sessionId}/pr", {
		params: { path: { sessionId } },
	});
	if (error) throw error;
	return { prs: data?.prs ?? [], linkedPrs: data?.linkedPrs ?? [] };
}

export function cloudPRSummaryToSessionPRSummary(
	pr: CloudCpPullRequestSummary,
	autoInjectCI: boolean,
): SessionPRSummary {
	return {
		...pr,
		provider: pr.provider === "gitlab" ? "gitlab" : "github",
		repo: pr.repository,
		ci: { ...pr.ci, autoInjectCI },
		review: pr.review,
		mergeability: {
			...pr.mergeability,
			prUrl: pr.mergeability.pullRequestUrl,
		},
	};
}

export function sessionScmSummaryQueryOptions(sessionId: string, hostId?: string) {
	return {
		queryKey: sessionScmSummaryQueryKey(sessionId, hostId),
		enabled: Boolean(sessionId),
		queryFn: () => fetchSessionScmSummary(sessionId, hostId),
		retry: 1,
		...(hostId ? { refetchInterval: 15_000 } : {}),
	};
}

export function useSessionScmSummary(
	sessionId?: string,
	enabled = true,
	cloudOrgId?: string,
	cloudAutoInjectCI = false,
	hostId?: string,
) {
	const { settings } = useSettings(undefined, Boolean(cloudOrgId));
	const baseUrl = settings?.cloudControlPlaneUrl ?? "";
	const cloudClient = useMemo(() => createRendererCloudCpClient(baseUrl), [baseUrl]);
	const cloud = Boolean(cloudOrgId);
	const queryClient = useQueryClient();
	const queryKey = useMemo(
		() => cloud
			? ["cloud-session-scm-summary", baseUrl, cloudOrgId, sessionId] as const
			: sessionScmSummaryQueryKey(sessionId, hostId),
		[baseUrl, cloud, cloudOrgId, hostId, sessionId],
	);
	useEffect(() => {
		if (!enabled || !cloudOrgId || !sessionId || baseUrl === "") return;
		const controller = new AbortController();
		let after: number | undefined;
		const reconnect = async () => {
			while (!controller.signal.aborted) {
				await subscribeSessionEventsBridged({
					baseUrl,
					orgId: cloudOrgId,
					sessionId,
					after,
					signal: controller.signal,
					onEvent: (event) => {
						after = Math.max(after ?? 0, event.sequence);
						if (
							event.type === "scm.updated" ||
							event.type === "pull_request.created" ||
							event.type === "pull_request.claimed"
						) {
							void queryClient.invalidateQueries({ queryKey });
						}
					},
				});
				if (controller.signal.aborted) return;
				await new Promise<void>((resolve) => {
					const onAbort = () => {
						clearTimeout(timer);
						resolve();
					};
					const timer = setTimeout(() => {
						controller.signal.removeEventListener("abort", onAbort);
						resolve();
					}, 1000);
					controller.signal.addEventListener("abort", onAbort, { once: true });
				});
			}
		};
		void reconnect();
		return () => controller.abort();
	}, [baseUrl, cloudOrgId, enabled, queryClient, queryKey, sessionId]);
	const query = useQuery({
		queryKey,
		enabled: enabled && Boolean(sessionId) && (!cloud || baseUrl !== ""),
		queryFn: async () => {
			if (!cloudOrgId) return fetchSessionScmSummary(sessionId!, hostId);
			const response = await cloudClient.listSessionPullRequests(cloudOrgId, sessionId!);
			return {
				prs: response.pullRequests.map((pr) => cloudPRSummaryToSessionPRSummary(pr, cloudAutoInjectCI)),
				linkedPrs: [],
			};
		},
		retry: 1,
		...(hostId ? { refetchInterval: 15_000 } : {}),
	});
	// Pull the workspace facts and these summaries back together whenever one
	// has moved without the other (issue #336): the zone and the card must not
	// contradict each other longer than one refetch, focus or not.
	const summaryData = query.data;
	useEffect(() => {
		if (cloud || !enabled || !sessionId || !summaryData) return;
		const workspaces = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKeyForHost(hostId));
		const session = workspaces?.flatMap((workspace) => workspace.sessions).find((candidate) => candidate.id === sessionId);
		if (!session) return;
		if (prGateFactsMatch(session.prs, summaryData.prs)) return;
		const key = `${hostId ?? LOCAL_HOST}:${sessionId}`;
		let stamps = lastFactsSyncAt.get(queryClient);
		if (!stamps) {
			stamps = new Map();
			lastFactsSyncAt.set(queryClient, stamps);
		}
		const now = Date.now();
		if (now - (stamps.get(key) ?? 0) < FACTS_SYNC_INTERVAL_MS) return;
		stamps.set(key, now);
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
		void queryClient.invalidateQueries({ queryKey });
	}, [cloud, enabled, hostId, queryClient, queryKey, sessionId, summaryData]);
	return query;
}

/**
 * One-shot read-only provider refresh for a PR card holding stale facts
 * (#338). A terminated session's PR record never refreshes on its own, so a
 * card opened onto it would render an 80-day-old snapshot as live. While the
 * facts are stale the card fires this once, patches the fresh provider facts
 * into its cached summaries (state, CI, review decision, mergeability and the
 * freshness stamps; unresolved-comment counts are not the peek's to say), and
 * lets the workspace list follow so the zone agrees. A failed or empty peek
 * leaves the card on its snapshot, honestly stale.
 */
export function usePRFactsRefresh(
	sessionId: string | undefined,
	pr: SessionPRSummary | undefined,
	hostId?: string,
	enabled = true,
) {
	const queryClient = useQueryClient();
	const askedFor = useRef<string | null>(null);
	const stale = pr ? prFactsStale(pr) : false;
	const prUrl = pr?.url;
	useEffect(() => {
		if (!enabled || !sessionId || !prUrl || !stale) return;
		if (askedFor.current === prUrl) return;
		askedFor.current = prUrl;
		let cancelled = false;
		void (async () => {
			try {
				const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/pr/refresh", {
					params: { path: { sessionId } },
				});
				if (cancelled || error || !data?.prs?.length) return;
				const fresh = new Map(data.prs.map((peeked) => [peeked.url, peeked]));
				queryClient.setQueryData<{ prs: SessionPRSummary[]; linkedPrs: SessionPRReference[] }>(
					sessionScmSummaryQueryKey(sessionId, hostId),
					(current) => current ? {
						...current,
						prs: current.prs.map((cached) => {
							const peeked = fresh.get(cached.url);
							if (!peeked) return cached;
							return {
								...cached,
								state: peeked.state,
								ci: { ...cached.ci, state: peeked.ci },
								review: { ...cached.review, decision: peeked.review },
								mergeability: { ...cached.mergeability, state: peeked.mergeability },
								updatedAt: peeked.updatedAt,
								observedAt: peeked.updatedAt,
								lastSeenAt: peeked.updatedAt,
							};
						}),
					} : current,
				);
				void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
			} catch {
				// The provider is unreachable: the card keeps its stale badge.
			}
		})();
		return () => {
			cancelled = true;
		};
	}, [enabled, hostId, prUrl, queryClient, sessionId, stale]);
}
