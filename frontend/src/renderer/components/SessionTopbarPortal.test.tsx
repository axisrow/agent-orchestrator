import { act, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { NativeModePicker } from "./NativeModePicker";
import { SessionTopbarHost, SessionTopbarPortal, SessionTopbarProvider } from "./SessionTopbarPortal";

describe("SessionTopbarPortal", () => {
	it("renders session chrome in the shell-owned host", () => {
		render(
			<SessionTopbarProvider>
				<SessionTopbarHost data-testid="session-topbar-host" />
				<SessionTopbarPortal>
					<span>terminal tabs</span>
				</SessionTopbarPortal>
			</SessionTopbarProvider>,
		);

		const host = screen.getByTestId("session-topbar-host");
		expect(host.classList.contains("workspace-topbar-container")).toBe(true);
		expect(within(host).getByText("terminal tabs")).not.toBeNull();
	});

	it("renders inline when a component test has no shell provider", () => {
		render(
			<SessionTopbarPortal>
				<span>terminal tabs</span>
			</SessionTopbarPortal>,
		);

		expect(screen.getByText("terminal tabs")).toBeInTheDocument();
	});

	// #346: a long-running in-flight tool call keeps session facts streaming, so
	// session_detail re-renders continuously. The host used to inline a fresh
	// ref callback per render: every commit detached it with setHost(null) and
	// reattached with setHost(node) — the commit-phase dispatch storm that
	// chained past React's update-depth limit and crashed the surface with
	// React error #185 (thrown from the ref detach inside
	// commitMutationEffects). The ref callback must therefore attach at mount
	// and detach at unmount only, whatever the fact stream does.
	it("survives live session fact updates with the model picker mounted without exceeding update depth", () => {
		const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
		const refCalls: Array<HTMLDivElement | null> = [];

		function SessionDetailTopbar({ facts }: { facts: number }) {
			return (
				<SessionTopbarProvider>
					<SessionTopbarHost
						data-testid="session-topbar-host"
						ref={(element) => {
							refCalls.push(element);
						}}
					/>
					<SessionTopbarPortal>
						<NativeModePicker
							models={[
								{ id: "claude-opus-4-6", label: `Opus fact ${facts}` },
								{ id: "claude-haiku-4-6", label: "Haiku" },
							]}
							mode="claude-opus-4-6"
							triggerClassName="trigger"
							onModeChange={() => {}}
						/>
					</SessionTopbarPortal>
				</SessionTopbarProvider>
			);
		}

		const view = render(<SessionDetailTopbar facts={0} />);
		expect(screen.getByTitle("Opus fact 0")).toBeInTheDocument();

		for (let facts = 1; facts <= 60; facts += 1) {
			act(() => {
				view.rerender(<SessionDetailTopbar facts={facts} />);
			});
		}

		// The portal content is still mounted and shows the latest fact-driven label.
		expect(screen.getByTitle("Opus fact 60")).toBeInTheDocument();
		// Mount attached once; no fact update detached the host afterwards. The
		// null entries are the churn dispatches that crossed React's depth limit
		// in production.
		expect(refCalls.filter((element) => element === null)).toEqual([]);
		expect(refCalls).toHaveLength(1);
		const depthErrors = consoleError.mock.calls.filter((call) => String(call[0]).includes("Maximum update depth"));
		expect(depthErrors).toEqual([]);
		consoleError.mockRestore();
	});
});
