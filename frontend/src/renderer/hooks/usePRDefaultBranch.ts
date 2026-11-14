import { useQuery } from "@tanstack/react-query";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import type { SessionPRSummary } from "./useSessionScmSummary";

export const prDefaultBranchQueryKey = (url: string, hostId?: string) => ["pr-default-branch", hostId ?? "local", url] as const;

/**
 * The PR repository's default branch, what an ordinary merge targets. The
 * merge confirmation compares the PR's base against it, so a stacked PR
 * landing in a side branch asks first. A failed read shows no default rather
 * than an error: the PR itself is still fine, and the provider re-checks at
 * merge time.
 */
export function usePRDefaultBranch(pr: SessionPRSummary | undefined, hostId?: string, enabled = true) {
	return useQuery({
		queryKey: prDefaultBranchQueryKey(pr?.url ?? "none", hostId),
		enabled: enabled && !usePreviewData && Boolean(pr?.url),
		staleTime: 10 * 60_000,
		// A default branch rarely changes; a missed read retries on the next mount.
		retry: false,
		queryFn: async (): Promise<string> => {
			if (!pr?.url) return "";
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/prs/{id}/default-branch", {
				params: { path: { id: String(pr.number) }, query: { prUrl: pr.url } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Could not read the default branch"));
			return data?.defaultBranch ?? "";
		},
	});
}
