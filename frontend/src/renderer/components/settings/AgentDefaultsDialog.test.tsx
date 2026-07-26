import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";
import { AgentDefaultsDialog } from "./AgentDefaultsDialog";
import { setApiBaseUrl } from "../../lib/api-client";

const DEFAULT_WORKER =
  "## AO Worker Role\n\nYou are an implementation worker.\n\n## Local Git Rules\n\n- Work locally.";
const DEFAULT_ORCH =
  "## AO Orchestrator Role\n\nYou are the human-facing orchestrator.";

type DialogProps = ComponentProps<typeof AgentDefaultsDialog>;

function renderDialog(props: Partial<DialogProps> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <AgentDefaultsDialog open={true} onOpenChange={() => {}} {...props} />
    </QueryClientProvider>,
  );
  return qc;
}

// fetchMock lets each test program GET/PUT responses for /api/v1/user-config.
// openapi-fetch returns the parsed JSON body directly as `data` (success) or
// `error` (non-2xx), so the Response body is the raw object, not wrapped in
// `{ data }`.
function fetchMock(getBody: unknown, putBody?: unknown) {
  const handler = vi.fn(
    async (_input: RequestInfo | URL, init?: RequestInit) => {
      const method = (init?.method ?? "GET").toUpperCase();
      let body: unknown = getBody;
      if (method === "PUT") body = putBody ?? getBody;
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    },
  );
  vi.stubGlobal("fetch", handler);
  return handler;
}

// waitForValue polls until a textarea labelled `label` holds `value`. The
// textarea mounts empty before the query resolves; findByLabelText would return
// immediately against the empty box, so we must wait for the prefilled value.
async function waitForValue(label: string, value: string) {
  await waitFor(() => {
    expect(screen.getByLabelText(label)).toHaveValue(value);
  });
}

// putRequestBody finds the PUT /user-config fetch call and returns its parsed
// body. onSuccess invalidates the query, so the fetch mock may also record a
// follow-up GET; locate the PUT by method rather than call index. The apiClient
// buffers request bodies as ArrayBuffer, so decode the body before parsing.
function putRequestBody(handler: ReturnType<typeof fetchMock>) {
  const putCall = handler.mock.calls.find(
    (call) => (call[1]?.method ?? "GET").toUpperCase() === "PUT",
  );
  const raw = putCall?.[1]?.body;
  if (raw === undefined) return {} as Record<string, unknown>;
  const text =
    raw instanceof ArrayBuffer ? new TextDecoder().decode(raw) : String(raw);
  return JSON.parse(text);
}

beforeEach(() => {
  // Trust a base URL so apiClient actually issues fetch calls in the test env.
  setApiBaseUrl("http://127.0.0.1:3001");
});

afterEach(() => {
  vi.unstubAllGlobals();
  setApiBaseUrl(null);
  vi.restoreAllMocks();
});

describe("AgentDefaultsDialog", () => {
  it("prefills the textareas with the assembled default prompts when no override is stored", async () => {
    fetchMock({
      agentConfig: {},
      defaultWorkerPrompt: DEFAULT_WORKER,
      defaultOrchestratorPrompt: DEFAULT_ORCH,
    });

    renderDialog();

    await waitForValue("Worker prompt override", DEFAULT_WORKER);
    await waitForValue("Orchestrator prompt override", DEFAULT_ORCH);
  });

  it("prefills the stored override when one exists (override wins over default)", async () => {
    const override = "## Custom Worker\nDo the custom thing.";
    fetchMock({
      agentConfig: { workerPromptOverride: override },
      defaultWorkerPrompt: DEFAULT_WORKER,
      defaultOrchestratorPrompt: DEFAULT_ORCH,
    });

    renderDialog();

    await waitForValue("Worker prompt override", override);
  });

  it("disables Save until a prompt is actually edited away from the initial value", async () => {
    const user = userEvent.setup();
    // No stored override: initial value = the hardcoded default.
    fetchMock({
      agentConfig: {},
      defaultWorkerPrompt: DEFAULT_WORKER,
      defaultOrchestratorPrompt: DEFAULT_ORCH,
    });

    renderDialog();
    await waitForValue("Worker prompt override", DEFAULT_WORKER);

    // Dialog opened with the prefilled default and nothing was edited → Save
    // disabled (there is nothing to save; the same value would round-trip).
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();

    // Editing worker away from the default gives Save something to do.
    const worker = screen.getByLabelText("Worker prompt override");
    await user.type(worker, " more");
    expect(save).toBeEnabled();

    // Editing back to the exact default text makes both fields unchanged
    // again → Save disabled once more.
    await user.clear(worker);
    await user.type(worker, DEFAULT_WORKER);
    expect(save).toBeDisabled();
  });

  it("stores the override text when the prompt differs from the default, preserving model/permissions", async () => {
    const user = userEvent.setup();
    const handler = fetchMock(
      {
        // A non-empty existing config must survive the wholesale-replace PUT.
        agentConfig: { model: "claude-opus-4-8", permissions: "auto" },
        defaultWorkerPrompt: DEFAULT_WORKER,
        defaultOrchestratorPrompt: DEFAULT_ORCH,
      },
      { agentConfig: { workerPromptOverride: "edited" } },
    );

    renderDialog();
    const worker = await screen.findByLabelText("Worker prompt override");
    await waitForValue("Worker prompt override", DEFAULT_WORKER);
    await user.clear(worker);
    await user.type(worker, "## Edited Worker\nNew instructions.");

    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(putRequestBody(handler)).toBeTruthy());
    const putBody = putRequestBody(handler);
    expect(putBody.agentConfig.workerPromptOverride).toBe(
      "## Edited Worker\nNew instructions.",
    );
    // Wholesale replace merges over the loaded config, so model/permissions survive.
    expect(putBody.agentConfig.model).toBe("claude-opus-4-8");
    expect(putBody.agentConfig.permissions).toBe("auto");
  });

  it("clears an existing override back to the default by editing it back", async () => {
    const user = userEvent.setup();
    const handler = fetchMock(
      {
        agentConfig: { workerPromptOverride: "## Custom" },
        defaultWorkerPrompt: DEFAULT_WORKER,
        defaultOrchestratorPrompt: DEFAULT_ORCH,
      },
      { agentConfig: {} },
    );

    renderDialog();
    const worker = await screen.findByLabelText("Worker prompt override");
    await waitForValue("Worker prompt override", "## Custom");
    // Replace the override with the default text → Save clears the override.
    await user.clear(worker);
    await user.type(worker, DEFAULT_WORKER);

    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(putRequestBody(handler)).toBeTruthy());
    const putBody = putRequestBody(handler);
    expect(putBody.agentConfig.workerPromptOverride).toBeUndefined();
  });

  it("Reset to default clears both overrides and refills the textareas with the hardcoded baseline", async () => {
    const user = userEvent.setup();
    // GET returns a stored override plus a model that must survive the
    // wholesale-replace PUT; PUT echoes back a cleared config.
    const handler = fetchMock(
      {
        agentConfig: {
          model: "claude-opus-4-8",
          workerPromptOverride: "## Custom Worker",
          orchestratorPromptOverride: "## Custom Orch",
        },
        defaultWorkerPrompt: DEFAULT_WORKER,
        defaultOrchestratorPrompt: DEFAULT_ORCH,
      },
      { agentConfig: { model: "claude-opus-4-8" } },
    );

    renderDialog();
    await waitForValue("Worker prompt override", "## Custom Worker");
    await waitForValue("Orchestrator prompt override", "## Custom Orch");

    await user.click(screen.getByRole("button", { name: /reset to default/i }));

    // PUT stores explicit undefined for both overrides, preserving model.
    await waitFor(() => expect(putRequestBody(handler)).toBeTruthy());
    const putBody = putRequestBody(handler);
    expect(putBody.agentConfig.workerPromptOverride).toBeUndefined();
    expect(putBody.agentConfig.orchestratorPromptOverride).toBeUndefined();
    expect(putBody.agentConfig.model).toBe("claude-opus-4-8");

    // Textareas now show the hardcoded baseline.
    await waitForValue("Worker prompt override", DEFAULT_WORKER);
    await waitForValue("Orchestrator prompt override", DEFAULT_ORCH);
  });

  it("shows a save error when the PUT fails", async () => {
    const user = userEvent.setup();
    // GET succeeds; PUT fails with an error envelope.
    const handler = vi.fn(
      async (_input: RequestInfo | URL, init?: RequestInit) => {
        const method = (init?.method ?? "GET").toUpperCase();
        if (method === "PUT") {
          return new Response(
            JSON.stringify({ code: "internal_error", message: "boom" }),
            {
              status: 500,
              headers: { "content-type": "application/json" },
            },
          );
        }
        return new Response(
          JSON.stringify({
            agentConfig: {},
            defaultWorkerPrompt: DEFAULT_WORKER,
            defaultOrchestratorPrompt: DEFAULT_ORCH,
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      },
    );
    vi.stubGlobal("fetch", handler);

    renderDialog();
    const worker = await screen.findByLabelText("Worker prompt override");
    await waitForValue("Worker prompt override", DEFAULT_WORKER);
    // Edit the prompt so Save is enabled (an unchanged field stays disabled).
    await user.type(worker, " more");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("boom");
  });

  it("disables Save and shows a role-warning when the worker prompt is cleared", async () => {
    const user = userEvent.setup();
    fetchMock({
      agentConfig: {},
      defaultWorkerPrompt: DEFAULT_WORKER,
      defaultOrchestratorPrompt: DEFAULT_ORCH,
    });

    renderDialog();
    const worker = await screen.findByLabelText("Worker prompt override");
    await waitForValue("Worker prompt override", DEFAULT_WORKER);
    await user.clear(worker);

    // An empty override would replace the hardcoded prompt with nothing, so the
    // agent wouldn't know its role. Save must be disabled and a warning shown.
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    expect(screen.getByRole("alert")).toHaveTextContent(
      /A worker needs a system prompt/,
    );
  });

  it("disables Save and shows a role-warning when the orchestrator prompt is cleared", async () => {
    const user = userEvent.setup();
    fetchMock({
      agentConfig: {},
      defaultWorkerPrompt: DEFAULT_WORKER,
      defaultOrchestratorPrompt: DEFAULT_ORCH,
    });

    renderDialog();
    const orchestrator = await screen.findByLabelText(
      "Orchestrator prompt override",
    );
    await waitForValue("Orchestrator prompt override", DEFAULT_ORCH);
    await user.clear(orchestrator);

    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    expect(screen.getByRole("alert")).toHaveTextContent(
      /An orchestrator needs a system prompt/,
    );
  });

  it("Reset to default refills both prompts and keeps Save disabled when already at default", async () => {
    const user = userEvent.setup();
    const handler = fetchMock(
      {
        agentConfig: {},
        defaultWorkerPrompt: DEFAULT_WORKER,
        defaultOrchestratorPrompt: DEFAULT_ORCH,
      },
      { agentConfig: {} },
    );

    renderDialog();
    const worker = await screen.findByLabelText("Worker prompt override");
    await waitForValue("Worker prompt override", DEFAULT_WORKER);
    // Clear worker → Save disabled (empty override is unsafe).
    await user.clear(worker);
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();

    // Reset refills the hardcoded baseline. With no stored override, that
    // baseline equals the initial value, so there is nothing new to save and
    // Save stays disabled (Reset itself already cleared the override via PUT).
    await user.click(screen.getByRole("button", { name: /reset to default/i }));

    await waitFor(() => expect(putRequestBody(handler)).toBeTruthy());
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    await waitForValue("Worker prompt override", DEFAULT_WORKER);
  });

  it("disables Save when nothing changed, enables after an edit, then disables again after Reset to default", async () => {
    const user = userEvent.setup();
    const handler = fetchMock(
      {
        agentConfig: {},
        defaultWorkerPrompt: DEFAULT_WORKER,
        defaultOrchestratorPrompt: DEFAULT_ORCH,
      },
      { agentConfig: {} },
    );

    renderDialog();
    const worker = await screen.findByLabelText("Worker prompt override");
    await waitForValue("Worker prompt override", DEFAULT_WORKER);

    // No override stored, prefilled text = default → nothing to save yet.
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();

    // A real edit gives Save something to persist.
    await user.type(worker, " edited");
    expect(save).toBeEnabled();

    // Reset wipes the edit and refills the baseline. With no stored override
    // the baseline is the initial value again, so Save returns to disabled
    // (the override was already cleared by Reset's own PUT).
    await user.click(screen.getByRole("button", { name: /reset to default/i }));
    await waitFor(() => expect(putRequestBody(handler)).toBeTruthy());
    expect(save).toBeDisabled();
    await waitForValue("Worker prompt override", DEFAULT_WORKER);

    // Reset to default stays clickable regardless of the unchanged state.
    expect(
      screen.getByRole("button", { name: /reset to default/i }),
    ).toBeEnabled();
  });
});
