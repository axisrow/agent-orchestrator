import { AGENT_OPTIONS, agentLabel } from "./agent-options";

describe("AGENT_OPTIONS", () => {
	it("offers fx as a spawn harness exactly once", () => {
		expect(AGENT_OPTIONS.filter((agent: string) => agent === "fx")).toHaveLength(1);
	});
	it("contains recent harnesses exactly once and has no duplicate harness ids", () => {
		expect(AGENT_OPTIONS.filter((agent) => agent === "omp")).toHaveLength(1);
		expect(AGENT_OPTIONS.filter((agent) => agent === "kimi")).toHaveLength(1);
		expect(AGENT_OPTIONS.filter((agent) => agent === "goose")).toHaveLength(1);
		expect(AGENT_OPTIONS.filter((agent) => agent === "memcode")).toHaveLength(1);
		expect(new Set(AGENT_OPTIONS).size).toBe(AGENT_OPTIONS.length);
	});
	it("uses the concise Kimi display name", () => {
		expect(agentLabel("kimi")).toBe("Kimi");
	});

	it("exposes OpenCode 2 as a distinct worker option", () => {
		expect(AGENT_OPTIONS).toContain("opencode-v2");
		expect(AGENT_OPTIONS).not.toContain("opencode");
		expect(agentLabel("opencode-v2")).toBe("OpenCode 2");
	});
});
