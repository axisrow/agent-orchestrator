import { useQuery, type Query, type UseQueryResult } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";

export type ProcessInventory = components["schemas"]["ProcessInventoryResponse"];
export type ProcessKillTarget = components["schemas"]["ProcessKillTarget"];
export type HostMemory = NonNullable<ProcessInventory["host"]>;
export type ProcessTreeRow = ProcessInventory["trees"][number];

export const processInventoryQueryKey = ["process-inventory"] as const;

// Null on failure: the status bar degrades to nothing when the daemon is
// headless, older than this surface, or unreachable.
async function fetchProcessInventory(): Promise<ProcessInventory | null> {
	const { data } = await apiClient.GET("/api/v1/system/processes");
	return data ?? null;
}

// Shared so the status bar and tests read one cache entry. Polls quietly at
// 15s while the footprint is healthy and drops to 5s once orphaned trees
// exist, so a kill's aftermath lands on the next tick.
export const processInventoryQueryOptions = {
	queryKey: processInventoryQueryKey,
	queryFn: fetchProcessInventory,
	retry: 1,
	staleTime: 10_000,
	refetchInterval: (query: Query<ProcessInventory | null>) =>
		(query.state.data?.totals.orphansCount ?? 0) > 0 ? 5_000 : 15_000,
};

export function useProcessInventoryQuery(): UseQueryResult<ProcessInventory | null, Error> {
	return useQuery(processInventoryQueryOptions);
}
