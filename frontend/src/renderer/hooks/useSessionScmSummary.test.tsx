import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { apiClient } from "../lib/api-client";
import type { PullRequestFacts, WorkspaceSummary } from "../types/workspace";
import type { SessionPRSummary } from "./useSessionScmSummary";
import { sessionScmSummaryQueryKey, usePRFactsRefresh, useSessionScmSummary } from "./useSessionScmSummary";
import { workspaceQueryKey } from "./useWorkspaceQuery";

const { listSessionPullRequestsMock, subscribeSessionEventsMock, refreshPostMock } = vi.hoisted(() => ({
	listSessionPullRequestsMock: vi.fn(),
	subscribeSessionEventsMock: vi.fn(),
	refreshPostMock: vi.fn(),
}));

vi.mock("../lib/cloud-cp/stream-bridge", () => ({
	subscribeSessionEventsBridged: subscribeSessionEventsMock,
}));

vi.mock("../lib/cloud-cp/renderer-client", () => ({
	createRendererCloudCpClient: () => ({ listSessionPullRequests: listSessionPullRequestsMock }),
}));

vi.mock("./useSettings", () => ({
	useSettings: () => ({ settings: { cloudControlPlaneUrl: "https://cloud.example.test" } }),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: vi.fn(async () => ({ data: { prs: [] } })) },
}));

vi.mock("../lib/host-clients", () => ({
	clientForHost: vi.fn(),
	clientForSessionHost: () => ({ POST: refreshPostMock }),
}));

describe("useSessionScmSummary cloud source", () => {
	it.each(["scm.updated", "pull_request.created", "pull_request.claimed"])(
		"refreshes the inspector after %s",
		async (type) => {
			listSessionPullRequestsMock.mockReset().mockResolvedValue({ sessionId: "session-1", pullRequests: [] });
			subscribeSessionEventsMock.mockReset().mockResolvedValue(undefined);
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			const wrapper = ({ children }: { children: ReactNode }) => (
				<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
			);
			const { unmount } = renderHook(() => useSessionScmSummary("session-1", true, "org-1"), { wrapper });
			await waitFor(() => expect(listSessionPullRequestsMock).toHaveBeenCalledTimes(1));
			await waitFor(() => expect(subscribeSessionEventsMock).toHaveBeenCalledTimes(1));
			const onEvent = subscribeSessionEventsMock.mock.calls[0][0].onEvent;
			act(() => onEvent({ type, sequence: 1 }));
			await waitFor(() => expect(listSessionPullRequestsMock).toHaveBeenCalledTimes(2));
			unmount();
		},
	);

	it("resumes SCM events after the stream closes", async () => {
		listSessionPullRequestsMock.mockReset().mockResolvedValue({ sessionId: "session-1", pullRequests: [] });
		let closeStream: (() => void) | undefined;
		subscribeSessionEventsMock.mockReset()
			.mockImplementationOnce(() => new Promise<void>((resolve) => { closeStream = resolve; }))
			.mockImplementation(() => new Promise<void>(() => undefined));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const wrapper = ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
		);
		const { unmount } = renderHook(() => useSessionScmSummary("session-1", true, "org-1"), { wrapper });
		await waitFor(() => expect(subscribeSessionEventsMock).toHaveBeenCalledTimes(1));
		act(() => subscribeSessionEventsMock.mock.calls[0][0].onEvent({ type: "scm.updated", sequence: 12 }));
		await waitFor(() => expect(listSessionPullRequestsMock).toHaveBeenCalledTimes(2));
		act(() => closeStream?.());
		await waitFor(() => expect(subscribeSessionEventsMock).toHaveBeenCalledTimes(2), { timeout: 2000 });
		expect(subscribeSessionEventsMock.mock.calls[1][0].after).toBe(12);
		act(() => subscribeSessionEventsMock.mock.calls[1][0].onEvent({ type: "scm.updated", sequence: 13 }));
		await waitFor(() => expect(listSessionPullRequestsMock).toHaveBeenCalledTimes(3));
		unmount();
	});

	it("maps Cloud PR details into the exact local inspector model", async () => {
		listSessionPullRequestsMock.mockResolvedValue({
			sessionId: "session-1",
			pullRequests: [{
				url: "https://api.github.com/repos/acme/widgets/pulls/7",
				htmlUrl: "https://github.com/acme/widgets/pull/7",
				number: 7,
				title: "Cloud details",
				state: "open",
				provider: "github",
				repository: "acme/widgets",
				author: "octocat",
				sourceBranch: "feature",
				targetBranch: "main",
				headSha: "abc123",
				additions: 4,
				deletions: 1,
				changedFiles: 2,
				ci: { state: "passing", failingChecks: [] },
				review: {
					decision: "none",
					hasUnresolvedHumanComments: false,
					unresolvedBy: [],
					reviews: [],
				},
				mergeability: {
					state: "mergeable",
					reasons: [],
					pullRequestUrl: "https://github.com/acme/widgets/pull/7",
					conflictFiles: [],
				},
				updatedAt: "2026-09-22T00:00:00Z",
				observedAt: "2026-09-22T00:00:00Z",
				ciObservedAt: "2026-09-22T00:00:00Z",
				reviewObservedAt: "2026-09-22T00:00:00Z",
			}],
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const wrapper = ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
		);

		const { result } = renderHook(
			() => useSessionScmSummary("session-1", true, "org-1"),
			{ wrapper },
		);

		await waitFor(() => expect(result.current.data?.prs[0]?.title).toBe("Cloud details"));
		expect(result.current.data?.prs[0]).toMatchObject({
			repo: "acme/widgets",
			mergeability: { prUrl: "https://github.com/acme/widgets/pull/7" },
		});
	});
});

describe("useSessionScmSummary facts convergence", () => {
	const prUrl = "https://github.com/acme/app/pull/7";

	const facts = (overrides: Partial<PullRequestFacts> = {}): PullRequestFacts => ({
		url: prUrl,
		number: 7,
		state: "open",
		ci: "passing",
		review: "none",
		mergeability: "mergeable",
		reviewComments: false,
		updatedAt: "2026-11-15T10:00:00Z",
		...overrides,
	});

	const summary = (overrides: Record<string, unknown> = {}) => ({
		url: prUrl,
		htmlUrl: prUrl,
		number: 7,
		title: "Fix it",
		state: "open",
		provider: "github",
		repo: "acme/app",
		author: "ada",
		sourceBranch: "fix/it",
		targetBranch: "main",
		headSha: "abc123",
		additions: 1,
		deletions: 0,
		changedFiles: 1,
		ci: { autoInjectCI: true, state: "passing", failingChecks: [], checks: [] },
		review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
		mergeability: { state: "mergeable", reasons: [], prUrl, conflictFiles: [] },
		updatedAt: "2026-11-15T10:00:00Z",
		observedAt: "2026-11-15T10:00:00Z",
		...overrides,
	});

	function seedWorkspace(queryClient: QueryClient, prs: PullRequestFacts[]) {
		const workspaces: WorkspaceSummary[] = [{
			id: "ws-1",
			name: "app",
			kind: "single_repo",
			path: "/tmp/app",
			sessions: [{
				id: "sess-1",
				workspaceId: "ws-1",
				workspaceName: "app",
				title: "fix",
				provider: "codex",
				status: "idle",
				updatedAt: "2026-11-15T10:00:00Z",
				prs,
			}],
		}];
		queryClient.setQueryData<WorkspaceSummary[]>(workspaceQueryKey, workspaces);
		return workspaces;
	}

	function serveSummaries(prs: Record<string, unknown>[]) {
		vi.mocked(apiClient.GET).mockImplementation(async (path) => {
			if (path === "/api/v1/sessions/{sessionId}/pr") {
				return { data: { sessionId: "sess-1", prs, linkedPrs: [] } } as never;
			}
			throw new Error(`unexpected GET ${String(path)}`);
		});
	}

	it("invalidates the workspace and the summaries when their gate facts disagree", async () => {
		// The card's query has learned about a changes_requested verdict; the
		// workspace the sidebar zone renders from still says mergeable. One
		// invalidation pulls both to the same answer, with no window focus.
		serveSummaries([summary({
			review: { decision: "changes_requested", hasUnresolvedHumanComments: false, unresolvedBy: [] },
		})]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		seedWorkspace(queryClient, [facts()]);
		const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockImplementation(async () => undefined);
		const { result } = renderHook(() => useSessionScmSummary("sess-1", true), { wrapper: ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
		) });

		await waitFor(() => expect(result.current.data?.prs[0]?.review.decision).toBe("changes_requested"));
		await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: workspaceQueryKey }));
		expect(invalidate).toHaveBeenCalledWith({ queryKey: sessionScmSummaryQueryKey("sess-1", undefined) });
	});

	it("invalidates both queries in the other direction when the workspace facts move first", async () => {
		serveSummaries([summary()]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		seedWorkspace(queryClient, [facts({ review: "changes_requested" })]);
		const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockImplementation(async () => undefined);
		renderHook(() => useSessionScmSummary("sess-1", true), { wrapper: ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
		) });

		await waitFor(() => {
			expect(invalidate).toHaveBeenCalledWith({ queryKey: workspaceQueryKey });
			expect(invalidate).toHaveBeenCalledWith({ queryKey: sessionScmSummaryQueryKey("sess-1", undefined) });
		});
	});

	it("leaves matched facts alone", async () => {
		serveSummaries([summary()]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		seedWorkspace(queryClient, [facts()]);
		const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockImplementation(async () => undefined);
		const { result } = renderHook(() => useSessionScmSummary("sess-1", true), { wrapper: ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
		) });

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(invalidate).not.toHaveBeenCalled();
	});

	it("bounds the sync while the workspace list stays behind", async () => {
		serveSummaries([summary({
			review: { decision: "changes_requested", hasUnresolvedHumanComments: false, unresolvedBy: [] },
		})]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		seedWorkspace(queryClient, [facts()]);
		const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockImplementation(async () => undefined);
		const { result } = renderHook(() => useSessionScmSummary("sess-1", true), { wrapper: ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
		) });

		await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: workspaceQueryKey }));
		// A refetch that still disagrees inside the sync interval waits for the
		// next one instead of hammering both lists.
		await result.current.refetch();
		const syncs = invalidate.mock.calls.filter(([input]) => JSON.stringify((input as { queryKey: unknown[] }).queryKey) === JSON.stringify(workspaceQueryKey)).length;
		expect(syncs).toBe(1);
	});
});

// The one-shot provider peek (#338): a card opened onto stale facts asks the
// daemon once, patches the fresh facts into its cached summaries read-only,
// and lets the workspace list follow. The regression case is the terminated
// session whose PR the provider merged while the stored row still says open.
describe("usePRFactsRefresh", () => {
	const prUrl = "https://github.com/acme/app/pull/1361";
	const old = "2026-08-26T12:00:00Z";

	const staleSummary = (overrides: Partial<SessionPRSummary> = {}): SessionPRSummary => ({
		url: prUrl,
		htmlUrl: prUrl,
		number: 1361,
		title: "Batch ledger",
		state: "open",
		provider: "github",
		repo: "acme/app",
		author: "ada",
		sourceBranch: "fix/ledger",
		targetBranch: "main",
		headSha: "abc123",
		additions: 2,
		deletions: 1,
		changedFiles: 1,
		ci: { autoInjectCI: true, state: "passing", failingChecks: [], checks: [] },
		review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
		mergeability: { state: "mergeable", reasons: [], prUrl, conflictFiles: [] },
		updatedAt: old,
		observedAt: old,
		lastSeenAt: old,
		...overrides,
	}) as SessionPRSummary;

	function seedCard(queryClient: QueryClient, prs: SessionPRSummary[]) {
		queryClient.setQueryData(sessionScmSummaryQueryKey("sess-1", undefined), { prs, linkedPrs: [] });
	}

	it("peeks once and reconciles a snapshot the provider has already merged", async () => {
		refreshPostMock.mockReset().mockResolvedValue({
			data: {
				ok: true,
				sessionId: "sess-1",
				refreshedAt: "2026-11-15T12:00:00Z",
				prs: [{
					url: prUrl, number: 1361, state: "merged", ci: "passing",
					review: "none", mergeability: "mergeable", updatedAt: "2026-11-15T12:00:00Z",
				}],
			},
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		seedCard(queryClient, [staleSummary()]);
		// The workspace facts still say open: the peek must patch them too, or
		// the facts-convergence effect would see the patched summaries disagree
		// with the frozen workspace facts and invalidate both back to stale.
		queryClient.setQueryData<WorkspaceSummary[]>(workspaceQueryKey, [{
			id: "ws-1",
			name: "app",
			kind: "single_repo",
			path: "/tmp/app",
			sessions: [{
				id: "sess-1",
				workspaceId: "ws-1",
				workspaceName: "app",
				title: "fix",
				provider: "codex",
				status: "terminated",
				updatedAt: "2026-11-15T10:00:00Z",
				prs: [{
					url: prUrl, number: 1361, state: "open", ci: "passing",
					review: "none", mergeability: "mergeable", reviewComments: false,
					updatedAt: "2026-08-26T12:00:00Z",
				}],
			}],
		}]);
		const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockImplementation(async () => undefined);
		renderHook(() => usePRFactsRefresh("sess-1", staleSummary(), undefined, true), {
			wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>,
		});

		await waitFor(() => expect(refreshPostMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/pr/refresh", { params: { path: { sessionId: "sess-1" } } }));
		const cached = queryClient.getQueryData<{ prs: SessionPRSummary[] }>(sessionScmSummaryQueryKey("sess-1", undefined));
		expect(cached?.prs[0]?.state).toBe("merged");
		expect(cached?.prs[0]?.observedAt).toBe("2026-11-15T12:00:00Z");
		// Both caches carry the peek: the workspace session's facts match the
		// patched summaries, so convergence has nothing to reconcile and the
		// patch survives instead of being refetched away.
		const workspaces = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey);
		expect(workspaces?.[0]?.sessions[0]?.prs[0]?.state).toBe("merged");
		expect(workspaces?.[0]?.sessions[0]?.prs[0]?.updatedAt).toBe("2026-11-15T12:00:00Z");
		expect(invalidate).not.toHaveBeenCalled();
		// One-shot: the effect never asks again while the facts stay put.
		expect(refreshPostMock).toHaveBeenCalledTimes(1);
	});

	it("aligns the comments pair with the summary so a bot thread cannot resurrect the divergence", async () => {
		// The workspace fact counts any author's unresolved comments; the
		// summary's flag is human-only. A frozen session holding an AO thread
		// has fact true / summary false, and leaving that pair alone would keep
		// the convergence effect invalidating both caches away from the peek.
		refreshPostMock.mockReset().mockResolvedValue({
			data: {
				ok: true,
				sessionId: "sess-1",
				refreshedAt: "2026-11-15T12:00:00Z",
				prs: [{
					url: prUrl, number: 1361, state: "open", ci: "passing",
					review: "none", mergeability: "mergeable", updatedAt: "2026-11-15T12:00:00Z",
				}],
			},
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		seedCard(queryClient, [staleSummary({
			review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
		})]);
		queryClient.setQueryData<WorkspaceSummary[]>(workspaceQueryKey, [{
			id: "ws-1",
			name: "app",
			kind: "single_repo",
			path: "/tmp/app",
			sessions: [{
				id: "sess-1",
				workspaceId: "ws-1",
				workspaceName: "app",
				title: "fix",
				provider: "codex",
				status: "terminated",
				updatedAt: "2026-11-15T10:00:00Z",
				prs: [{
					url: prUrl, number: 1361, state: "open", ci: "passing",
					review: "none", mergeability: "mergeable", reviewComments: true,
					updatedAt: "2026-08-26T12:00:00Z",
				}],
			}],
		}]);
		const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockImplementation(async () => undefined);
		renderHook(() => usePRFactsRefresh("sess-1", staleSummary(), undefined, true), {
			wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>,
		});

		await waitFor(() => expect(refreshPostMock).toHaveBeenCalledTimes(1));
		const workspaces = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey);
		// The patched fact carries the summary's human-only flag, so the pair
		// agrees and no invalidation fires.
		expect(workspaces?.[0]?.sessions[0]?.prs[0]?.reviewComments).toBe(false);
		expect(invalidate).not.toHaveBeenCalled();
	});

	it("leaves fresh facts and a failed peek alone", async () => {
		refreshPostMock.mockReset().mockRejectedValue(new Error("provider down"));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		// Freshness is judged against the real clock, so the fresh stamp moves
		// with it: a fixed date aged past the one-hour threshold and the card
		// started asking, flaking the call count.
		const freshStamp = new Date(Date.now() - 60_000).toISOString();
		const fresh = staleSummary({
			updatedAt: freshStamp,
			observedAt: freshStamp,
			lastSeenAt: freshStamp,
		});
		seedCard(queryClient, [fresh]);
		const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockImplementation(async () => undefined);
		renderHook(() => usePRFactsRefresh("sess-1", fresh, undefined, true), {
			wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>,
		});
		renderHook(() => usePRFactsRefresh("sess-1", staleSummary(), undefined, true), {
			wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>,
		});

		await waitFor(() => expect(refreshPostMock).toHaveBeenCalledTimes(1));
		// The fresh card never asked; the stale card's failed peek left its snapshot.
		const cached = queryClient.getQueryData<{ prs: SessionPRSummary[] }>(sessionScmSummaryQueryKey("sess-1", undefined));
		expect(cached?.prs[0]?.state).toBe("open");
		expect(invalidate).not.toHaveBeenCalled();
	});
});
