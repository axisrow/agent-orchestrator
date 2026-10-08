import { queryOptions } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { foldClaudeAliasDefault } from "../lib/agent-model-choices";
import { clientForHost } from "../lib/host-clients";

export type AgentModelCatalog = components["schemas"]["AgentModelsResponse"];

const MODEL_CATALOG_VALIDATION_INTERVAL_MS = 10 * 60 * 1_000;

export const agentModelsQueryPrefix = (agentId: string) =>
	["agent-models", agentId] as const;

export const agentModelsQueryKey = (agentId: string, projectId: string, hostId?: string, role?: string) => {
	if (hostId) return ["agent-models", hostId, agentId, projectId] as const;
	if (role) return [...agentModelsQueryPrefix(agentId), projectId, `role:${role}`] as const;
	return [...agentModelsQueryPrefix(agentId), projectId] as const;
};

async function requestAgentModels(
	agentId: string,
	projectId: string,
	mode: "cached" | "refresh" | "revalidate",
	hostId?: string,
	role?: string,
): Promise<AgentModelCatalog> {
	const client = hostId ? clientForHost(hostId) : apiClient;
	const path = { agent: agentId };
	const query = { projectId: projectId || undefined, role: role || undefined };
	const result =
		mode === "cached"
			? await client.GET("/api/v1/agents/{agent}/models", {
					params: { path, query },
				})
			: await client.POST("/api/v1/agents/{agent}/models/refresh", {
					params: {
						path,
						query: { ...query, revalidate: mode === "revalidate" || undefined },
					},
				});
	if (result.error) throw new Error(apiErrorMessage(result.error));
	const catalog = result.data as AgentModelCatalog;
	return agentId === "claude-code" ? { ...catalog, models: foldClaudeAliasDefault(catalog.models) } : catalog;
}

export function agentModelsQueryOptions(agentId: string, projectId: string, hostId?: string, role?: string) {
	return queryOptions({
		queryKey: agentModelsQueryKey(agentId, projectId, hostId, role),
		queryFn: () => requestAgentModels(agentId, projectId, "cached", hostId, role),
		enabled: agentId !== "",
		staleTime: MODEL_CATALOG_VALIDATION_INTERVAL_MS,
	});
}

export function refreshAgentModels(agentId: string, projectId: string, hostId?: string, role?: string) {
	return requestAgentModels(agentId, projectId, "refresh", hostId, role);
}

export function revalidateAgentModels(agentId: string, projectId: string, hostId?: string, role?: string) {
	return requestAgentModels(agentId, projectId, "revalidate", hostId, role);
}
