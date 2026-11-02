import { AGENT_OPTIONS, agentLabel } from "./agent-options";

describe("AGENT_OPTIONS", () => {
	it("contains recent harnesses exactly once and has no duplicate harness ids", () => {
		expect(AGENT_OPTIONS.filter((agent) => agent === "omp")).toHaveLength(1);
		expect(AGENT_OPTIONS.filter((agent) => agent === "agy")).toHaveLength(1);
		expect(AGENT_OPTIONS.filter((agent) => agent === "pi")).toHaveLength(1);
		expect(new Set(AGENT_OPTIONS).size).toBe(AGENT_OPTIONS.length);
	});

	it("exposes OpenCode 2 as a distinct worker option", () => {
		expect(AGENT_OPTIONS).toContain("opencode-v2");
		expect(AGENT_OPTIONS).not.toContain("opencode");
		expect(agentLabel("opencode-v2")).toBe("OpenCode 2");
	});
});
