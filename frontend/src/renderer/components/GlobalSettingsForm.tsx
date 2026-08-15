import { lazy, Suspense, useState } from "react";
import { useTranslation } from "react-i18next";
import type { GlobalSettingsSection as GlobalSettingsPage } from "../stores/ui-store";
import { PromptOverrideDialog } from "./settings/PromptOverrideDialog";
import { GeneralSettingsSection } from "./settings/GeneralSettingsSection";
import { CloudCredentialsSection } from "./settings/CloudCredentialsSection";
import { ConnectMobileContent } from "./settings/ConnectMobileContent";
import { KeyboardShortcutsContent } from "./settings/KeyboardShortcutsContent";
import { MobileDevicesSection } from "./settings/MobileDevicesSection";
import { ReportProblemContent } from "./settings/ReportProblemContent";
import { SettingsSection } from "./settings/SettingsSection";

const UpdatesSection = lazy(async () => {
	const module = await import("./settings/UpdatesSection");
	return { default: module.UpdatesSection };
});

export type GlobalSettingsSection = GlobalSettingsPage | "all";

/** Full-width panel for page-level content (forms, editors) — matches the
 *  grouped-row surface so pages read as one coherent family. */
function SettingsContentPanel({ children }: { children: React.ReactNode }) {
	return <div className="rounded-md bg-[var(--color-bg-settings-row)] px-4 py-4">{children}</div>;
}

export function GlobalSettingsForm({
	section = "all",
}: {
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
			{(all || section === "general") && (
				<>
					<SettingsSection title={t("settings.agentDefaults")} titleHidden={titleHidden}>
						<button
							type="button"
							className="w-full rounded-md bg-[var(--color-bg-settings-row)] px-4 py-3 text-left"
							onClick={() => setAgentDefaultsOpen(true)}
						>
							{t("settings.agentDefaults")}
						</button>
					</SettingsSection>
					<GeneralSettingsSection titleHidden={titleHidden} />
				</>
			)}

			{(all || section === "cloud") && <CloudCredentialsSection titleHidden={titleHidden} />}

			{(all || section === "mobile") && (
				<SettingsSection title={t("settings.mobile")} titleHidden={titleHidden}>
					<div className="rounded-md bg-[var(--color-bg-settings-row)] px-4 pb-4 pt-0">
						<ConnectMobileContent active />
						<MobileDevicesSection />
					</div>
				</SettingsSection>
			)}

			{(all || section === "shortcuts") && (
				<SettingsSection title={t("settings.keyboardShortcuts")} titleHidden={titleHidden}>
					<SettingsContentPanel>
						<KeyboardShortcutsContent active />
					</SettingsContentPanel>
				</SettingsSection>
			)}

			{(all || section === "updates") && (
				<Suspense fallback={null}>
					<UpdatesSection titleHidden={titleHidden} />
				</Suspense>
			)}

			{(all || section === "help") && (
				<SettingsSection title={t("settings.reportProblem")} titleHidden={titleHidden}>
					<SettingsContentPanel>
						<ReportProblemContent active />
					</SettingsContentPanel>
				</SettingsSection>
			)}
		</div>
		<PromptOverrideDialog open={agentDefaultsOpen} onOpenChange={setAgentDefaultsOpen} scope="user" />
		</>
	);
}
