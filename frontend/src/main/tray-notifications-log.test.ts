import { mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { createTrayNotificationLog, TRAY_NOTIFICATION_LOG_MAX_BYTES } from "./tray-notifications-log";

const directories: string[] = [];
afterEach(() => { for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true }); });

describe("tray notification log", () => {
	it("persists each transition with timestamp, reason, and the session", () => {
		const directory = mkdtempSync(path.join(os.tmpdir(), "ao-tray-log-"));
		directories.push(directory);
		const file = path.join(directory, "tray-notifications.jsonl");
		const log = createTrayNotificationLog(file, "test-version");
		log("tray_notification_added", { sessionId: "s1", projectId: "p1", title: "ask", count: 1, reason: "waiting_input" });
		log("tray_notification_removed", { sessionId: "s1", projectId: "p1", title: "ask", count: 0, reason: "no_longer_waiting_input" });
		const lines = readFileSync(file, "utf8").trim().split("\n").map((line) => JSON.parse(line));
		expect(lines).toEqual([
			{ timestamp: expect.any(String), event: "tray_notification_added", appVersion: "test-version", sessionId: "s1", projectId: "p1", title: "ask", count: 1, reason: "waiting_input" },
			{ timestamp: expect.any(String), event: "tray_notification_removed", appVersion: "test-version", sessionId: "s1", projectId: "p1", title: "ask", count: 0, reason: "no_longer_waiting_input" },
		]);
		if (process.platform !== "win32") expect(statSync(file).mode & 0o777).toBe(0o600);
	});

	it("rotates at the size limit and keeps one previous file", () => {
		const directory = mkdtempSync(path.join(os.tmpdir(), "ao-tray-log-"));
		directories.push(directory);
		const file = path.join(directory, "tray-notifications.jsonl");
		const log = createTrayNotificationLog(file, "test-version");
		log("tray_notification_added", { sessionId: "s0", projectId: "p1", title: "seed", count: 1, reason: "waiting_input" });
		writeFileSync(file, "x".repeat(TRAY_NOTIFICATION_LOG_MAX_BYTES), { mode: 0o600 });
		log("tray_notification_added", { sessionId: "s1", projectId: "p1", title: "ask", count: 1, reason: "waiting_input" });
		expect(statSync(`${file}.1`).size).toBe(TRAY_NOTIFICATION_LOG_MAX_BYTES);
		expect(readFileSync(file, "utf8").trim().split("\n")).toHaveLength(1);
	});

	it("does not throw when the log path cannot be written", () => {
		const directory = mkdtempSync(path.join(os.tmpdir(), "ao-tray-log-"));
		directories.push(directory);
		const blocked = path.join(directory, "not-a-directory");
		writeFileSync(blocked, "occupies the log directory path");
		const log = createTrayNotificationLog(path.join(blocked, "tray-notifications.jsonl"), "test-version");
		expect(() => log("tray_notification_added", { sessionId: "s1", projectId: "p1", title: "ask", count: 1, reason: "waiting_input" })).not.toThrow();
	});
});
