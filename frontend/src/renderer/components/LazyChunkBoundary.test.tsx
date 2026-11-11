import { fireEvent, render, screen } from "@testing-library/react";
import { lazy, Suspense } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LazyChunkBoundary, LazyChunkErrorPanel } from "./LazyChunkBoundary";

const bridgeMocks = vi.hoisted(() => ({
	appGetVersion: vi.fn<() => Promise<string>>(),
	clipboardWriteText: vi.fn<(text: string) => Promise<void>>(),
	crashReport: vi.fn<(report: unknown) => void>(),
}));

vi.mock("../lib/telemetry", () => ({ captureRendererException: vi.fn(), routeSurface: () => "other" }));
vi.mock("../lib/bridge", () => ({
	aoBridge: {
		app: { getVersion: bridgeMocks.appGetVersion },
		clipboard: { writeText: bridgeMocks.clipboardWriteText },
		crash: { report: bridgeMocks.crashReport },
	},
}));

describe("LazyChunkBoundary", () => {
	beforeEach(() => {
		bridgeMocks.appGetVersion.mockResolvedValue("9.9.9-test");
		bridgeMocks.clipboardWriteText.mockResolvedValue(undefined);
	});
	afterEach(() => {
		vi.restoreAllMocks();
		vi.clearAllMocks();
	});

	it("contains a failed lazy import to its own panel", async () => {
		vi.spyOn(console, "error").mockImplementation(() => undefined);
		const Broken = lazy(() => Promise.reject(new Error("chunk failed to load")));
		render(
			<div>
				<span>sibling stays</span>
				<LazyChunkBoundary>
					<Suspense fallback={null}>
						<Broken />
					</Suspense>
				</LazyChunkBoundary>
			</div>,
		);
		expect(await screen.findByRole("alert")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
		expect(screen.getByText("sibling stays")).toBeInTheDocument();
		expect(screen.getAllByText(/chunk failed to load/).length).toBeGreaterThan(0);
		await vi.waitFor(() => expect(bridgeMocks.crashReport).toHaveBeenCalledWith(expect.objectContaining({ operation: "lazy_chunk" })));
	});

	it("shows the thrown error, version and surface next to the title", async () => {
		render(<LazyChunkErrorPanel error={new Error("boom-317")} operation="lazy_chunk" />);
		expect(screen.getAllByText(/boom-317/).length).toBeGreaterThan(0);
		expect(await screen.findByText(/Version: 9\.9\.9-test/)).toBeInTheDocument();
		expect(screen.getByText(/Surface: other/)).toBeInTheDocument();
		expect(screen.getByText("Details")).toBeInTheDocument();
	});

	it("copies a full report to the clipboard", async () => {
		render(<LazyChunkErrorPanel error={new Error("boom-copy")} operation="lazy_chunk" componentStack="    at div" />);
		await screen.findByText(/Version: 9\.9\.9-test/);
		fireEvent.click(screen.getByRole("button", { name: "Copy details" }));
		expect(await screen.findByRole("button", { name: "Copied" })).toBeInTheDocument();
		const copied = bridgeMocks.clipboardWriteText.mock.calls[0]?.[0] ?? "";
		expect(copied).toContain("boom-copy");
		expect(copied).toContain("9.9.9-test");
		expect(copied).toContain("lazy_chunk");
		expect(copied).toContain("Error: boom-copy");
	});

	it("offers to retry the panel before a full reload when it can reset", () => {
		const reset = vi.fn();
		render(<LazyChunkErrorPanel reset={reset} />);
		fireEvent.click(screen.getByRole("button", { name: "Try again" }));
		expect(reset).toHaveBeenCalledTimes(1);
		expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
	});

	it("does not offer a retry that cannot recover", () => {
		render(<LazyChunkErrorPanel />);
		expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
		expect(screen.queryByText("Details")).not.toBeInTheDocument();
	});

	it("renders its children when nothing fails", () => {
		render(
			<LazyChunkBoundary>
				<span>terminal</span>
			</LazyChunkBoundary>,
		);
		expect(screen.getByText("terminal")).toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});
});
