export const RENDERER_CRASH_REPORT_CHANNEL = "crash:report";

// Renderer-built crash report for the local durable log. The renderer never
// sends its app version: Electron main stamps its own at write time, so the
// JSONL always carries the version of the process that actually wrote it.
export type RendererCrashReport = {
	surface: string;
	operation: string;
	name: string;
	message: string;
	stack?: string;
	componentStack?: string;
};
