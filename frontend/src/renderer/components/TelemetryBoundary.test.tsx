import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TelemetryBoundary } from "./TelemetryBoundary";

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

function ThrowingChild(): never {
	throw new Error("render-boom");
}

describe("TelemetryBoundary", () => {
	beforeEach(() => {
		bridgeMocks.appGetVersion.mockResolvedValue("9.9.9-test");
		bridgeMocks.clipboardWriteText.mockResolvedValue(undefined);
	});
	afterEach(() => {
		vi.restoreAllMocks();
		vi.clearAllMocks();
	});

	it("shows the error details and reports locally when a child render throws", async () => {
		vi.spyOn(console, "error").mockImplementation(() => undefined);
		render(
			<TelemetryBoundary>
				<ThrowingChild />
			</TelemetryBoundary>,
		);
		expect(await screen.findByRole("heading", { name: "The app hit an unexpected error." })).toBeInTheDocument();
		expect(screen.getAllByText(/render-boom/).length).toBeGreaterThan(0);
		expect(await screen.findByText(/Version: 9\.9\.9-test/)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Copy details" })).toBeInTheDocument();
		expect(bridgeMocks.crashReport).toHaveBeenCalledWith(expect.objectContaining({ operation: "react_render", name: "Error", message: "render-boom" }));
		expect(screen.queryByRole("button", { name: "Reload" })).not.toBeInTheDocument();
	});

	it("renders its children when nothing fails", () => {
		render(
			<TelemetryBoundary>
				<span>app content</span>
			</TelemetryBoundary>,
		);
		expect(screen.getByText("app content")).toBeInTheDocument();
		expect(screen.queryByRole("heading", { name: "The app hit an unexpected error." })).not.toBeInTheDocument();
	});
});
