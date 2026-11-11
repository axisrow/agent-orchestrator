import { mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
	createRendererCrashLog,
	parseRendererCrashReport,
	RENDERER_CRASH_LOG_MAX_BYTES,
} from "./renderer-crash-log";

const directories: string[] = [];
afterEach(() => { for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true }); });
function setup() {
	const directory = mkdtempSync(path.join(os.tmpdir(), "ao-crash-log-"));
	directories.push(directory);
	const file = path.join(directory, "renderer-crashes.jsonl");
	return { directory, file, log: createRendererCrashLog(file, "test-version") };
}
const report = {
	surface: "session_detail",
	operation: "session_view",
	name: "Error",
	message: "boom",
	stack: "Error: boom\n    at SessionView (app://renderer/assets/SessionView-abc.js:1:2)",
};

describe("renderer crash log", () => {
	it("persists the report with timestamp and the main-side app version", () => {
		const { file, log } = setup();
		log(report);
		log({ ...report, componentStack: "    at div" });
		const lines = readFileSync(file, "utf8").trim().split("\n").map((line) => JSON.parse(line));
		expect(lines[0]).toEqual({
			timestamp: expect.any(String),
			event: "renderer_crash",
			appVersion: "test-version",
			surface: "session_detail",
			operation: "session_view",
			name: "Error",
			message: "boom",
			stack: expect.stringContaining("SessionView"),
		});
		expect(lines[0].timestamp).toMatch(/^\d{4}-\d{2}-\d{2}T/);
		expect(lines[1].componentStack).toBe("    at div");
		if (process.platform !== "win32") expect(statSync(file).mode & 0o777).toBe(0o600);
	});

	it("rotates at the size limit and retains only one previous file", () => {
		const { file, log } = setup();
		log(report);
		writeFileSync(file, "x".repeat(RENDERER_CRASH_LOG_MAX_BYTES), { mode: 0o600 });
		log(report);
		expect(statSync(`${file}.1`).size).toBe(RENDERER_CRASH_LOG_MAX_BYTES);
		expect(readFileSync(file, "utf8").trim().split("\n")).toHaveLength(1);
	});

	it("does not throw when the log path cannot be written", () => {
		const { directory } = setup();
		const blocked = path.join(directory, "not-a-directory");
		writeFileSync(blocked, "occupies the log directory path");
		const log = createRendererCrashLog(path.join(blocked, "renderer-crashes.jsonl"), "test-version");
		expect(() => log(report)).not.toThrow();
	});
});

describe("parseRendererCrashReport", () => {
	it("keeps allowlisted fields and drops unknown keys", () => {
		expect(parseRendererCrashReport({ ...report, hackerField: "x", version: "fake" })).toEqual(report);
	});

	it("rejects garbage shapes and malformed required fields", () => {
		expect(parseRendererCrashReport(null)).toBeNull();
		expect(parseRendererCrashReport("crash")).toBeNull();
		expect(parseRendererCrashReport([report])).toBeNull();
		expect(parseRendererCrashReport({ ...report, message: 42 })).toBeNull();
		expect(parseRendererCrashReport({ ...report, message: undefined })).toBeNull();
	});

	it("drops malformed optional fields without losing the report", () => {
		const parsed = parseRendererCrashReport({ ...report, stack: 7, componentStack: null });
		expect(parsed).toEqual({ surface: report.surface, operation: report.operation, name: report.name, message: report.message });
	});

	it("truncates oversized fields to their caps instead of dropping the report", () => {
		const truncated = parseRendererCrashReport({ ...report, message: "x".repeat(4001), stack: "y".repeat(16001) });
		expect(truncated?.message).toHaveLength(4000);
		expect(truncated?.stack).toHaveLength(16000);
		const atCap = parseRendererCrashReport({ ...report, message: "x".repeat(4000), stack: "x".repeat(16000) });
		expect(atCap?.message).toHaveLength(4000);
		expect(atCap?.stack).toHaveLength(16000);
	});
});
