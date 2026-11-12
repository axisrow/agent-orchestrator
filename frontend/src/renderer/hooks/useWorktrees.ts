import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { workspaceQueryKey } from "./useWorkspaceQuery";

// The response of GET /api/v1/sessions/worktrees: one session workspace's
// registration and its current on-disk size. Terminated worktrees are what
// POST /api/v1/sessions/cleanup can reclaim.
export type WorktreeUsage = {
	sessionId: string;
	projectId?: string;
	path: string;
	bytes: number;
	terminated: boolean;
};

export const worktreesQueryKey = ["worktrees"] as const;

// Sizing walks every workspace directory, so this query is deliberately
// patient: a window-focus refetch only re-walks after five minutes of
// staleness. Statuses come from the workspace query, which refreshes on focus
// far more cheaply; the overview page pairs the two.
export const worktreesQueryOptions = {
	queryKey: worktreesQueryKey,
	queryFn: async (): Promise<WorktreeUsage[]> => {
		const { data, error } = await apiClient.GET("/api/v1/sessions/worktrees");
		if (error) throw error;
		return data?.worktrees ?? [];
	},
	staleTime: 5 * 60_000,
	refetchOnWindowFocus: true,
	retry: 1,
};

export function useWorktreesQuery() {
	return useQuery(worktreesQueryOptions);
}

export function useCleanupWorktrees() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async (projectId: string) => {
			const { data, error } = await apiClient.POST("/api/v1/sessions/cleanup", {
				params: { query: { project: projectId } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		// Cleanup changes what /sessions/worktrees measures and retires
		// terminated sessions; both surfaces must re-read.
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: worktreesQueryKey });
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
	});
}
