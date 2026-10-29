import { expect, test } from "@playwright/test";
import { installFakeAgent, installFakeBridge } from "./support/fake-bridge";

// INS/DMN/BRD/SET RENDERER SMOKE (issue #2483, renderer slice).
//
// Scope — read this before trusting a green run. These run under `dev:web`
// (VITE_NO_ELECTRON=1) with an injected `window.ao` (installFakeBridge /
// installFakeAgent) plus a fake CDC/SSE stream and workspace snapshot. They
// assert the renderer's rendering + interaction logic ONLY. They do NOT exercise
// the real daemon, storage, API, preload, PTY, or filesystem — those boundaries
// are faked, so a daemon/storage/API/preload/PTY/FS regression can still pass
// here.
//
// Cases that inject state (a version string, a daemon status, board data) prove
// the renderer RENDERS that state, not that the daemon PRODUCES it. The real
// boundaries (daemon, storage, API, preload, PTY, FS) are exercised only in the
// packaged-app pod gate (#2697) — which today runs a boot-level smoke (app
// launches + paints, daemon reaches ready), NOT these specific cases; per-case
// runtime coverage in the pod is future work. Each test carries its #2483
// catalog ID in a comment for traceability; this is renderer smoke, NOT the
// canonical T0/P0 gate.

// ── INS: install / first run ────────────────────────────────────────────────

// #2483 INS-001.
test("renderer: packaged bundle launches and paints @T0 @INS", async ({ page }) => {
	// The real "deb/zip installs cleanly on the reference image" check is a pod
	// packaging step with no renderer surface. The renderer-observable proof that
	// the install produced a runnable app is that the bundle loads, the shell
	// paints, and the app carries a real version string (bundle integrity). The
	// on-image install itself stays in the pod INS script.
	await installFakeBridge(page, { version: "9.9.9-test" });
	await page.goto("/");
	await expect(page.getByText("Jump back right in")).toBeVisible();
	await page.goto("/#/settings");
	await expect(page.getByTestId("settings-page")).toBeVisible();
	await page.getByRole("button", { name: "Updates" }).click();
	await expect(page.getByTestId("app-version")).toContainText(/v\d+\.\d+\.\d+/);
});

// #2483 INS-007.
test("renderer: update settings surface renders (feed/checksum checks are pod) @T0 @INS", async ({ page }) => {
	// INS-007 (updater feed ymls reference real uploaded assets with matching
	// checksums) is a release-artifact check with no renderer surface — it belongs
	// to the pod/CI updater leg. The renderer slice we can lock is that the update
	// settings surface (channel + version) renders, i.e. the app is wired to a
	// feed at all. Checksum + asset-existence verification stays in the pod.
	await installFakeBridge(page, { version: "9.9.9-test" });
	await page.goto("/#/settings");
	await expect(page.getByTestId("settings-page")).toBeVisible();
	await page.getByRole("button", { name: "Updates" }).click();
	await expect(page.locator('[data-testid="settings-section"][data-section="updates"]')).toBeVisible();
	await expect(page.getByTestId("app-version")).toContainText("v9.9.9-test");
});

// #2483 INS-002.
test("renderer: first-run home renders with the app launched @T0 @INS", async ({ page }) => {
	// "Empty data dir" is a pod-side precondition; under dev:web the mock
	// fixtures are always present, so the BoardWelcome empty state can't render
	// and we assert the home surface + a mounted daemon-status indicator
	// (proof the shell booted). The empty-state testid (`board-welcome`) is wired
	// for the real empty-dir pod run.
	await page.goto("/");
	await expect(page.getByText("Jump back right in")).toBeVisible();
	await expect(page.getByTestId("daemon-status")).toBeAttached();
	await expect(page.getByText("Projects", { exact: true })).toBeVisible();
});

// #2483 INS-003.
test("renderer: reflects a ready daemon (data dir + config initialized) @T0 @INS", async ({ page }) => {
	// Renderer proxy: reaching "ready" with a REST port means the daemon
	// initialized its data dir + config skeleton (a not-ready daemon never
	// advertises a port). Asserting the on-disk ~/.ao layout itself belongs to
	// the backend/daemon suite, not this renderer harness — see report.
	await installFakeBridge(page, { daemonState: "ready", daemonPort: 8080 });
	await page.goto("/");
	await expect(page.getByTestId("daemon-status")).toHaveAttribute("data-state", "ready");
});

// #2483 INS-004.
test("renderer: app version string renders as expected @T0 @INS", async ({ page }) => {
	await installFakeBridge(page, { version: "9.9.9-test" });
	await page.goto("/#/settings");
	await expect(page.getByTestId("settings-page")).toBeVisible();
	await page.getByRole("button", { name: "Updates" }).click();
	await expect(page.getByTestId("app-version")).toContainText("v9.9.9-test");
});

// ── DMN: daemon lifecycle / health ──────────────────────────────────────────

// #2483 DMN-001.
test("renderer: reflects the daemon reaching ready on app start @T0 @DMN", async ({ page }) => {
	await installFakeBridge(page, { daemonState: "ready", daemonPort: 8080 });
	await page.goto("/");
	const status = page.getByTestId("daemon-status");
	await expect(status).toHaveAttribute("data-state", "ready");
	await expect(status).toContainText("ready");
});

// #2483 DMN-002.
test("renderer: daemon health reflected with a hydrated workspace @T0 @DMN", async ({ page }) => {
	// A responsive daemon → the renderer is ready AND has data to paint: the
	// project route redirects to a real session rather than an error/empty
	// shell.
	//
	// Use installFakeAgent so the session is served through the
	// window.__aoFakeAgent.snapshot() workspace seam (the daemon-backed source),
	// not the static mockWorkspaces fallback — otherwise the session would
	// render regardless of the daemon and the "daemon → has data" link would be
	// a false green.
	await installFakeAgent(page, {
		daemonPort: 8080,
		workers: [{ id: "dmn002", title: "Active worker", status: "working" }],
	});
	await page.goto("/#/projects/fake-proj");
	await expect(page.getByTestId("daemon-status")).toHaveAttribute("data-state", "ready");
	await expect(page.getByTestId("session-detail")).toBeVisible();
});

// #2483 DMN-005.
test("renderer: daemon stop surfaced cleanly with no renderer crash @T0 @DMN", async ({ page }) => {
	// The real DMN-005 ("graceful quit stops the daemon, no orphan processes") is
	// a process-tree assertion for the pod. The renderer slice: a stopped daemon
	// is surfaced as a stopped status and the app stays alive (no crash/blank),
	// which is the visible half of a clean shutdown.
	await installFakeBridge(page, { daemonState: "stopped" });
	await page.goto("/");
	await expect(page.getByTestId("daemon-status")).toHaveAttribute("data-state", "stopped");
	// The shell still paints its navigation — the app did not blank out.
	await expect(page.locator('[data-sidebar="menu-button"]').first()).toBeVisible();
});

// #2483 DMN-009.
test("renderer: session state rehydrates after a renderer relaunch @T0 @DMN", async ({ page }) => {
	// The real DMN-009 ("create state, restart the daemon, all state survives") is
	// a daemon/storage persistence check for the pod. The renderer slice we can
	// lock: the landed session rehydrates after a full renderer relaunch
	// (reload), i.e. the app rebuilds from the daemon rather than in-memory state.
	//
	// Use installFakeAgent (not installFakeBridge): its data is read through
	// the `window.__aoFakeAgent.snapshot()` workspace seam — the same source the
	// real daemon fills — so the reload genuinely re-reads from the daemon-backed
	// source. installFakeBridge alone would fall back to the static mockWorkspaces
	// import, and the reload would pass by re-reading the same mock (false green).
	await installFakeAgent(page, {
		daemonPort: 8080,
		workers: [{ id: "dmn009", title: "Persisted worker", status: "working" }],
	});
	await page.goto("/#/projects/fake-proj");
	await expect(page).toHaveURL(/sessions\/dmn009/);
	await expect(page.getByTestId("session-detail")).toBeVisible();

	await page.reload();
	await expect(page.getByTestId("daemon-status")).toHaveAttribute("data-state", "ready");
	await expect(page).toHaveURL(/sessions\/dmn009/);
	await expect(page.getByTestId("session-detail")).toBeVisible();
});

// ── SET: settings ────────────────────────────────────────────────────────────

// #2483 SET-001.
test("renderer: global settings page renders all sections @T0 @SET", async ({ page }) => {
	// The settings revamp (#2797) reduced the page to General + Updates + Help;
	// Help now renders the report form inline rather than opening another dialog.
	await page.goto("/#/settings");
	await expect(page.getByTestId("settings-page")).toBeVisible();
	await page.getByRole("button", { name: "Updates" }).click();
	await expect(page.locator('[data-testid="settings-section"][data-section="updates"]')).toBeVisible();
	await page.getByRole("button", { name: "Help" }).click();
	await expect(page.getByLabel("Title")).toBeVisible();
});
