import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../lib/bridge";
import { formatCrashReport } from "../lib/crash-report";
import { currentRoutePath } from "../lib/report-problem";
import { routeSurface } from "../lib/telemetry";
import { Button } from "./ui/button";

// The diagnosable part of a crash screen: what threw, on which surface, in
// which build, with the stacks one click away. Rendered inside the error
// boundaries' fallbacks — the owning panel keeps role="alert".
export function CrashDiagnostics({ error, operation, componentStack }: {
	error: unknown;
	operation?: string;
	componentStack?: string | null;
}) {
	const { t } = useTranslation();
	const [version, setVersion] = useState("unknown");
	const [copied, setCopied] = useState(false);
	const copiedTimeout = useRef<ReturnType<typeof setTimeout> | null>(null);
	const normalized = error instanceof Error ? error : new Error(String(error));
	const surface = routeSurface(currentRoutePath());
	const stack = normalized.stack ?? `${normalized.name}: ${normalized.message}`;

	useEffect(() => {
		let alive = true;
		aoBridge.app.getVersion().then((value) => {
			if (alive) setVersion(value || "unknown");
		}).catch(() => undefined);
		return () => {
			alive = false;
			if (copiedTimeout.current !== null) clearTimeout(copiedTimeout.current);
		};
	}, []);

	const copyReport = () => {
		const text = formatCrashReport({
			title: t("appError.title"),
			name: normalized.name,
			message: normalized.message,
			surface,
			operation: operation ?? "unknown",
			version,
			timestamp: new Date().toISOString(),
			stack,
			componentStack,
		});
		void aoBridge.clipboard.writeText(text).then(() => {
			setCopied(true);
			if (copiedTimeout.current !== null) clearTimeout(copiedTimeout.current);
			copiedTimeout.current = setTimeout(() => {
				setCopied(false);
				copiedTimeout.current = null;
			}, 2_000);
		}).catch(() => undefined);
	};

	return (
		<div className="flex w-full max-w-xl flex-col gap-2 text-left">
			<p className="wrap-break-word font-mono text-caption text-muted-foreground">
				{normalized.name}: {normalized.message}
			</p>
			<p className="text-caption text-muted-foreground">
				{t("appError.version", { version })}
				{" · "}
				{t("appError.surface", { surface })}
			</p>
			<details className="w-full">
				<summary className="cursor-pointer text-caption text-muted-foreground underline-offset-2 hover:underline">
					{t("appError.details")}
				</summary>
				<pre className="mt-1 max-h-64 w-full overflow-auto rounded-md border border-border bg-muted/40 px-2 py-1.5 font-mono text-micro leading-relaxed text-muted-foreground">
					{componentStack ? `${stack}\n\nReact component stack:\n${componentStack}` : stack}
				</pre>
			</details>
			<div>
				<Button size="sm" variant="ghost" onClick={copyReport}>
					{copied ? t("appError.copied") : t("appError.copyDetails")}
				</Button>
			</div>
		</div>
	);
}
