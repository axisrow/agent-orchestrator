import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";
import { openInspector } from "./support/open-inspector";

// dev:web (VITE_NO_ELECTRON=1) lab for the merge-readiness guard error state
// (issue #306). The fake bridge advertises a ready daemon on port 8080 so the
// renderer trusts that API base, and every /api call is fulfilled here —
// nothing reaches a daemon. The session PR summary carries a readinessCheck
// block: the wire shape the SCM observer produces once its readiness pipeline
// (commit check-runs guard + PR detail fetch) fails repeatedly. The card must
// show an explicit error state with the provider reason and the retry
// schedule, never an endless silent "Checking", while the healthy mergeable
// card next door stays exactly as today.

const sessionId = "demo-readiness-failed";
const apiBase = "http://127.0.0.1:8080";

const now = new Date().toISOString();

const failingPr = {
	url: "https://github.com/Untrivial-ai/agent-orchestrator/pull/977",
	htmlUrl: "https://github.com/Untrivial-ai/agent-orchestrator/pull/977",
	number: 977,
	title: "Merge the readiness guard retry fix",
	state: "open",
	provider: "github",
	repo: "Untrivial-ai/agent-orchestrator",
	author: "octocat",
	sourceBranch: "ao/readiness-guard/root",
	targetBranch: "main",
	headSha: "preview-977",
	additions: 42,
	deletions: 8,
	changedFiles: 3,
	ci: { state: "unknown", failingChecks: [], autoInjectCI: true },
	review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] },
	mergeability: { state: "unknown", reasons: [], prUrl: "https://github.com/Untrivial-ai/agent-orchestrator/pull/977" },
	updatedAt: now,
	observedAt: now,
	ciObservedAt: now,
	reviewObservedAt: now,
	readinessCheck: {
		attempts: 3,
		lastError: "context deadline exceeded (Client.Timeout exceeded while awaiting headers)",
		lastFailedAt: new Date(Date.now() - 30_000).toISOString(),
		nextRetryAt: new Date(Date.now() + 45_000).toISOString(),
	},
};

test("a repeatedly failing readiness pipeline shows an explicit error state with the retry schedule @T0", async ({ page }) => {
	await page.emulateMedia({ reducedMotion: "reduce" });
	await installFakeBridge(page, { daemonState: "ready", daemonPort: 8080 });
	await page.route(`${apiBase}/api/v1/**`, async (route) => {
		const pathname = new URL(route.request().url()).pathname;
		if (pathname === `/api/v1/sessions/${sessionId}/pr`) {
			await route.fulfill({ json: { sessionId, prs: [failingPr], linkedPrs: [] } });
			return;
		}
		// Everything else stays a plain 404 so hooks take their ordinary
		// error path; a fake success payload would crash array consumers.
		await route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND", message: "not mocked in this lab" } } });
	});

	await page.goto(`/#/projects/ao-demo/sessions/${sessionId}`);

	const inspector = await openInspector(page);
	const failingCard = inspector.locator("article").filter({ hasText: "PR #977" });
	await expect(failingCard).toBeVisible();

	// The failing card states the failure, the provider reason, and the retry
	// schedule — it must not read as an endless silent "Checking". Both the CI
	// status row and the readiness strip carry the error, hence .first().
	await expect(failingCard.getByText("Readiness check failed").first()).toBeVisible();
	await expect(failingCard.getByText(/context deadline exceeded/).first()).toBeVisible();
	await expect(failingCard.getByText(/Next retry in \d+s/).first()).toBeVisible();
	await expect(failingCard.getByText("Checking merge readiness")).toHaveCount(0);

	await inspector.screenshot({ path: "test-results/pr-readiness-check-failure.png" });
});

test("the healthy mergeable card stays free of readiness-check errors @P0", async ({ page }) => {
	await page.emulateMedia({ reducedMotion: "reduce" });
	await installFakeBridge(page, { daemonState: "ready", daemonPort: 8080 });
	await page.route(`${apiBase}/api/v1/**`, async (route) => {
		await route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND", message: "not mocked in this lab" } } });
	});
	await page.goto("/#/projects/ao-demo/sessions/demo-ready");

	const inspector = await openInspector(page);
	const healthyCard = inspector.locator("article").filter({ hasText: "PR #323" });
	await expect(healthyCard).toBeVisible();
	// The readiness strip keeps today's healthy verdict.
	await expect(healthyCard.getByText("Mergeable", { exact: true })).toBeVisible();
	await expect(healthyCard.getByText("Readiness check failed")).toHaveCount(0);
});
