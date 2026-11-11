import React from "react";
import { useTranslation } from "react-i18next";
import { captureRendererException } from "../lib/telemetry";
import { captureCrashReport } from "../lib/crash-report";
import { CrashDiagnostics } from "./CrashDiagnostics";

type Props = {
	children: React.ReactNode;
};

type BoundaryProps = Props & {
	fallbackBody: string;
	fallbackTitle: string;
};

type State = {
	hasError: boolean;
	error?: Error;
	componentStack?: string;
};

class TelemetryErrorBoundary extends React.Component<BoundaryProps, State> {
	state: State = { hasError: false };

	static getDerivedStateFromError(error: Error) {
		return { hasError: true, error };
	}

	componentDidCatch(error: Error, info: React.ErrorInfo) {
		void captureRendererException(error, {
			source: "react-error-boundary",
			operation: "react_render",
		});
		captureCrashReport(error, { operation: "react_render", componentStack: info.componentStack });
		this.setState({ componentStack: info.componentStack ?? undefined });
	}

	render() {
		if (this.state.hasError) {
			return (
				<div className="flex h-screen items-center justify-center bg-background px-6 text-center text-foreground">
					<div className="flex flex-col items-center gap-3">
						<h1 className="text-heading-sm font-semibold">{this.props.fallbackTitle}</h1>
						<p className="mt-2 text-sm text-muted-foreground">{this.props.fallbackBody}</p>
						<CrashDiagnostics
							error={this.state.error}
							operation="react_render"
							componentStack={this.state.componentStack}
						/>
					</div>
				</div>
			);
		}
		return this.props.children;
	}
}

export function TelemetryBoundary({ children }: Props) {
	const { t } = useTranslation();
	return (
		<TelemetryErrorBoundary fallbackBody={t("appError.body")} fallbackTitle={t("appError.title")}>
			{children}
		</TelemetryErrorBoundary>
	);
}
