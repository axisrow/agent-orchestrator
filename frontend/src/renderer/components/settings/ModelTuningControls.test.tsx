import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { components } from "../../../api/schema";
import { ModelTuningControls } from "./ModelTuningControls";

type Model = components["schemas"]["AgentModelInfo"];

const models: Model[] = [
	{
		id: "capable",
		label: "Capable",
		isDefault: true,
		efforts: ["default", "low", "high"],
		defaultEffort: "low",
	},
	{ id: "plain", label: "Plain", efforts: ["low"] },
	{ id: "gateway", label: "Gateway", efforts: [] },
];

describe("ModelTuningControls", () => {
	it("shows the reported effort without saving it until selected", async () => {
		const onEffortChange = vi.fn();
		render(
			<ModelTuningControls
				models={models}
				model="capable"
				effort=""
				onEffortChange={onEffortChange}
				variant="settings"
				roleLabel="Worker"
			/>,
		);

		const effort = screen.getByRole("button", { name: "Worker Effort" });
		expect(effort).toHaveTextContent("low");
		expect(onEffortChange).not.toHaveBeenCalled();
		await userEvent.click(effort);
		expect(screen.queryByRole("menuitem", { name: "Provider default" })).not.toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "default" })).not.toBeInTheDocument();
		await userEvent.click(await screen.findByRole("menuitem", { name: "high" }));
		expect(onEffortChange).toHaveBeenCalledWith("high");
	});

	it("clears incompatible dependent selections when the model changes", () => {
		const onEffortChange = vi.fn();
		const view = render(
			<ModelTuningControls
				models={models}
				model="capable"
				effort="high"
				onEffortChange={onEffortChange}
				variant="composer"
			/>,
		);
		view.rerender(
			<ModelTuningControls
				models={models}
				model="plain"
				effort="high"
				onEffortChange={onEffortChange}
				variant="composer"
			/>,
		);

		expect(onEffortChange).toHaveBeenCalledWith("");
	});

	it("warns and marks unsupported saved values invalid until corrected", () => {
		const onValidityChange = vi.fn();
		render(
			<ModelTuningControls
				models={models}
				model="plain"
				effort="high"
				onEffortChange={vi.fn()}
				onValidityChange={onValidityChange}
				variant="settings"
				roleLabel="Reviewer"
			/>,
		);

		expect(screen.getByRole("alert")).toHaveTextContent("Reviewer model tuning is no longer supported");
		expect(onValidityChange).toHaveBeenCalledWith(false);
	});

	it("falls back to the common ladder when the provider reports no efforts", async () => {
		const onEffortChange = vi.fn();
		render(
			<ModelTuningControls
				models={models}
				model="gateway"
				effort=""
				onEffortChange={onEffortChange}
				variant="settings"
				roleLabel="Worker"
			/>,
		);

		await userEvent.click(screen.getByRole("button", { name: "Worker Effort" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "medium" }));
		expect(onEffortChange).toHaveBeenCalledWith("medium");
		expect(screen.getByText(/best guess/i)).toBeInTheDocument();
	});

	it("keeps a saved effort for a gateway model instead of flagging it invalid", () => {
		const onValidityChange = vi.fn();
		const onEffortReset = vi.fn();
		render(
			<ModelTuningControls
				models={models}
				model="gateway"
				effort="high"
				onEffortChange={vi.fn()}
				onEffortReset={onEffortReset}
				onValidityChange={onValidityChange}
				variant="settings"
			/>,
		);

		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(onValidityChange).toHaveBeenCalledWith(true);
		expect(onEffortReset).not.toHaveBeenCalled();
	});

	it("falls back to the ladder for an off-catalog model", async () => {
		const onEffortChange = vi.fn();
		render(
			<ModelTuningControls
				models={models}
				model="custom-off-catalog"
				effort=""
				onEffortChange={onEffortChange}
				variant="settings"
			/>,
		);

		await userEvent.click(screen.getByRole("button", { name: "Effort" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "low" }));
		expect(onEffortChange).toHaveBeenCalledWith("low");
	});
});
