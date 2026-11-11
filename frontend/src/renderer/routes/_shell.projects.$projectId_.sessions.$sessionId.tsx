import { CatchBoundary, createFileRoute } from "@tanstack/react-router";
import { Suspense, useEffect, useRef } from "react";
import { SessionView } from "../components/SessionView";
import { LazyChunkErrorPanel } from "../components/LazyChunkBoundary";
import { loadSessionView } from "../lib/session-view-chunk";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { captureRendererException } from "../lib/telemetry";
import { captureCrashReport } from "../lib/crash-report";

/*
SessionView renders from the route itself: the shell-level host would only
add indirection now that a single local session route exists. Switching
sessions keeps the route mounted — SessionView picks up the new session
through its props.
*/
export const Route = createFileRoute("/_shell/projects/$projectId_/sessions/$sessionId")({
	loader: async () => {
		await loadSessionView();
	},
	pendingMs: Number.POSITIVE_INFINITY,
	errorComponent: SessionRouteErrorPanel,
	component: ProjectSessionRoute,
});

function captureSessionViewCrash(error: Error) {
	void captureRendererException(error, { source: "react-error-boundary", operation: "session_view" });
	captureCrashReport(error, { operation: "session_view" });
}

// Loader errors land here without any catch below having run, and today they
// reach a crash screen with zero capture anywhere. Log once per error object:
// the route-level reset cannot recover a loader (React.lazy caches the
// rejection), so this panel intentionally offers no Try again.
function SessionRouteErrorPanel({ error }: { error: Error }) {
	const logged = useRef<unknown>(null);
	useEffect(() => {
		if (logged.current === error) return;
		logged.current = error;
		void captureRendererException(error, { source: "react-error-boundary", operation: "session_view_loader" });
		captureCrashReport(error, { operation: "session_view_loader" });
	}, [error]);
	return <LazyChunkErrorPanel error={error} operation="session_view_loader" />;
}

function ProjectSessionRoute() {
	const { projectId, sessionId } = Route.useParams();
	const { org } = useCloudOrg();
	return (
		<CatchBoundary
			errorComponent={({ error, reset }) => <LazyChunkErrorPanel reset={reset} error={error} operation="session_view" />}
			getResetKey={() => sessionId}
			onCatch={captureSessionViewCrash}
		>
			<Suspense fallback={null}>
				<SessionView cloudOrgId={org?.id} projectId={projectId} sessionId={sessionId} />
			</Suspense>
		</CatchBoundary>
	);
}
