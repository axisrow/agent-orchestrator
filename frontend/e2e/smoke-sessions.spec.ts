import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

// SES-* RENDERER SMOKE (issue #2483, renderer slice).
//
// Scope: this runs under `dev:web` (VITE_NO_ELECTRON=1) with an injected
// `window.ao` + a fake CDC/SSE stream + an in-page workspace snapshot. It
// exercises the renderer's SSE → invalidate → refetch path only — NOT the real
// daemon, storage, API, preload, PTY, or filesystem. Those boundaries are
// exercised only in the packaged-app pod gate (#2697), which today runs a
// boot-level smoke (app launches, daemon ready), NOT these cases — per-case pod
// coverage is future work. The case IDs cross-reference the #2483 catalog; they
// are not a claim of full-boundary coverage, and this suite is not the canonical
// T0/P0 gate.

const row = (page: import("@playwright/test").Page, title: string) =>
	page.locator("[data-session-row]").filter({ hasText: title });

// #2483 SES-002.
test("renderer: new session row appears in the sidebar without a manual refresh @T0 @SES", async ({ page }) => {
	// The board is gone (#163): a freshly spawned session surfaces in the
	// sidebar's session rows; the row must not exist until the fake agent
	// creates it.
	await installFakeAgent(page);
	await page.goto("/#/projects/fake-proj");
	const spawnRow = row(page, "Spawning worker");
	await expect(spawnRow).toHaveCount(0);

	await page.evaluate(() =>
		window.__aoFakeAgent!.createWorker({ id: "fake-spawn", title: "Spawning worker", activity: "exited" }),
	);

	await expect(spawnRow).toBeVisible();
});
