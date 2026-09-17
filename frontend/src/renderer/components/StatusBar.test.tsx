import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ProcessInventory } from "../hooks/useProcessInventoryQuery";
import { StatusBar } from "./StatusBar";

const { getMock, postMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: getMock,
		POST: postMock,
	},
}));

vi.mock("motion/react", () => ({
	AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
	motion: {},
	useReducedMotion: () => true,
}));

const orphanTree = {
	sessionId: "legacy-x",
	rootPid: 8123,
	rootLstart: "Mon Sep 14 00:13:02 2026",
	pidCount: 3,
	rssBytes: 1153433600,
	kind: "worker",
	state: "orphan",
	attached: false,
};

const liveTree = {
	sessionId: "web-api-3",
	rootPid: 90111,
	rootLstart: "Tue Sep 15 19:06:11 2026",
	pidCount: 3,
	rssBytes: 851443, // ~831 KB
	kind: "worker",
	state: "owned",
	attached: true,
};

function inventory(overrides: Partial<ProcessInventory> = {}): ProcessInventory {
	return {
		generatedAt: "2026-09-17T00:00:00Z",
		daemon: { pid: 4001, present: true, rssBytes: 94371840 }, // 90 MB
		tmux: { pid: 4055, present: true, rssBytes: 46037504 }, // ~44 MB
		trees: [liveTree, orphanTree],
		remnants: [],
		totals: {
			sessionsCount: 1,
			sessionsRssBytes: 851443,
			orphansCount: 1,
			orphansRssBytes: orphanTree.rssBytes,
			foreignCount: 0,
			foreignRssBytes: 0,
			daemonRssBytes: 94371840,
			tmuxRssBytes: 46037504,
		},
		...overrides,
	} as ProcessInventory;
}

function renderStatusBar(inventoryData: ProcessInventory | null) {
	const client = new QueryClient({
		defaultOptions: { queries: { retry: false } },
	});
	if (inventoryData) {
		client.setQueryData(["process-inventory"], inventoryData);
	}
	return render(
		<QueryClientProvider client={client}>
			<StatusBar />
		</QueryClientProvider>,
	);
}

describe("StatusBar", () => {
	it("renders the footprint segments with binary-formatted sizes", async () => {
		renderStatusBar(inventory());
		expect(screen.getByTestId("status-bar")).toBeInTheDocument();
		expect(screen.getByText("90 MB")).toBeInTheDocument();
		expect(screen.getByText("sessions")).toBeInTheDocument();
		expect(screen.getByText("orphans")).toBeInTheDocument();
		expect(screen.getByText("Σ 1.2 GB")).toBeInTheDocument(); // full AO footprint incl. orphans
	});

	it("renders nothing while the inventory is unavailable", () => {
		getMock.mockResolvedValue({ data: null, error: { message: "scan failed" } });
		const { container } = renderStatusBar(null);
		expect(container.querySelector('[data-testid="status-bar"]')).toBeNull();
	});

	it("hides the kill flow when there are no orphans", () => {
		const clean = inventory();
		clean.trees = [liveTree];
		clean.totals.orphansCount = 0;
		clean.totals.orphansRssBytes = 0;
		renderStatusBar(clean);
		expect(screen.queryByTestId("status-bar-kill")).toBeNull();
	});

	it("lists the orphan trees in the confirm dialog and posts them on confirm", async () => {
		const user = userEvent.setup();
		postMock.mockResolvedValue({
			data: { results: [{ sessionId: "legacy-x", rootPid: 8123, status: "killed" }] },
			error: undefined,
		});
		renderStatusBar(inventory());

		await user.click(screen.getByTestId("status-bar-kill"));
		expect(screen.getByText("legacy-x · pid 8123 · 1.1 GB")).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Kill trees" }));
		await waitFor(() => {
			expect(postMock).toHaveBeenCalledWith("/api/v1/system/processes/kill", {
				body: { targets: [{ sessionId: "legacy-x", rootPid: 8123, rootLstart: "Mon Sep 14 00:13:02 2026" }] },
			});
		});
	});

	it("keeps the dialog open with an error when the kill fails", async () => {
		const user = userEvent.setup();
		postMock.mockResolvedValue({ data: undefined, error: { code: "PROCESS_SCAN_FAILED" } });
		renderStatusBar(inventory());

		await user.click(screen.getByTestId("status-bar-kill"));
		await user.click(screen.getByRole("button", { name: "Kill trees" }));
		await waitFor(() => {
			expect(screen.getByRole("alert")).toHaveTextContent("Killing failed. Try again.");
		});
		expect(screen.getByText("legacy-x · pid 8123 · 1.1 GB")).toBeInTheDocument();
	});
});
