import { appendFileSync, chmodSync, mkdirSync, renameSync, statSync } from "node:fs";
import path from "node:path";
import type { RendererCrashReport } from "../shared/renderer-crash";

export const RENDERER_CRASH_LOG_MAX_BYTES = 256 * 1024;

const FIELD_CAPS = {
	surface: 120,
	operation: 120,
	name: 200,
	message: 4000,
	stack: 16000,
	componentStack: 8000,
} as const;

// The renderer is untrusted input for this sink: only allowlisted string
// fields within caps survive. A malformed required field drops the whole
// report; a malformed optional field only drops that field, so diagnostics
// never lose a report over a type mismatch in componentStack.
export function parseRendererCrashReport(input: unknown): RendererCrashReport | null {
	if (!input || typeof input !== "object" || Array.isArray(input)) return null;
	const value = input as Record<string, unknown>;
	const read = (field: keyof typeof FIELD_CAPS): string | undefined => {
		const raw = value[field];
		if (raw === undefined) return undefined;
		if (typeof raw !== "string" || raw.length > FIELD_CAPS[field]) return undefined;
		return raw;
	};
	const surface = read("surface");
	const operation = read("operation");
	const name = read("name");
	const message = read("message");
	if (surface === undefined || operation === undefined || name === undefined || message === undefined) return null;
	const stack = read("stack");
	const componentStack = read("componentStack");
	return {
		surface,
		operation,
		name,
		message,
		...(stack === undefined ? {} : { stack }),
		...(componentStack === undefined ? {} : { componentStack }),
	};
}

// Local-only, bounded renderer crash log. Unlike browser-close diagnostics this
// keeps raw messages and stacks on purpose: the file exists so a crash can be
// diagnosed afterwards, and its only reader is the machine's own operator.
// ponytail: unbounded entry count within the 256 KiB rotation window — add an
// age filter if the file ever becomes an audit surface.
export function createRendererCrashLog(filePath: string, appVersion: string) {
	const directory = path.dirname(filePath);
	return (report: RendererCrashReport): void => {
		try {
			const line = JSON.stringify({
				timestamp: new Date().toISOString(),
				event: "renderer_crash",
				appVersion,
				...report,
			}) + "\n";
			mkdirSync(directory, { recursive: true, mode: 0o700 });
			chmodSync(directory, 0o700);
			try {
				chmodSync(filePath, 0o600);
				if (statSync(filePath).size + Buffer.byteLength(line) > RENDERER_CRASH_LOG_MAX_BYTES)
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
