import { describe, expect, it } from "vitest";
import { formatElapsed, formatTimeCompact, formatTokenCount } from "./formatting";

describe("formatting", () => {
	it.each([
		[0, "0 tok"],
		[999, "999 tok"],
		[1_000, "1K tok"],
		[12_400, "12.4K tok"],
		[1_250_000, "1.3M tok"],
	])("formats %d tokens as %s", (tokens, expected) => {
		expect(formatTokenCount(tokens)).toBe(expected);
	});

	it("formats relative time with an injected clock and labels", () => {
		const result = formatTimeCompact("2026-01-01T00:00:00Z", {
			now: new Date("2026-01-01T00:42:00Z"),
			translate: (key, values) => `${key}:${values?.n ?? ""}`,
		});

		expect(result).toBe("time.minutesAgo:42");
	});

	it.each([
		[0, "0s"],
		[59_999, "59s"],
		[60_000, "1m"],
		[3_540_000, "59m"],
		[3_600_000, "1h"],
		[7_200_000, "2h"],
		[7_500_000, "2h 5m"],
		[86_400_000, "1d"],
		[129_600_000, "1d 12h"],
		[172_800_000, "2d"],
		[-5_000, "0s"],
	])("formats %dms elapsed as %s", (ms, expected) => {
		expect(formatElapsed(ms)).toBe(expected);
	});
});
