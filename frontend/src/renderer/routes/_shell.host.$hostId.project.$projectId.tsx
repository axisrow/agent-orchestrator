import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useEffect } from "react";
import { useRemoteProjectQuery } from "../hooks/useWorkspaceQuery";
import { projectLandingSessionId, sessionNavigateTarget } from "../lib/navigate-to-session";

export const Route = createFileRoute("/_shell/host/$hostId/project/$projectId")({
	component: HostProjectRoute,
});

// Remote twin of the project redirect (#163): the kanban board is gone, so
// opening a hosted project lands on its top sidebar session, or home when
// the project has no live workers.
function HostProjectRoute() {
	const { hostId, projectId } = Route.useParams();
	const navigate = useNavigate();
	const { data: workspace, isPending } = useRemoteProjectQuery(hostId, projectId);
	const sessionId = workspace ? projectLandingSessionId(workspace.sessions) : undefined;

	useEffect(() => {
		if (isPending) return;
		if (sessionId) void navigate(sessionNavigateTarget(projectId, sessionId, hostId));
		else void navigate({ to: "/" });
	}, [hostId, isPending, navigate, projectId, sessionId]);

	return null;
}
