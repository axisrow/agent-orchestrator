import { Fragment, Suspense, useState } from "react";
import { useTranslation } from "react-i18next";
import type { GlobalSettingsSection as GlobalSettingsPage } from "../stores/ui-store";
import { globalSettingsItemsFor } from "./settings/settingsCatalog";
import { PromptOverrideDialog } from "./settings/PromptOverrideDialog";
import { SettingsSection } from "./settings/SettingsSection";

export type GlobalSettingsSection = GlobalSettingsPage | "all";

export function GlobalSettingsForm({
	cloudEnabled = true,
	section = "all",
}: {
	cloudEnabled?: boolean;
	section?: GlobalSettingsSection;
}) {
	const { t } = useTranslation();
	const [agentDefaultsOpen, setAgentDefaultsOpen] = useState(false);
	const all = section === "all";
	// One section per page means the dialog header already names it, so a
	// leading in-page heading would just repeat that title.
	const titleHidden = !all;

	return (
		<>
		<div
			aria-label={t("settings.title")}
			className="flex w-full flex-col gap-(--size-settings-section-gap)"
			data-testid="settings-page"
		>
			// Fork: user-scope prompt override ("Agent Defaults") stays a standalone
			// panel ahead of the catalog's general section, preserving the original
			// section order on both the "all" and "general" pages.
			{(all || section === "general") && (
				<SettingsSection title={t("settings.agentDefaults")} titleHidden={titleHidden}>
					<button
						type="button"
						className="w-full rounded-md bg-[var(--color-bg-settings-row)] px-4 py-3 text-left"
						onClick={() => setAgentDefaultsOpen(true)}
					>
						{t("settings.agentDefaults")}
					</button>
				</SettingsSection>
			)}
			{globalSettingsItemsFor(section, { cloudEnabled }).map((item) => (
				<Fragment key={item.id}>
					<Suspense fallback={null}>{item.render(t, titleHidden)}</Suspense>
				</Fragment>
			))}
		</div>
		<PromptOverrideDialog open={agentDefaultsOpen} onOpenChange={setAgentDefaultsOpen} scope="user" />
		</>
	);
}
