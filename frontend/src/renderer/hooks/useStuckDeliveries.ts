import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";

export type StuckDeliveryGroup = components["schemas"]["StuckDeliveryGroupResponse"];

export const stuckDeliveriesQueryKey = ["stuck-deliveries"] as const;

async function fetchStuckDeliveries(): Promise<StuckDeliveryGroup[]> {
	const { data, error } = await apiClient.GET("/api/v1/reports/deliveries/stuck");
	if (error) throw error;
	return data?.groups ?? [];
}

// The overview's undelivered-message facts, read from the daemon's delivery
// event journal (#270) through the read-only stuck-deliveries grouping. Polled
// at the same cadence as the workspace query so a delivery that lands or gets
// stuck surfaces on the next tick without its own event subscription.
export const stuckDeliveriesQueryOptions = {
	queryKey: stuckDeliveriesQueryKey,
	queryFn: fetchStuckDeliveries,
	staleTime: 10_000,
	refetchInterval: 15_000,
	refetchOnWindowFocus: true,
	retry: 1,
};

/** Undelivered report counts per sending session: `deliveries` summed over
 * each session's stuck groups. Absent sessions have nothing undelivered. */
export function useStuckDeliveriesBySession(): Map<string, number> {
	const query = useQuery({
		...stuckDeliveriesQueryOptions,
		select: (groups: StuckDeliveryGroup[]) => {
			const bySession = new Map<string, number>();
			for (const group of groups) {
				if (!group.sessionId) continue;
				bySession.set(group.sessionId, (bySession.get(group.sessionId) ?? 0) + group.deliveries);
			}
			return bySession;
		},
	});
	return query.data ?? new Map<string, number>();
}
