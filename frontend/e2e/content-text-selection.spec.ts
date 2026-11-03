import { expect, test, type Locator, type Page } from "@playwright/test";
import { openInspector } from "./support/open-inspector";

// #4268: `body { user-select: none }` keeps app chrome inert; read-only
// content must opt back in so a drag selects it and Cmd/Ctrl+C copies it.

test.use({ permissions: ["clipboard-read", "clipboard-write"] });

async function dragAcross(page: Page, target: Locator) {
	await page.evaluate(() => {
		window.getSelection()?.removeAllRanges();
		return navigator.clipboard.writeText("<untouched>");
	});
	await target.hover(); // waits out the inspector's slide-in so the box is final
	const box = (await target.boundingBox())!;
	// Corner to corner, so text that wraps onto several lines is covered whole.
	await page.mouse.move(box.x + 1, box.y + 2);
	await page.mouse.down();
	await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 5 });
	await page.mouse.move(box.x + box.width - 1, box.y + box.height - 2, { steps: 5 });
	await page.mouse.up();
}

async function expectDragCopies(page: Page, target: Locator, text: string) {
	await dragAcross(page, target);
	expect(await page.evaluate(() => window.getSelection()?.toString().trim())).toBe(text);
	await page.keyboard.press("ControlOrMeta+c");
	expect((await page.evaluate(() => navigator.clipboard.readText())).trim()).toBe(text);
}

test("inspector details can be drag-selected and copied @T0", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-ci-failed");
	const inspector = await openInspector(page);
	// This fork's PR card shows merge readiness as a short status line instead
	// of upstream's merge-reason sentence; drag across the readiness text.
	const text = "Not mergeable yet";
	await expectDragCopies(page, inspector.getByText(text, { exact: true }), text);
});

test("Markdown file content and error copy can be drag-selected and copied @T0", async ({ page }) => {
	await page.goto("/e2e/text-selection/harness.html");
	await expectDragCopies(page, page.getByText("Run ao session ls to list sessions."), "Run ao session ls to list sessions.");
	await expectDragCopies(page, page.getByRole("alert"), "Push rejected: branch demo/new-task-flake is protected.");
});

test("dragging across a sidebar session row selects nothing and the row still opens on click @T0", async ({ page }) => {
	await page.goto("/#/projects/ao-demo");
	// The kanban board is gone in this fork: a project lands on its live
	// orchestrator (#260), so the row dragged below is not the active session.
	// The row is app chrome — a drag across it must select nothing, and the
	// row must still open its session. The drag itself is a sort gesture, not
	// a click, so the open happens on the row's click.
	const row = page.getByRole("button", { name: "Open Build screenshot-ready dashboard data" });
	await expect(row).toBeVisible();
	expect(await row.evaluate((el) => getComputedStyle(el).userSelect)).toBe("none");
	await dragAcross(page, row);
	expect(await page.evaluate(() => window.getSelection()?.toString())).toBe("");
	await row.click();
	await expect(page).toHaveURL(/\/sessions\/demo-working$/);
});
