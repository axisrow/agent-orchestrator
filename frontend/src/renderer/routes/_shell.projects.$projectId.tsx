import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useEffect } from "react";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { projectLandingSessionId, sessionNavigateTarget } from "../lib/navigate-to-session";

export const Route = createFileRoute("/_shell/projects/$projectId")({
	component: ProjectRoute,
});

// The kanban board is gone (#163): opening a project lands on its top
// sidebar session, or home when the project has no live workers.
function ProjectRoute() {
	const { projectId } = Route.useParams();
	const navigate = useNavigate();
	const { data: workspaces, isPending } = useWorkspaceQuery();
	const workspace = workspaces?.find((candidate) => candidate.id === projectId);
	const sessionId = workspace ? projectLandingSessionId(workspace.sessions) : undefined;

	useEffect(() => {
		if (isPending) return;
		if (sessionId) void navigate(sessionNavigateTarget(projectId, sessionId));
		else void navigate({ to: "/" });
	}, [isPending, navigate, projectId, sessionId]);

	return null;
}
