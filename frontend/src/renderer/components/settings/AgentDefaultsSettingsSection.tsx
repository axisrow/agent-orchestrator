import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { components } from "../../../api/schema";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { captureRendererEvent } from "../../lib/telemetry";
import { Button } from "../ui/button";
import { SettingsSection } from "./SettingsSection";

type AgentConfig = components["schemas"]["AgentConfig"];

export const userConfigQueryKey = ["user-config"] as const;

/**
 * Global worker/orchestrator prompt override. The first API-backed section in
 * GlobalSettingsForm (the rest use the IPC bridge): reads/writes
 * /api/v1/user-config via the typed apiClient. PUT replaces AgentConfig
 * wholesale, so the two edited fields are merged over the loaded config to
 * preserve model/permissions/env/mcp/pluginDirs/systemPrompt.
 */
export function AgentDefaultsSettingsSection() {
	const queryClient = useQueryClient();

	const query = useQuery({
		queryKey: userConfigQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/user-config");
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
	});

	const agentConfig = query.data?.agentConfig ?? {};
	const [workerPrompt, setWorkerPrompt] = useState(agentConfig.workerPromptOverride ?? "");
	const [orchestratorPrompt, setOrchestratorPrompt] = useState(agentConfig.orchestratorPromptOverride ?? "");
	const [savedAt, setSavedAt] = useState<number | null>(null);

	// Re-sync local form when the server value loads or changes (e.g. after invalidation).
	useEffect(() => {
		setWorkerPrompt(agentConfig.workerPromptOverride ?? "");
		setOrchestratorPrompt(agentConfig.orchestratorPromptOverride ?? "");
	}, [agentConfig.workerPromptOverride, agentConfig.orchestratorPromptOverride]);

	const mutation = useMutation({
		mutationFn: async () => {
			void captureRendererEvent("ao.renderer.user_settings_save_requested");
			// Wholesale replace: merge the two edited fields over the loaded
			// agentConfig so model/permissions/env/mcp/pluginDirs/systemPrompt survive.
			const next: AgentConfig = {
				...agentConfig,
				workerPromptOverride: workerPrompt.trim() || undefined,
				orchestratorPromptOverride: orchestratorPrompt.trim() || undefined,
			};
			const { error } = await apiClient.PUT("/api/v1/user-config", {
				body: { agentConfig: next },
			});
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => {
			void captureRendererEvent("ao.renderer.user_settings_save_succeeded");
			setSavedAt(Date.now());
			void queryClient.invalidateQueries({ queryKey: userConfigQueryKey });
		},
		onError: () => {
			void captureRendererEvent("ao.renderer.user_settings_save_failed");
		},
	});

	return (
		<SettingsSection title="Agent defaults" sectionId="agent-defaults">
			{query.isLoading && <SettingsNote>Loading agent defaults…</SettingsNote>}
			{query.isError && (
				<SettingsNote variant="error">
					{query.error instanceof Error ? query.error.message : "Could not load agent defaults."}
				</SettingsNote>
			)}
			<Field label="Worker prompt override" htmlFor="userWorkerPrompt">
				<textarea
					id="userWorkerPrompt"
					className="settings-field-control min-h-(--size-textarea-min) resize-y py-2.5"
					placeholder="(default worker system prompt)"
					value={workerPrompt}
					onChange={(e) => {
						setWorkerPrompt(e.target.value);
						setSavedAt(null);
					}}
					disabled={query.isLoading}
				/>
				<FieldHint>Replaces the hardcoded worker system prompt globally (all projects). Empty = default baseline.</FieldHint>
			</Field>
			<Field label="Orchestrator prompt override" htmlFor="userOrchestratorPrompt">
				<textarea
					id="userOrchestratorPrompt"
					className="settings-field-control min-h-(--size-textarea-min) resize-y py-2.5"
					placeholder="(default orchestrator system prompt)"
					value={orchestratorPrompt}
					onChange={(e) => {
						setOrchestratorPrompt(e.target.value);
						setSavedAt(null);
					}}
					disabled={query.isLoading}
				/>
				<FieldHint>
					Replaces the hardcoded orchestrator system prompt globally (all projects). Empty = default baseline.
				</FieldHint>
			</Field>
			<div className="flex items-center gap-3 pt-1">
				<Button
					type="button"
					variant="primary"
					disabled={mutation.isPending || query.isLoading}
					onClick={() => {
						setSavedAt(null);
						mutation.mutate();
					}}
				>
					{mutation.isPending ? "Saving…" : "Save"}
				</Button>
				{mutation.isError && (
					<span className="text-xs text-error">
						{mutation.error instanceof Error ? mutation.error.message : "Save failed"}
					</span>
				)}
				{savedAt && !mutation.isPending && !mutation.isError && <span className="text-xs text-success">Saved.</span>}
			</div>
		</SettingsSection>
	);
}

function Field({ label, htmlFor, children }: { label: string; htmlFor?: string; children: React.ReactNode }) {
	return (
		<div className="flex flex-col gap-1.5">
			<label className="settings-field-label" htmlFor={htmlFor}>
				{label}
			</label>
			{children}
		</div>
	);
}

function FieldHint({ children }: { children: React.ReactNode }) {
	return <span className="text-caption leading-4 text-settings-muted">{children}</span>;
}

function SettingsNote({ children, variant = "muted" }: { children: React.ReactNode; variant?: "muted" | "error" }) {
	return <p className={`text-caption leading-4 ${variant === "error" ? "text-error" : "text-settings-muted"}`}>{children}</p>;
}
