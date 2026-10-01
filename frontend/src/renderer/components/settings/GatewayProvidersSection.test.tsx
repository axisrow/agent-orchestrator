import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "../../i18n";

const apiClient = vi.hoisted(() => ({
	GET: vi.fn(),
	PUT: vi.fn(),
	POST: vi.fn(),
}));
const apiErrorMessage = vi.hoisted(() => (error: unknown) => String(error));

vi.mock("../../lib/api-client", () => ({
	apiClient,
	apiErrorMessage,
}));

import { GatewayProvidersSection } from "./GatewayProvidersSection";

const appConfig = {
	app: { baseUrl: "https://gw.example.com", tokenSet: true, model: "glm-5" },
	effective: { baseUrl: "https://gw.example.com", model: "glm-5", source: "app" },
};

function renderSection(projectId?: string) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<GatewayProvidersSection projectId={projectId} />
		</QueryClientProvider>,
	);
}

describe("GatewayProvidersSection", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		apiClient.GET.mockResolvedValue({ data: appConfig, error: undefined });
	});

	it("shows the active gateway and the stored app entry", async () => {
		renderSection();
		await screen.findByDisplayValue("https://gw.example.com");
		expect(screen.getByText(/active gateway/i)).toHaveTextContent("https://gw.example.com");
		expect(screen.getByText(/a token is stored/i)).toBeInTheDocument();
		expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/settings/gateway", { params: undefined });
	});

	it("saves with scope app and only sends a token that was typed", async () => {
		apiClient.PUT.mockResolvedValue({ data: appConfig, error: undefined });
		const user = userEvent.setup();
		renderSection();
		await screen.findByDisplayValue("https://gw.example.com");
		const save = screen.getByRole("button", { name: "Save" });
		expect(save).toBeDisabled();
		await user.clear(screen.getByLabelText("Default model"));
		await user.type(screen.getByLabelText("Default model"), "glm-5-air");
		await user.click(save);
		await waitFor(() => expect(apiClient.PUT).toHaveBeenCalledTimes(1));
		const call = apiClient.PUT.mock.calls[0];
		expect(call[0]).toBe("/api/v1/settings/gateway");
		expect(call[1].body).toEqual({ scope: "app", baseUrl: "https://gw.example.com", model: "glm-5-air" });
	});

	it("reports a probe verdict and offers the gateway's models", async () => {
		apiClient.POST.mockResolvedValue({
			data: { state: "valid", models: [{ id: "glm-5", displayName: "GLM" }] },
			error: undefined,
		});
		const user = userEvent.setup();
		renderSection();
		await screen.findByDisplayValue("https://gw.example.com");
		await user.type(screen.getByLabelText("Auth token"), "typed-token");
		await user.click(screen.getByRole("button", { name: "Test connection" }));
		await screen.findByText(/works/i);
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/settings/gateway/probe", {
			body: { baseUrl: "https://gw.example.com", token: "typed-token" },
		});
		expect(document.querySelector('option[value="glm-5"]')).not.toBeNull();
	});

	it("renders the project override editor when a projectId is given", async () => {
		apiClient.GET.mockResolvedValue({
			data: {
				app: appConfig.app,
				project: { baseUrl: "https://project.example.com", tokenSet: false, model: "" },
				effective: { baseUrl: "https://project.example.com", source: "project" },
			},
			error: undefined,
		});
		renderSection("p1");
		await screen.findByDisplayValue("https://project.example.com");
		expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/settings/gateway", { params: { query: { projectId: "p1" } } });
	});
});
