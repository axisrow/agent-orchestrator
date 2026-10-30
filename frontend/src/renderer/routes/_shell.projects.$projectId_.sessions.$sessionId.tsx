import { CatchBoundary, createFileRoute } from "@tanstack/react-router";
import { Suspense } from "react";
import { SessionView } from "../components/SessionView";
import { LazyChunkErrorPanel } from "../components/LazyChunkBoundary";
import { loadSessionView } from "../lib/session-view-chunk";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { captureRendererException } from "../lib/telemetry";

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
	errorComponent: LazyChunkErrorPanel,
	component: ProjectSessionRoute,
});

function captureSessionViewCrash(error: Error) {
	void captureRendererException(error, { source: "react-error-boundary", operation: "session_view" });
}

function ProjectSessionRoute() {
	const { projectId, sessionId } = Route.useParams();
	const { org } = useCloudOrg();
	return (
		<CatchBoundary errorComponent={LazyChunkErrorPanel} getResetKey={() => sessionId} onCatch={captureSessionViewCrash}>
			<Suspense fallback={null}>
				<SessionView cloudOrgId={org?.id} projectId={projectId} sessionId={sessionId} />
			</Suspense>
		</CatchBoundary>
	);
}
