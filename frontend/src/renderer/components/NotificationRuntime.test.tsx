import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { unreadNotificationsQueryKey, type NotificationDTO, type NotificationsCache } from "../lib/notifications";
import { useSessionReadStore } from "../stores/session-read-store";
import { NotificationRuntime } from "./NotificationCenter";

const notifications: NotificationDTO[] = [
	{
		id: "ntf_1",
		sessionId: "sess-1",
		projectId: "proj-1",
		prUrl: "",
		type: "needs_input",
		title: "Needs input",
		body: "",
		status: "unread",
		createdAt: "2026-06-16T10:00:00Z",
		target: { kind: "session", sessionId: "sess-1" },
	},
	{
		id: "ntf_2",
		sessionId: "sess-2",
		projectId: "proj-1",
		prUrl: "",
		type: "ready_to_merge",
		title: "Ready to merge",
		body: "",
		status: "unread",
		createdAt: "2026-06-16T11:00:00Z",
		target: { kind: "session", sessionId: "sess-2" },
	},
];

const unreadCache: NotificationsCache = {
	pages: [{ notifications, nextCursor: undefined, unreadCount: notifications.length, unresolvedCount: notifications.length }],
	pageParams: [""],
};

const { apiPostMock, modeMock, setBadge } = vi.hoisted(() => ({ apiPostMock: vi.fn(), modeMock: vi.fn(), setBadge: vi.fn() }));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn(), useParams: () => ({}) }));

vi.mock("../hooks/useNotificationsQuery", () => ({
	useNotificationsQuery: () => ({ data: unreadCache, isError: false }),
}));

vi.mock("../lib/api-client", async (importOriginal) => {
	const real = await importOriginal<typeof import("../lib/api-client")>();
	return { ...real, apiClient: { ...real.apiClient, POST: apiPostMock } };
});

vi.mock("../lib/notifications", async (importOriginal) => ({
	...((await importOriginal()) as object),
	createNotificationsTransport: () => ({ connect: () => undefined }),
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		notifications: {
			// Quiet (false) by default; the loud test opts back in (see
			// notification-signals for the surface policy).
			mode: () => modeMock(),
			setBadge,
			show: vi.fn(),
			devBounce: vi.fn(),
			onClick: () => () => undefined,
		},
	},
}));

function renderRuntimeOnly(seedUnreadCache = true) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	// The mocked query hook bypasses the cache, but the read-parity subscription
	// reads the session's rows from the queryClient the way production does.
	if (seedUnreadCache) {
		queryClient.setQueryData<NotificationsCache>(unreadNotificationsQueryKey, unreadCache);
	}
	return render(
		<QueryClientProvider client={queryClient}>
			{/* NotificationCenter (the bell) is intentionally NOT mounted here. */}
			<NotificationRuntime />
		</QueryClientProvider>,
	);
}

describe("NotificationRuntime badge sync", () => {
	beforeEach(() => {
		setBadge.mockClear();
		modeMock.mockResolvedValue(false);
		// Read parity (fork issue #376) reads the sidebar's read times: pin the
		// baseline before the fixtures so "conversation never opened" is
		// deterministic instead of inheriting the store's first-run now.
		useSessionReadStore.setState({ baseline: Date.parse("2026-06-16T00:00:00Z"), readAt: {} });
	});

	// Quiet by default (fork issue #370): the badge counts only the decision
	// classes, so an unread ready_to_merge row never lights the launcher.
	it("counts only the decision classes in quiet mode", async () => {
		renderRuntimeOnly();

		await waitFor(() => expect(setBadge).toHaveBeenCalledWith(1));
	});

	it("counts every unread row in loud mode, without the bell being mounted", async () => {
		modeMock.mockResolvedValue(true);

		renderRuntimeOnly();

		// The badge must update purely from the always-mounted runtime, independent
		// of NotificationCenter, which Linux hides from the topbar and only mounts
		// on the sessions board.
		await waitFor(() => expect(setBadge).toHaveBeenCalledWith(notifications.length));
	});
});

// Read parity (fork issue #376): the read event that clears a session's
// sidebar unread circle also clears that session's unread bell rows — except
// a needs_input row whose agent still waits on the user.
describe("NotificationRuntime read parity", () => {
	beforeEach(() => {
		setBadge.mockClear();
		apiPostMock.mockReset();
		modeMock.mockResolvedValue(false);
		useSessionReadStore.setState({ baseline: Date.parse("2026-06-16T00:00:00Z"), readAt: {} });
	});

	it("acks a read session's rows and holds the pending-input row", async () => {
		apiPostMock.mockResolvedValue({ data: { updatedCount: 1 } });
		renderRuntimeOnly();

		// Opening sess-2 reads its conversation: the ready_to_merge row clears.
		useSessionReadStore.getState().markRead(":sess-2", Date.parse("2026-06-16T12:00:00Z"));

		await waitFor(() => expect(apiPostMock).toHaveBeenCalledTimes(1));
		expect(apiPostMock.mock.calls[0][0]).toBe("/api/v1/notifications/read-all");
		expect(apiPostMock.mock.calls[0][1]).toEqual({ body: { ids: ["ntf_2"] } });

		// Opening sess-1 reads its conversation too, but its agent still waits:
		// the pending needs_input row must stay lit.
		useSessionReadStore.getState().markRead(":sess-1", Date.parse("2026-06-16T12:00:00Z"));
		await waitFor(() => expect(apiPostMock).toHaveBeenCalledTimes(1));
	});
});
