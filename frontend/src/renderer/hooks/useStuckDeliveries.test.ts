import { describe, expect, it } from "vitest";
import type { StuckDeliveryGroup } from "./useStuckDeliveries";
import { stuckDeliveriesBySession } from "./useStuckDeliveries";

function group(overrides: Partial<StuckDeliveryGroup> = {}): StuckDeliveryGroup {
	return {
		ageSeconds: 120,
		attempts: 3,
		causeClass: "attempts exhausted",
		deliveries: 1,
		oldestDeliveryAt: "2026-11-23T00:00:00Z",
		projectId: "proj-1",
		recipient: "orchestrator",
		sessionId: "sess-1",
		terminal: false,
		zombie: false,
		...overrides,
	};
}

describe("stuckDeliveriesBySession", () => {
	it("counts live and terminal groups on the same session as live-only", () => {
		const live = group({ deliveries: 2 });
		const retired = group({ terminal: true, deliveries: 84, causeClass: "superseded by a newer prompt" });

		expect(stuckDeliveriesBySession([live, retired])).toEqual(new Map([["sess-1", 2]]));
	});

	it("counts nothing for a session whose only groups are terminal", () => {
		const retired = group({ sessionId: "sess-2", terminal: true, deliveries: 20 });

		expect(stuckDeliveriesBySession([retired])).toEqual(new Map());
	});

	it("sums several live groups of one session", () => {
		const first = group({ causeClass: "attempts exhausted", deliveries: 3 });
		const second = group({ causeClass: "no active orchestrator", deliveries: 1 });

		expect(stuckDeliveriesBySession([first, second])).toEqual(new Map([["sess-1", 4]]));
	});

	it("skips groups without a session", () => {
		const anonymous = group({ sessionId: "", deliveries: 5 });

		expect(stuckDeliveriesBySession([anonymous])).toEqual(new Map());
	});
});
