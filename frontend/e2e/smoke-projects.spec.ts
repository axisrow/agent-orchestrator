import { expect, test } from "@playwright/test";

// PRJ-* RENDERER SMOKE (issue #2483, renderer slice).
//
// Scope: runs under `dev:web` against lib/mock-data.ts fixtures. It verifies the
// renderer surfaces (sidebar row + board render) only — NOT project registration
// through the real daemon/filesystem. That boundary is exercised only in the
// packaged-app pod gate (#2697), which today runs a boot-level smoke (app
// launches, daemon ready), NOT this case — per-case pod coverage is future work.
// Case IDs cross-reference the #2483 catalog; not a claim of full-boundary
// coverage, and this suite is not the canonical T0/P0 gate.

// #2483 PRJ-005.
test("renderer: added project appears in the sidebar and board @T0 @PRJ", async ({ page }) => {
	// dev:web serves lib/mock-data.ts (ao-demo, docs-site). A registered project
	// must show as a sidebar row AND drive the board it opens.
	await page.goto("/#/");
	await expect(page.getByText("Projects", { exact: true })).toBeVisible();

	// Sidebar row for the project.
	const projectRow = page.locator('[data-sidebar="menu-button"]').filter({ hasText: "ao-demo" }).first();
	await expect(projectRow).toBeVisible();

	// Opening it lands on that project's top session (the board is gone, #163;
	// the project route redirects).
	await projectRow.click();
	await expect(page).toHaveURL(/sessions\//);
	await expect(page.getByTestId("session-detail")).toBeVisible();
});
