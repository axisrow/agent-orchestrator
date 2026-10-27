import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { SessionArtifactsView } from "./SessionArtifactsView";
import { TooltipProvider } from "./ui/tooltip";

const { deleteArtifactMock, openPreviewMock } = vi.hoisted(() => ({ deleteArtifactMock: vi.fn(), openPreviewMock: vi.fn() }));

vi.mock("../hooks/useOpenArtifactPreview", () => ({ useOpenArtifactPreview: () => openPreviewMock }));
vi.mock("../hooks/useSessionWorkspaceFiles", () => ({
	deleteSessionArtifactFile: deleteArtifactMock,
	sessionArtifactFileQueryOptions: (sessionId: string, path: string) => ({
		queryKey: ["artifact-file", sessionId, path],
		queryFn: async () => (path.endsWith(".png") ? { binary: true, content: "" } : { content: `contents of ${path}` }),
	}),
}));
vi.mock("./ArtifactFileView", () => ({
	ArtifactFileView: ({ feedbackRequestKey, path }: { feedbackRequestKey?: number; path: string }) => (
		<div data-feedback-key={feedbackRequestKey ?? ""} data-testid="artifact-view">{`viewer:${path}`}</div>
	),
}));

const artifacts: SessionArtifact[] = [
	{ kind: "markdown", name: "notes.md", path: "notes.md", size: 12, updatedAt: "2026-09-22T00:00:00Z" },
	{ kind: "html", name: "report.html", path: "report.html", previewUrl: "http://artifact.localhost/report.html", size: 20, updatedAt: "2026-09-23T00:00:00Z" },
	{ kind: "file", name: "plan.md", path: "reports/plan.md", size: 20, updatedAt: "2026-09-21T00:00:00Z" },
];

function session(artifactFiles: SessionArtifact[] = artifacts): WorkspaceSession {
	return { id: "sess-1", artifactFiles } as WorkspaceSession;
}

function renderView(ui: ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider>{ui}</TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("SessionArtifactsView", () => {
	beforeEach(() => {
		openPreviewMock.mockReset();
		deleteArtifactMock.mockReset().mockResolvedValue(undefined);
	});

	it("lists every artifact newest first", () => {
		renderView(<SessionArtifactsView session={session()} />);

		expect(within(screen.getByRole("list")).getAllByRole("button").map((button) => button.textContent?.replace(/\d+[a-z]+$|now$/, ""))).toEqual([
			"report.html",
			"notes.md",
			"plan.md",
		]);
	});

	it("shows an empty state when the session has no artifacts", () => {
		renderView(<SessionArtifactsView session={session([])} />);

		expect(screen.getByText("No artifacts in this session yet.")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /^Open in / })).not.toBeInTheDocument();
	});

	it("opens the artifact folder in the file manager and the terminal", async () => {
		const openArtifacts = vi.spyOn(aoBridge.editorHandoff, "openArtifacts");
		renderView(<SessionArtifactsView session={session()} />);

		const [fileManager, terminal] = screen.getAllByRole("button", { name: /^Open in / });
		await userEvent.click(fileManager);
		await userEvent.click(terminal);

		expect(openArtifacts).toHaveBeenNthCalledWith(1, { sessionId: "sess-1", targetId: "file-manager" });
		expect(openArtifacts).toHaveBeenNthCalledWith(2, { sessionId: "sess-1", targetId: "terminal" });
		openArtifacts.mockRestore();
	});

	it("shows why the artifact folder could not open", async () => {
		const openArtifacts = vi.spyOn(aoBridge.editorHandoff, "openArtifacts").mockRejectedValue(
			new Error("Error invoking remote method 'editorHandoff:openArtifacts': Error: This session has no artifacts folder yet."),
		);
		renderView(<SessionArtifactsView session={session()} />);

		await userEvent.click(screen.getAllByRole("button", { name: /^Open in / })[0]);

		expect(await screen.findByRole("alert")).toHaveTextContent("This session has no artifacts folder yet.");
		openArtifacts.mockRestore();
	});

	it("clears a failed copy after a few seconds", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockRejectedValue(new Error("Could not write to the clipboard."));
		try {
			renderView(<SessionArtifactsView request={{ key: 1, path: "notes.md" }} session={{ ...session(), cloud: { orgId: "org-1" } } as WorkspaceSession} />);

			await userEvent.click(screen.getByRole("button", { name: "notes.md" }));
			await userEvent.click(screen.getByRole("menuitem", { name: "Copy as Markdown" }));
			expect(await screen.findByRole("alert")).toHaveTextContent("Could not write to the clipboard.");

			await act(() => vi.advanceTimersByTimeAsync(5_000));
			expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		} finally {
			writeText.mockRestore();
			vi.useRealTimers();
		}
	});

	it("hides the folder actions for a session on a remote host", () => {
		renderView(<SessionArtifactsView hostId="remote-1" session={session()} />);

		expect(screen.queryByRole("button", { name: /^Open in / })).not.toBeInTheDocument();
	});

	it("opens an html artifact in the Browser panel", async () => {
		renderView(<SessionArtifactsView session={session()} />);

		await userEvent.click(screen.getByRole("button", { name: /report\.html/ }));

		expect(openPreviewMock).toHaveBeenCalledWith("http://artifact.localhost/report.html");
		expect(screen.queryByTestId("artifact-view")).not.toBeInTheDocument();
	});

	it("opens a file artifact in the viewer and returns to the list", async () => {
		renderView(<SessionArtifactsView session={session()} />);

		await userEvent.click(screen.getByRole("button", { name: /plan\.md/ }));
		expect(screen.getByTestId("artifact-view")).toHaveTextContent("viewer:reports/plan.md");

		await userEvent.click(screen.getByRole("button", { name: "Back to artifacts" }));
		expect(screen.queryByTestId("artifact-view")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: /notes\.md/ })).toBeInTheDocument();
	});

	it("offers version history for a text file but not for a binary one", async () => {
		const image: SessionArtifact = { kind: "file", name: "chart.png", path: "chart.png", size: 30, updatedAt: "2026-09-20T00:00:00Z" };
		renderView(<SessionArtifactsView session={session([...artifacts, image])} />);

		await userEvent.click(screen.getByRole("button", { name: /plan\.md/ }));
		expect(await screen.findByRole("button", { name: "Version history" })).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Back to artifacts" }));
		await userEvent.click(screen.getByRole("button", { name: /chart\.png/ }));
		await vi.waitFor(() => expect(screen.queryByRole("button", { name: "Version history" })).not.toBeInTheDocument());
		await userEvent.click(screen.getByRole("button", { name: /chart\.png/ }));
		expect(screen.queryByRole("menuitem", { name: "Copy contents" })).not.toBeInTheDocument();
	});

	it("shows the open artifact full screen, keeping the same viewer, until Escape", async () => {
		renderView(<SessionArtifactsView session={session()} />);
		await userEvent.click(screen.getByRole("button", { name: /notes\.md/ }));
		const viewer = screen.getByTestId("artifact-view");

		await userEvent.click(screen.getByRole("button", { name: "Full screen" }));

		const overlay = screen.getByRole("dialog", { name: "notes.md" });
		expect(overlay.parentElement).toBe(document.body);
		expect(within(overlay).getByTestId("artifact-view")).toBe(viewer);
		expect(overlay).toHaveFocus();
		expect(within(overlay).getByRole("button", { name: "Exit full screen" })).toBeInTheDocument();

		await userEvent.keyboard("{Escape}");

		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(screen.getByTestId("artifact-view")).toBe(viewer);
		expect(screen.getByRole("button", { name: "Full screen" })).toBeInTheDocument();
	});

	it("covers the whole app window without taking the window itself full screen", async () => {
		const action = vi.spyOn(aoBridge.menu, "action").mockResolvedValue(undefined);
		renderView(<SessionArtifactsView session={session()} />);
		await userEvent.click(screen.getByRole("button", { name: /notes\.md/ }));

		await userEvent.click(screen.getByRole("button", { name: "Full screen" }));

		expect(screen.getByRole("dialog", { name: "notes.md" })).toHaveClass("fixed", "inset-0");
		await userEvent.click(screen.getByRole("button", { name: "Exit full screen" }));
		expect(action).not.toHaveBeenCalled();
		action.mockRestore();
	});

	it("leaves full screen when going back to the list", async () => {
		renderView(<SessionArtifactsView session={session()} />);
		await userEvent.click(screen.getByRole("button", { name: /notes\.md/ }));
		await userEvent.click(screen.getByRole("button", { name: "Full screen" }));

		await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Back to artifacts" }));

		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(screen.getByRole("list")).toBeInTheDocument();
	});

	it("names the open artifact once, in a menu that offers Delete", async () => {
		renderView(<SessionArtifactsView request={{ key: 1, path: "reports/plan.md" }} session={session()} />);

		expect(screen.getByRole("button", { name: "Back to artifacts" })).toBeInTheDocument();
		expect(screen.getAllByText("plan.md")).toHaveLength(1);
		await userEvent.click(screen.getByRole("button", { name: "plan.md" }));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Delete artifact"]);
	});

	it("copies a cloud artifact's contents to the clipboard", async () => {
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		renderView(<SessionArtifactsView request={{ key: 1, path: "notes.md" }} session={{ ...session(), cloud: { orgId: "org-1" } } as WorkspaceSession} />);

		await userEvent.click(screen.getByRole("button", { name: "notes.md" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Copy as Markdown" }));

		await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith("contents of notes.md"));
		writeText.mockRestore();
	});

	it.each([
		{ location: "local", hostId: undefined, cloud: false, path: "chart.png", separators: 0, actions: ["Delete artifact"] },
		{ location: "remote", hostId: "remote-1", cloud: false, path: "chart.png", separators: 0, actions: ["Delete artifact"] },
		{ location: "cloud", hostId: undefined, cloud: true, path: "chart.png", separators: 0, actions: ["Delete artifact"] },
		{ location: "local", hostId: undefined, cloud: false, path: "notes.md", separators: 0, actions: ["Delete artifact"] },
		{ location: "remote", hostId: "remote-1", cloud: false, path: "notes.md", separators: 0, actions: ["Delete artifact"] },
		{ location: "cloud", hostId: undefined, cloud: true, path: "notes.md", separators: 1, actions: ["Copy as Markdown", "Delete artifact"] },
	])("separates only populated action groups for $location $path", async ({ hostId, cloud, path, separators, actions }) => {
		const artifact: SessionArtifact = { kind: "file", name: path, path, size: 2, updatedAt: "2026-09-24T00:00:00Z" };
		const currentSession = { ...session([artifact]), ...(cloud ? { cloud: { orgId: "org-1" } } : {}) } as WorkspaceSession;
		renderView(<SessionArtifactsView hostId={hostId} request={{ key: 1, path }} session={currentSession} />);

		await userEvent.click(screen.getByRole("button", { name: path }));
		const menu = screen.getByRole("menu");
		expect(within(menu).getAllByRole("menuitem").map((item) => item.textContent)).toEqual(actions);
		expect(within(menu).queryAllByRole("separator")).toHaveLength(separators);
		const children = Array.from(menu.children);
		expect(children[0]).not.toHaveAttribute("role", "separator");
		expect(children.at(-1)).not.toHaveAttribute("role", "separator");
		for (let index = 1; index < children.length; index += 1) {
			if (children[index].getAttribute("role") === "separator") expect(children[index - 1]).not.toHaveAttribute("role", "separator");
		}
	});

	it("deletes the open artifact right away", async () => {
		renderView(<SessionArtifactsView request={{ key: 1, path: "reports/plan.md" }} session={session()} />);

		await userEvent.click(screen.getByRole("button", { name: "plan.md" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Delete artifact" }));

		await vi.waitFor(() => expect(deleteArtifactMock).toHaveBeenCalledWith({ path: "reports/plan.md", sessionId: "sess-1", hostId: undefined }));
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	});

	it("shows a failed delete in the header", async () => {
		deleteArtifactMock.mockRejectedValue(new Error("artifact file not found"));
		renderView(<SessionArtifactsView request={{ key: 1, path: "notes.md" }} session={session()} />);

		await userEvent.click(screen.getByRole("button", { name: "notes.md" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Delete artifact" }));

		expect(await screen.findByRole("alert")).toHaveTextContent("artifact file not found");
	});

	it("opens a requested artifact and passes its feedback request through", () => {
		renderView(<SessionArtifactsView request={{ feedback: true, key: 7, path: "notes.md" }} session={session()} />);

		const viewer = screen.getByTestId("artifact-view");
		expect(viewer).toHaveTextContent("viewer:notes.md");
		expect(viewer).toHaveAttribute("data-feedback-key", "7");
	});
});
