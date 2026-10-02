import { queryOptions } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type AgentModelCatalog = components["schemas"]["AgentModelsResponse"];

const MODEL_CATALOG_VALIDATION_INTERVAL_MS = 10 * 60 * 1_000;

export const agentModelsQueryPrefix = (agentId: string) =>
	["agent-models", agentId] as const;

export const agentModelsQueryKey = (agentId: string, projectId: string, role?: string) =>
	role
		? [...agentModelsQueryPrefix(agentId), projectId, `role:${role}`] as const
		: [...agentModelsQueryPrefix(agentId), projectId] as const;

async function requestAgentModels(
	agentId: string,
	projectId: string,
	mode: "cached" | "refresh" | "revalidate",
	role?: string,
): Promise<AgentModelCatalog> {
	const path = { agent: agentId };
	const query = { projectId: projectId || undefined, role: role || undefined };
	const result =
		mode === "cached"
			? await apiClient.GET("/api/v1/agents/{agent}/models", {
					params: { path, query },
				})
			: await apiClient.POST("/api/v1/agents/{agent}/models/refresh", {
					params: {
						path,
						query: { ...query, revalidate: mode === "revalidate" || undefined },
					},
				});
	if (result.error) throw new Error(apiErrorMessage(result.error));
	return result.data as AgentModelCatalog;
}

export function agentModelsQueryOptions(agentId: string, projectId: string, role?: string) {
	return queryOptions({
		queryKey: agentModelsQueryKey(agentId, projectId, role),
		queryFn: () => requestAgentModels(agentId, projectId, "cached", role),
		enabled: agentId !== "",
		staleTime: MODEL_CATALOG_VALIDATION_INTERVAL_MS,
	});
}

export function refreshAgentModels(agentId: string, projectId: string, role?: string) {
	return requestAgentModels(agentId, projectId, "refresh", role);
}

export function revalidateAgentModels(agentId: string, projectId: string, role?: string) {
	return requestAgentModels(agentId, projectId, "revalidate", role);
}
