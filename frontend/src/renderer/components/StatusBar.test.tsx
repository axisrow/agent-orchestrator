import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { HostMemory, ProcessInventory, ProcessTreeRow } from "../hooks/useProcessInventoryQuery";
import { TooltipProvider } from "./ui/tooltip";
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

const host: HostMemory = {
	totalBytes: 25769803776, // 24 GB
	usedBytes: 21861388288, // 20.4 GB
	freeBytes: 3908431872,
	cachedBytes: 4294967296,
	wiredBytes: 3732488192,
	appBytes: 16106127360,
	compressedBytes: 2013265920,
	swapTotalBytes: 8589934592, // 8 GB
	swapUsedBytes: 7411984384, // ~6.9 GB
	swapFreeBytes: 1177948160,
	swapMaxBytes: 42949672960,
	pressureFreePercent: 31,
};

const ownedTree: ProcessTreeRow = {
	sessionId: "web-api-3",
	rootPid: 90111,
	rootLstart: "Tue Sep 15 19:06:11 2026",
	pidCount: 3,
	rssBytes: 851443,
	kind: "worker",
	state: "owned",
	attached: true,
};

const orphanTree: ProcessTreeRow = {
	sessionId: "legacy-x",
	rootPid: 8123,
	rootLstart: "Mon Sep 14 00:13:02 2026",
	pidCount: 3,
	rssBytes: 1153433600, // ~1.1 GB
	kind: "worker",
	state: "orphan",
	attached: false,
};

function inventory(overrides: Partial<ProcessInventory> = {}): ProcessInventory {
	return {
		generatedAt: "2026-09-17T00:00:00Z",
		daemon: { pid: 4001, present: true, rssBytes: 94371840 },
		tmux: { pid: 4055, present: true, rssBytes: 46037504 },
		trees: [ownedTree, orphanTree],
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
		host,
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
			<TooltipProvider>
				<StatusBar />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("StatusBar", () => {
	it("renders compact host memory segments with binary-formatted sizes", () => {
		renderStatusBar(inventory());
		expect(screen.getByTestId("status-bar-memory")).toHaveTextContent("20.4 GB / 24 GB");
		expect(screen.getByTestId("status-bar-memory")).toHaveTextContent("6.9 GB / 8 GB");
		// The bar is minimal: no sigma total, no AO/tmux/sessions byte segments.
		expect(screen.queryByText(/Σ/)).toBeNull();
		expect(screen.queryByText("90 MB")).toBeNull();
		expect(screen.queryByText("sessions")).toBeNull();
	});

	it("shows the orphan badge and kill entry when orphans exist", () => {
		renderStatusBar(inventory());
		expect(screen.getByText("orphans")).toBeInTheDocument();
		expect(screen.getByTestId("status-bar-kill")).toBeInTheDocument();
	});

	it("renders nothing when the inventory has no host section and no orphans", () => {
		const clean = inventory({ host: undefined });
		clean.trees = [ownedTree];
		clean.totals.orphansCount = 0;
		renderStatusBar(clean);
		expect(screen.queryByTestId("status-bar")).toBeNull();
	});

	it("opens the popover and kills a single orphan tree with one exact target", async () => {
		const user = userEvent.setup();
		postMock.mockResolvedValue({
			data: { results: [{ sessionId: "legacy-x", rootPid: 8123, status: "killed" }] },
			error: undefined,
		});
		renderStatusBar(inventory());

		await user.click(screen.getByTestId("status-bar-memory"));
		expect(screen.getByText("Host memory")).toBeInTheDocument();
		expect(screen.getByText("AO process trees")).toBeInTheDocument();

		const orphanRow = screen.getByText("legacy-x · 8123 · 1.1 GB");
		expect(orphanRow).toBeInTheDocument();
		// The owned tree is listed read-only — no kill button on its row.
		expect(screen.getByText("web-api-3 · 90111 · 831 KB")).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Kill orphan legacy-x" }));
		await waitFor(() => {
			expect(postMock).toHaveBeenCalledWith("/api/v1/system/processes/kill", {
				body: {
					targets: [{ sessionId: "legacy-x", rootPid: 8123, rootLstart: "Mon Sep 14 00:13:02 2026" }],
				},
			});
		});
	});

	it("routes the batch kill through the confirm dialog", async () => {
		const user = userEvent.setup();
		postMock.mockResolvedValue({
			data: { results: [{ sessionId: "legacy-x", rootPid: 8123, status: "killed" }] },
			error: undefined,
		});
		renderStatusBar(inventory());

		await user.click(screen.getByTestId("status-bar-kill"));
		expect(screen.getByText("Kill orphaned process trees?")).toBeInTheDocument();
		expect(screen.getByText("legacy-x · pid 8123 · 1.1 GB")).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Kill trees" }));
		await waitFor(() => {
			expect(postMock).toHaveBeenCalledWith("/api/v1/system/processes/kill", {
				body: {
					targets: [{ sessionId: "legacy-x", rootPid: 8123, rootLstart: "Mon Sep 14 00:13:02 2026" }],
				},
			});
		});
	});
});
