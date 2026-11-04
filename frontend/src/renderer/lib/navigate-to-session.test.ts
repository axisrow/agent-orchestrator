import { describe, expect, it } from "vitest";
import { projectLandingSessionId, projectNavigateTarget, sessionNavigateTarget } from "./navigate-to-session";
import type { WorkspaceSession } from "../types/workspace";

function session(overrides: Partial<WorkspaceSession> & { id: string }): WorkspaceSession {
	return { kind: "worker", updatedAt: "2026-01-01T00:00:00Z", ...overrides } as WorkspaceSession;
}

describe("sessionNavigateTarget", () => {
	it("keeps local URLs and identifies the remote host in a session URL", () => {
		expect(sessionNavigateTarget("project-1", "session-1")).toEqual({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "project-1", sessionId: "session-1" },
		});
		expect(sessionNavigateTarget("project-1", "session-1", "machine-a")).toEqual({
			to: "/host/$hostId/project/$projectId/session/$sessionId",
			params: { hostId: "machine-a", projectId: "project-1", sessionId: "session-1" },
		});
	});
});

it("routes a project to its owning host without changing the local URL", () => {
	expect(projectNavigateTarget("project-1")).toEqual({ to: "/projects/$projectId", params: { projectId: "project-1" } });
	expect(projectNavigateTarget("project-1", "machine-a")).toEqual({
		to: "/host/$hostId/project/$projectId", params: { hostId: "machine-a", projectId: "project-1" },
	});
});

describe("projectLandingSessionId", () => {
	it("lands on the live orchestrator — the sidebar icon's target (#260)", () => {
		expect(projectLandingSessionId([
			session({ id: "boss", kind: "orchestrator", updatedAt: "2026-01-01T00:00:00Z" }),
			session({ id: "new", lastUserMessageAt: "2026-02-02T00:00:00Z" }),
		])).toBe("boss");
	});

	it("lands on the newest of several live orchestrators", () => {
		expect(projectLandingSessionId([
			session({ id: "boss-old", kind: "orchestrator", createdAt: "2026-01-01T00:00:00Z" }),
			session({ id: "boss-new", kind: "orchestrator", createdAt: "2026-02-02T00:00:00Z" }),
			session({ id: "worker", lastUserMessageAt: "2026-05-05T00:00:00Z" }),
		])).toBe("boss-new");
	});

	it("falls back to the most recently messaged live worker without an orchestrator", () => {
		expect(projectLandingSessionId([
			session({ id: "old", lastUserMessageAt: "2026-01-01T00:00:00Z" }),
			session({ id: "new", lastUserMessageAt: "2026-02-02T00:00:00Z" }),
		])).toBe("new");
	});

	it("falls back to updatedAt when message timestamps are unparseable", () => {
		expect(projectLandingSessionId([
			session({ id: "older", lastUserMessageAt: "not-a-date", updatedAt: "2026-01-01T00:00:00Z" }),
			session({ id: "newer", lastUserMessageAt: "also-not-a-date", updatedAt: "2026-03-03T00:00:00Z" }),
		])).toBe("newer");
	});

	it("lands on a terminated orchestrator over live workers — its view carries Restore (#260 reopen)", () => {
		expect(projectLandingSessionId([
			session({ id: "dead-boss", kind: "orchestrator", isTerminated: true, lastUserMessageAt: "2026-06-06T00:00:00Z" }),
			session({ id: "gone", status: "terminated", lastUserMessageAt: "2026-04-04T00:00:00Z" }),
			session({ id: "worker-1", lastUserMessageAt: "2026-01-01T00:00:00Z" }),
		])).toBe("dead-boss");
	});

	it("prefers the live orchestrator over a newer terminated one — the icon's target", () => {
		expect(projectLandingSessionId([
			session({ id: "boss-dead", kind: "orchestrator", isTerminated: true, createdAt: "2026-03-03T00:00:00Z" }),
			session({ id: "boss-live", kind: "orchestrator", createdAt: "2026-01-01T00:00:00Z" }),
			session({ id: "worker-1", lastUserMessageAt: "2026-05-05T00:00:00Z" }),
		])).toBe("boss-live");
	});

	it("lands on the newest orchestrator when several are terminated", () => {
		expect(projectLandingSessionId([
			session({ id: "boss-old", kind: "orchestrator", isTerminated: true, createdAt: "2026-01-01T00:00:00Z" }),
			session({ id: "boss-new", kind: "orchestrator", isTerminated: true, createdAt: "2026-02-02T00:00:00Z" }),
			session({ id: "worker-1", lastUserMessageAt: "2026-05-05T00:00:00Z" }),
		])).toBe("boss-new");
	});

	it("returns undefined only when the project has no sessions at all", () => {
		expect(projectLandingSessionId([])).toBeUndefined();
	});

	it("falls back to the top live worker only when no orchestrator session exists", () => {
		expect(projectLandingSessionId([
			session({ id: "gone", status: "terminated", lastUserMessageAt: "2026-04-04T00:00:00Z" }),
			session({ id: "worker-1", lastUserMessageAt: "2026-01-01T00:00:00Z" }),
		])).toBe("worker-1");
	});
});
