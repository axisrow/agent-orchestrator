import { useMemo } from "react";
import type { SessionProvisionStep } from "../../types/workspace";
import { ShellTopbar } from "../ShellTopbar";
import type { ConversationSnapshot } from "../../types/conversation";
import { ChatWorkspace } from "./ChatWorkspace";
import { CloudSessionStartupLoader } from "../CloudSessionStartupLoader";

export function startingConversationSnapshot(sessionId: string, harness: ConversationSnapshot["harness"]): ConversationSnapshot {
	return {
		conversationId: sessionId, sessionId, harness, mode: "chat",
		controller: { state: "connecting" }, latestSequence: 0, oldestSequence: 0,
		hasMoreBefore: false, activeBranchId: "branch-root", branchPoints: [],
		settings: {}, mcpServers: [], capabilities: [], turns: [], items: [],
	};
}

export function OrchestratorStartingChat({ steps, cloud = false, coderWorkspaceUrl, harness }: { steps?: readonly SessionProvisionStep[]; cloud?: boolean; coderWorkspaceUrl?: string; harness?: string }) {
	// The harness being started picks the agent icon; Claude Code only when
	// the project names none and no orchestrator session exists yet.
	const snapshot = useMemo(() => startingConversationSnapshot("pending-orchestrator", harness || "claude-code"), [harness]);
	if (cloud) return <CloudSessionStartupLoader coderWorkspaceUrl={coderWorkspaceUrl} />;
	return <ChatWorkspace
		snapshot={snapshot}
		sessionRole="orchestrator"
		startingSteps={steps}
		newWorkDisabled
		inlineHeader
		headerActions={
			<div className="session-topbar-session-chrome flex shrink-0 items-center" data-compact-session-chrome="false">
				<ShellTopbar embedded startingOrchestrator />
			</div>
		}
	/>;
}
