import React from "react";
import { useTranslation } from "react-i18next";
import { captureRendererException } from "../lib/telemetry";
import { captureCrashReport } from "../lib/crash-report";
import { Button } from "./ui/button";
import { CrashDiagnostics } from "./CrashDiagnostics";

type Props = { children: React.ReactNode };
type State = { hasError: boolean; error?: Error; componentStack?: string };

// A superset of TanStack's ErrorComponentProps, so the panel assigns directly
// to errorComponent slots; everything past `reset` is optional diagnostics.
type PanelProps = {
	reset?: () => void;
	error?: unknown;
	operation?: string;
	componentStack?: string | null;
};

/**
 * The in-panel crash state: a short message and a full reload. With `reset`,
 * it first offers to re-render just the panel, which keeps the rest of the app
 * (and its terminals) running. A failed chunk import cannot recover that way.
 */
export function LazyChunkErrorPanel({ reset, error, operation, componentStack }: PanelProps) {
	const { t } = useTranslation();
	return (
		<div className="flex h-full min-h-0 flex-col items-center justify-center gap-3 px-6 text-center" role="alert">
			<p className="text-sm text-muted-foreground">{t("appError.title")}</p>
			{error !== undefined ? (
				<CrashDiagnostics error={error} operation={operation} componentStack={componentStack} />
			) : null}
			<div className="flex items-center gap-2">
				{reset ? (
					<Button size="sm" variant="secondary" onClick={reset}>
						{t("appError.tryAgain")}
					</Button>
				) : null}
				<Button size="sm" variant={reset ? "ghost" : "secondary"} onClick={() => window.location.reload()}>
					{t("titlebar.reload")}
				</Button>
			</div>
		</div>
	);
}

class LazyChunkErrorBoundary extends React.Component<Props, State> {
	state: State = { hasError: false };

	static getDerivedStateFromError(error: Error): State {
		return { hasError: true, error };
	}

	componentDidCatch(error: Error, info: React.ErrorInfo) {
		void captureRendererException(error, { source: "react-error-boundary", operation: "lazy_chunk" });
		captureCrashReport(error, { operation: "lazy_chunk", componentStack: info.componentStack });
		this.setState({ componentStack: info.componentStack ?? undefined });
	}

	render() {
		if (!this.state.hasError) return this.props.children;
		return <LazyChunkErrorPanel error={this.state.error} operation="lazy_chunk" componentStack={this.state.componentStack} />;
	}
}

/**
 * Contains a failed lazy chunk (or a render error inside it) to its own panel.
 * Without it, a rejected import propagates to the route's error screen and takes
 * the whole shell, including every retained terminal, down with it. React.lazy
 * caches the rejection, so recovery is a reload.
 */
export function LazyChunkBoundary({ children }: Props) {
	return <LazyChunkErrorBoundary>{children}</LazyChunkErrorBoundary>;
}
