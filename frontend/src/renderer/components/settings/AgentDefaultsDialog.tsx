import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import type { components } from "../../../api/schema";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { captureRendererEvent } from "../../lib/telemetry";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";

type AgentConfig = components["schemas"]["AgentConfig"];

export const userConfigQueryKey = ["user-config"] as const;

type AgentDefaultsDialogProps = {
	open: boolean;
	onOpenChange: (open: boolean) => void;
};

/**
 * Agent defaults override editor, surfaced as a dialog (Report-a-problem
 * pattern). Reads/writes /api/v1/user-config via the typed apiClient. The
 * textareas are prefilled with the FULL assembled default system prompt served
 * by GET /api/v1/user-config (defaultWorkerPrompt / defaultOrchestratorPrompt),
 * so the user sees and edits the real hardcoded baseline rather than starting
 * from an empty box.
 *
 * Save semantics: if the edited text equals the default (trimmed), the override
 * is cleared (stored as undefined) so the hardcoded baseline is used; any other
 * text is stored as the override. PUT replaces AgentConfig wholesale, so the
 * two derived fields are merged over the loaded config to preserve
 * model/permissions/env/mcp/pluginDirs/systemPrompt.
 *
 * "Reset to default" bypasses the compare-with-default dance: it refills the
 * textareas with the hardcoded baseline and writes an explicit
 * {workerPromptOverride: undefined, orchestratorPromptOverride: undefined} PUT
 * (merged over the loaded config so model/permissions survive).
 */
export function AgentDefaultsDialog({ open, onOpenChange }: AgentDefaultsDialogProps) {
	const workerId = useId();
	const orchestratorId = useId();
	const workerRef = useRef<HTMLTextAreaElement>(null);

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
	const defaultWorker = query.data?.defaultWorkerPrompt ?? "";
	const defaultOrchestrator = query.data?.defaultOrchestratorPrompt ?? "";

	// The textarea shows the stored override when one exists, otherwise the full
	// assembled default baseline. The "displayed" value is what the user sees and
	// edits; it re-syncs whenever the server value or defaults load/change.
	const storedWorker = agentConfig.workerPromptOverride ?? "";
	const storedOrchestrator = agentConfig.orchestratorPromptOverride ?? "";
	const initialWorker = storedWorker || defaultWorker;
	const initialOrchestrator = storedOrchestrator || defaultOrchestrator;

	const [workerPrompt, setWorkerPrompt] = useState(initialWorker);
	const [orchestratorPrompt, setOrchestratorPrompt] = useState(initialOrchestrator);
	const [savedAt, setSavedAt] = useState<number | null>(null);

	// Re-sync the form when the dialog opens or the server value/defaults change.
	useEffect(() => {
		if (!open) return;
		setWorkerPrompt(storedWorker || defaultWorker);
		setOrchestratorPrompt(storedOrchestrator || defaultOrchestrator);
		setSavedAt(null);
	}, [open, storedWorker, storedOrchestrator, defaultWorker, defaultOrchestrator]);

	// Save the two overrides. Pass an explicit `{ worker, orchestrator }` to
	// bypass the textarea state — used by "Reset to default" so the override is
	// cleared regardless of the current textarea text (state updates are async,
	// so mutating off the just-set state would race).
	const mutation = useMutation({
		mutationFn: async (overrides?: { worker?: string; orchestrator?: string }) => {
			const explicit = overrides !== undefined;
			void captureRendererEvent(
				explicit
					? "ao.renderer.user_settings_reset_to_default"
					: "ao.renderer.user_settings_save_requested",
			);
			// If the edited text equals the default (trimmed), clear the override so
			// the hardcoded baseline is used; any other text becomes the override.
			const workerOverride = explicit
				? overrides!.worker
				: workerPrompt.trim() && workerPrompt.trim() !== defaultWorker.trim()
					? workerPrompt.trim()
					: undefined;
			const orchestratorOverride = explicit
				? overrides!.orchestrator
				: orchestratorPrompt.trim() && orchestratorPrompt.trim() !== defaultOrchestrator.trim()
					? orchestratorPrompt.trim()
					: undefined;
			// Wholesale replace: merge the two derived fields over the loaded
			// agentConfig so model/permissions/env/mcp/pluginDirs/systemPrompt survive.
			const next: AgentConfig = {
				...agentConfig,
				workerPromptOverride: workerOverride,
				orchestratorPromptOverride: orchestratorOverride,
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
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent
				showCloseButton={false}
				className={settingsDialogContentClass}
				onOpenAutoFocus={(event) => {
					event.preventDefault();
					workerRef.current?.focus();
				}}
				onKeyDown={(event) => {
					// Cmd/Ctrl+Enter saves; a plain Enter keeps inserting newlines.
					if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
						event.preventDefault();
						setSavedAt(null);
						mutation.mutate(undefined);
					}
				}}
			>
				<DialogClose asChild>
					<button
						type="button"
						className="settings-dialog-close-button settings-close-button"
						aria-label="Close agent defaults dialog"
						title="Close (Esc)"
					>
						<X className="size-5" aria-hidden="true" />
					</button>
				</DialogClose>

				<div className={settingsDialogHeaderClass}>
					<DialogTitle className="settings-dialog-title">Agent defaults</DialogTitle>
					<DialogDescription className="text-control leading-4 text-settings-muted">
						Override the hardcoded worker and orchestrator system prompts globally (all projects).
					</DialogDescription>
				</div>

				<div className={settingsDialogBodyClass}>
					{query.isLoading && <p className="text-caption leading-4 text-settings-muted">Loading agent defaults…</p>}
					{query.isError && (
						<p role="alert" className="text-caption leading-4 text-error">
							{query.error instanceof Error ? query.error.message : "Could not load agent defaults."}
						</p>
					)}

					<div className="flex flex-col gap-1.5">
						<label className="settings-field-label" htmlFor={workerId}>
							Worker prompt override
						</label>
						<textarea
							ref={workerRef}
							id={workerId}
							className="settings-field-control min-h-(--size-textarea-min) resize-y py-2.5 font-mono text-caption"
							value={workerPrompt}
							onChange={(e) => {
								setWorkerPrompt(e.target.value);
								setSavedAt(null);
							}}
							disabled={query.isLoading}
							spellCheck={false}
						/>
						<span className="text-caption leading-4 text-settings-muted">
							This replaces the hardcoded worker system prompt for all projects. Edit the default above; clearing it
							back to the default restores the baseline.
						</span>
					</div>

					<div className="flex flex-col gap-1.5">
						<label className="settings-field-label" htmlFor={orchestratorId}>
							Orchestrator prompt override
						</label>
						<textarea
							id={orchestratorId}
							className="settings-field-control min-h-(--size-textarea-min) resize-y py-2.5 font-mono text-caption"
							value={orchestratorPrompt}
							onChange={(e) => {
								setOrchestratorPrompt(e.target.value);
								setSavedAt(null);
							}}
							disabled={query.isLoading}
							spellCheck={false}
						/>
						<span className="text-caption leading-4 text-settings-muted">
							This replaces the hardcoded orchestrator system prompt for all projects. Edit the default above; clearing
							it back to the default restores the baseline.
						</span>
					</div>

					{mutation.isError && (
						<p role="alert" className="text-caption leading-4 text-error">
							{mutation.error instanceof Error ? mutation.error.message : "Save failed"}
						</p>
					)}
					{savedAt && !mutation.isPending && !mutation.isError && (
						<p className="text-caption leading-4 text-success">Saved.</p>
					)}
				</div>

				<div className={settingsDialogFooterClass}>
					<button
						type="button"
						className="settings-footer-button mr-auto"
						disabled={mutation.isPending || query.isLoading}
						aria-label="Reset to default (restore the hardcoded AO prompt, clears your override)"
						title="Restore the hardcoded AO prompt (clears your override)"
						onClick={() => {
							// Refill the textareas with the hardcoded baseline, then clear
							// the stored override explicitly. Explicit undefined is passed
							// so the override is wiped regardless of the textarea state
							// (state updates are async and would race the mutation closure).
							setWorkerPrompt(defaultWorker);
							setOrchestratorPrompt(defaultOrchestrator);
							setSavedAt(null);
							mutation.mutate({ worker: undefined, orchestrator: undefined });
						}}
					>
						Reset to default
					</button>
					<DialogClose asChild>
						<button type="button" className="settings-footer-button">
							Cancel
						</button>
					</DialogClose>
					<button
						type="button"
						className="settings-footer-button border-transparent bg-settings-accent text-white disabled:cursor-not-allowed disabled:opacity-50"
						disabled={mutation.isPending || query.isLoading}
						onClick={() => {
							setSavedAt(null);
							mutation.mutate(undefined);
						}}
					>
						{mutation.isPending ? "Saving…" : "Save"}
					</button>
				</div>
			</DialogContent>
		</Dialog>
	);
}
