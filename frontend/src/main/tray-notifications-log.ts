import { appendFileSync, chmodSync, mkdirSync, renameSync, statSync } from "node:fs";
import path from "node:path";
import type { TrayNotificationEvent, TrayNotificationFields } from "./tray-lifecycle";

export const TRAY_NOTIFICATION_LOG_MAX_BYTES = 256 * 1024;

// Local-only, bounded record of tray badge transitions (#353): every add and
// remove carries the session, the resulting count and the reason, so "why was
// the badge at 53" can be answered from the file afterwards. Same shape and
// privacy as renderer-crashes.jsonl: the only reader is the machine's own
// operator, nothing is forwarded anywhere.
export function createTrayNotificationLog(filePath: string, appVersion: string) {
	const directory = path.dirname(filePath);
	return (event: TrayNotificationEvent, fields: TrayNotificationFields): void => {
		try {
			const line = JSON.stringify({
				timestamp: new Date().toISOString(),
				event,
				appVersion,
				...fields,
			}) + "\n";
			mkdirSync(directory, { recursive: true, mode: 0o700 });
			chmodSync(directory, 0o700);
			try {
				chmodSync(filePath, 0o600);
				if (statSync(filePath).size + Buffer.byteLength(line) > TRAY_NOTIFICATION_LOG_MAX_BYTES)
					renameSync(filePath, `${filePath}.1`);
			} catch (error) {
				if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
			}
			appendFileSync(filePath, line, { mode: 0o600 });
			chmodSync(filePath, 0o600);
		} catch {
			// Diagnostics must never replace the original failure (e.g. disk full).
		}
	};
}
