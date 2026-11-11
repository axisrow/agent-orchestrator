import { aoBridge } from "./bridge";
import { currentRoutePath } from "./report-problem";
import { routeSurface } from "./telemetry";

// Fire-and-forget local crash report over the crash:report channel. Deliberately
// independent of the telemetry pipeline: it must reach the on-disk log even when
// analytics is opted out or PostHog never initializes. Errors here must never
// become unhandled rejections on the crash path.
export function captureCrashReport(error: unknown, options: { operation: string; componentStack?: string | null }): void {
	try {
		const normalized = error instanceof Error ? error : new Error(String(error));
		aoBridge.crash.report({
			surface: routeSurface(currentRoutePath()),
			operation: options.operation,
			name: normalized.name || "Error",
			message: normalized.message,
			stack: normalized.stack ?? `${normalized.name}: ${normalized.message}`,
			...(options.componentStack ? { componentStack: options.componentStack } : {}),
		});
	} catch {
		// The crash screen is already showing the failure; never add a second one.
	}
}

// Plain-text crash report for the Copy button (and identical in spirit to the
// JSONL line the main process writes). Raw paths and stacks on purpose — they
// are the diagnosable part for the machine's own operator.
// ponytail: if these reports ever auto-attach to GitHub issues, run the result
// through sanitizeReportText from report-problem.ts first.
export function formatCrashReport(parts: {
	title: string;
	name: string;
	message: string;
	surface: string;
	operation: string;
	version: string;
	timestamp: string;
	stack?: string;
	componentStack?: string | null;
}): string {
	return [
		parts.title,
		`${parts.name}: ${parts.message}`,
		`Surface: ${parts.surface} (${parts.operation})`,
		`Version: ${parts.version}`,
		`Timestamp: ${parts.timestamp}`,
		parts.stack ? `\n${parts.stack}` : "",
		parts.componentStack ? `\nReact component stack:\n${parts.componentStack}` : "",
	].filter(Boolean).join("\n");
}
