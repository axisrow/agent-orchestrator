import { describe, expect, it } from "vitest";
import { RIGHT_PANEL_EXPANDED_ARG, rightPanelExpandedFromArgv, rightPanelExpandedFromEnv, rightPanelPreloadArgs } from "./launch-env";

describe("AO_RIGHT_PANEL", () => {
	it("enables the expanded default only for the documented values", () => {
		expect(rightPanelExpandedFromEnv({})).toBe(false);
		expect(rightPanelExpandedFromEnv({ AO_RIGHT_PANEL: "" })).toBe(false);
		expect(rightPanelExpandedFromEnv({ AO_RIGHT_PANEL: "collapsed" })).toBe(false);
		expect(rightPanelExpandedFromEnv({ AO_RIGHT_PANEL: "expanded" })).toBe(true);
		expect(rightPanelExpandedFromEnv({ AO_RIGHT_PANEL: "1" })).toBe(true);
	});

	it("maps the env value to the preload argv flag", () => {
		expect(rightPanelPreloadArgs({})).toEqual([]);
		expect(rightPanelPreloadArgs({ AO_RIGHT_PANEL: "expanded" })).toEqual([RIGHT_PANEL_EXPANDED_ARG]);
	});

	it("detects the flag on the preload argv", () => {
		expect(rightPanelExpandedFromArgv(["/Applications/AO.app/Contents/MacOS/AO"])).toBe(false);
		expect(rightPanelExpandedFromArgv(["/Applications/AO.app/Contents/MacOS/AO", RIGHT_PANEL_EXPANDED_ARG])).toBe(true);
	});
});
