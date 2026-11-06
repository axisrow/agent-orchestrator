import { Fragment, Suspense, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil } from "lucide-react";
import { useTranslation } from "react-i18next";
import { type GlobalSettingsSection as GlobalSettingsPage, type HarnessDiagnosticsRequest, useUiStore } from "../stores/ui-store";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { globalSettingsItemsFor } from "./settings/settingsCatalog";
import { PromptOverrideDialog, userConfigQueryKey } from "./settings/PromptOverrideDialog";
import { SettingsRow } from "./settings/SettingsRow";
import { SettingsPageTitleContext, SettingsSection } from "./settings/SettingsSection";
import { Switch } from "./ui/switch";

export type GlobalSettingsSection = GlobalSettingsPage | "all";

export function GlobalSettingsForm({
	cloudEnabled = true,
	customCoder = false,
	selfServeBilling = false,
	focusAgentId,
	hostId,
	harnessView,
	startLogin,
	showDiagnostics,
	section = "all",
}: {
	cloudEnabled?: boolean;
	/** The org's offering includes coder (SettingsDialog reads useCloudOffering). */
	customCoder?: boolean;
	/** The org is on self-serve billing. */
	selfServeBilling?: boolean;
	focusAgentId?: string;
	hostId?: string;
	harnessView?: "local" | "cloud";
	startLogin?: boolean;
	showDiagnostics?: HarnessDiagnosticsRequest;
	section?: GlobalSettingsSection;
}) {
	const { t } = useTranslation();
	const [agentDefaultsOpen, setAgentDefaultsOpen] = useState(false);
	const developerMode = useUiStore((state) => state.developerMode);
	const diagnostics = useUiStore((state) => state.developerMode && state.diagnostics);
	const all = section === "all";
	const context = { cloudEnabled, developerMode, diagnostics, customCoder, selfServeBilling, focusAgentId, hostId, harnessView, startLogin, showDiagnostics, singlePage: !all };
	const items = globalSettingsItemsFor(section, context);
	// On a single page the pane's title names it; SettingsSection hides only a
	// heading that would repeat that title, so other section headings show.
	const pageTitle = all ? null : (items[0]?.label(t) ?? null);
	const titleHidden = !all;
	// Fork: user-scope prompt override ("Agent Defaults") stays a standalone
	// panel ahead of the catalog's general section, preserving the original
	// section order on both the "all" and "general" pages.

	return (
		<SettingsPageTitleContext.Provider value={pageTitle}>
			<div
				aria-label={t("settings.title")}
				className="flex w-full flex-col gap-(--size-settings-section-gap)"
			>
				{(all || section === "general") && (
					<SettingsSection title={t("settings.agentDefaults")} titleHidden={titleHidden} grouped>
						{/* Same link-style settings row as the project-scope trigger
						    (issue #21): one affordance, identical everywhere. */}
						<button
							type="button"
							className="settings-row-bar w-full text-left transition-colors hover:bg-settings-menu-selected focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak"
							onClick={() => setAgentDefaultsOpen(true)}
						>
							<span className="min-w-0 flex-1">
								<span className="block text-sm leading-5 text-settings-label">{t("settings.agentDefaults")}</span>
								<span className="mt-0.5 block text-pretty text-control leading-4 text-settings-muted">{t("settings.promptOverride.description", { scope: t("settings.promptOverride.globalScope") })}</span>
							</span>
							<Pencil className="size-icon-lg shrink-0 text-settings-muted" aria-hidden="true" />
						</button>
						<TerminateOnMergeDefaultRow />
					</SettingsSection>
				)}
				{items.map((item) => (
					<Fragment key={item.id}>
						<Suspense fallback={null}>{item.render(t, false, context)}</Suspense>
					</Fragment>
				))}
			</div>
			<PromptOverrideDialog open={agentDefaultsOpen} onOpenChange={setAgentDefaultsOpen} scope="user" />
		</SettingsPageTitleContext.Provider>
	);
}

/**
 * User-scope default for the per-session terminate-on-merge toggle (fork
 * #285). A project default wins when set; the per-session switch stays
 * authoritative after spawn. PUT /api/v1/user-config replaces the stored
 * config wholesale, so the flip merges over the loaded agentConfig — the same
 * round-trip PromptOverrideDialog uses for the neighboring overrides.
 */
function TerminateOnMergeDefaultRow() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: userConfigQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/user-config");
			if (error) throw new Error(apiErrorMessage(error));
			return data.agentConfig ?? {};
		},
	});
	const stored = query.data;
	const mutation = useMutation({
		mutationFn: async (terminateOnPrMerge: boolean) => {
			if (!stored) return;
			const { error } = await apiClient.PUT("/api/v1/user-config", {
				body: { agentConfig: { ...stored, terminateOnPrMerge } },
			});
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: userConfigQueryKey });
		},
	});
	const error = mutation.error instanceof Error ? mutation.error.message : null;
	return (
		<div className="flex w-full flex-col">
			<SettingsRow
				className="rounded-none"
				label={t("settings.terminateOnMergeDefaultToggle")}
				description={error ?? t("settings.terminateOnMergeDefaultDescription")}
			>
				<Switch
					aria-label={t("settings.terminateOnMergeDefaultToggle")}
					checked={Boolean(stored?.terminateOnPrMerge)}
					disabled={query.isLoading || mutation.isPending || !stored}
					onCheckedChange={(checked) => mutation.mutate(checked)}
				/>
			</SettingsRow>
			{error ? (
				<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
					{error}
				</p>
			) : null}
		</div>
	);
}
