import { useNavigate } from "@tanstack/react-router";
import { useCallback } from "react";
import { LOCAL_HOST, type HostId } from "./hosts";
import { newestActiveOrchestrator, sessionIsActive, sortedWorkerSessions, type WorkspaceSession } from "../types/workspace";

export type SessionNavigateTarget =
	| { to: "/projects/$projectId/sessions/$sessionId"; params: { projectId: string; sessionId: string } }
	| { to: "/host/$hostId/project/$projectId/session/$sessionId"; params: { hostId: string; projectId: string; sessionId: string } };

export function projectNavigateTarget(projectId: string, host: HostId = LOCAL_HOST) {
	return host === LOCAL_HOST
		? { to: "/projects/$projectId" as const, params: { projectId } }
		: { to: "/host/$hostId/project/$projectId" as const, params: { hostId: host, projectId } };
}

/** Id of the session a project click lands on now that the kanban board is
 * gone (#163): the project's live orchestrator — the same session its sidebar
 * icon opens (#260) — or, with none live, the top of the worker order (most
 * recently messaged first). */
export function projectLandingSessionId(sessions: WorkspaceSession[]): string | undefined {
	return newestActiveOrchestrator(sessions)?.id ?? sortedWorkerSessions(sessions.filter(sessionIsActive))[0]?.id;
}

export function sessionNavigateTarget(projectId: string | undefined, sessionId: string, host: HostId = LOCAL_HOST): SessionNavigateTarget {
	if (host !== LOCAL_HOST) {
		return { to: "/host/$hostId/project/$projectId/session/$sessionId", params: { hostId: host, projectId: projectId ?? "", sessionId } };
	}
	return {
		to: "/projects/$projectId/sessions/$sessionId",
		params: { projectId: projectId ?? "", sessionId },
	};
}

export function useNavigateToSession(): (projectId: string | undefined, sessionId: string, host?: HostId) => void {
	const navigate = useNavigate();
	return useCallback(
		(projectId: string | undefined, sessionId: string, host: HostId = LOCAL_HOST) => {
			if (!sessionId) return;
			void navigate(sessionNavigateTarget(projectId, sessionId, host));
		},
		[navigate],
	);
}

export function useNavigateToTerminals(): () => void {
	const navigate = useNavigate();
	return useCallback(() => {
		void navigate({ to: "/terminals" });
	}, [navigate]);
}
