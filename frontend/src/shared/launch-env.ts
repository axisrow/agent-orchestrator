// Opt-in launch flags that cross from the Electron main process environment to
// the sandboxed renderer. A sandboxed preload cannot read process.env, so main
// turns each supported AO_* variable into an explicit additionalArguments entry
// (visible to the preload as process.argv) and the renderer reads the parsed
// value off the bridge. Shared so the argv literal has one definition.

/** AO_RIGHT_PANEL=expanded (or =1): open the session inspector rail expanded by default. */
export const RIGHT_PANEL_EXPANDED_ARG = "--ao-right-panel=expanded";

export function rightPanelExpandedFromEnv(env: Record<string, string | undefined>): boolean {
	const value = env.AO_RIGHT_PANEL;
	return value === "expanded" || value === "1";
}

/** Main process: the additionalArguments list to hand the shell WebContentsView. */
export function rightPanelPreloadArgs(env: Record<string, string | undefined>): string[] {
	return rightPanelExpandedFromEnv(env) ? [RIGHT_PANEL_EXPANDED_ARG] : [];
}

/** Preload: whether the main process passed the flag through webPreferences. */
export function rightPanelExpandedFromArgv(argv: readonly string[]): boolean {
	return argv.includes(RIGHT_PANEL_EXPANDED_ARG);
}

/** AO_LINKS_EXTERNAL=0: use the AO browser; otherwise open web links in the system browser by default; the AO Browser panel stays on the context menu. */
export const LINKS_EXTERNAL_ARG = "--ao-links-external";

export function linksExternalFromEnv(env: Record<string, string | undefined>): boolean {
	return env.AO_LINKS_EXTERNAL !== "0";
}

/** Main process: the additionalArguments list to hand the shell WebContentsView. */
export function linksExternalPreloadArgs(env: Record<string, string | undefined>): string[] {
	return linksExternalFromEnv(env) ? [LINKS_EXTERNAL_ARG] : [];
}

/** Preload: whether the main process passed the flag through webPreferences. */
export function linksExternalFromArgv(argv: readonly string[]): boolean {
	return argv.includes(LINKS_EXTERNAL_ARG);
}
