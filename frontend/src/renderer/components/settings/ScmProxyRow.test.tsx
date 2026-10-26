import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { ScmProxyRow } from "./GeneralSettingsSection";

const { update, useSettings, useUpdateScmProxyURL } = vi.hoisted(() => ({
	update: vi.fn(),
	useSettings: vi.fn(),
	useUpdateScmProxyURL: vi.fn(),
}));

vi.mock("../../hooks/useSettings", () => ({
	useSettings,
	useUpdateScmProxyURL,
}));

function mockSettings(partial: Record<string, unknown>) {
	vi.mocked(useSettings).mockReturnValue({
		settings: partial as never,
		isLoading: false,
		error: undefined,
	});
	vi.mocked(useUpdateScmProxyURL).mockReturnValue({
		update,
		saving: false,
		error: undefined,
	});
}

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	update.mockReset();
});

describe("ScmProxyRow", () => {
	it("labels the field by the user's job, not internal SCM vocabulary", () => {
		mockSettings({ scmProxyUrl: "", scmProxyActiveLayer: "direct" });
		render(<ScmProxyRow />);
		expect(screen.getByText("GitHub & GitLab proxy")).toBeInTheDocument();
	});

	it("shows the active layer line for each resolution layer", () => {
		mockSettings({ scmProxyUrl: "socks5h://127.0.0.1:10808", scmProxyActiveLayer: "setting" });
		const { unmount } = render(<ScmProxyRow />);
		expect(screen.getByText(/Active now: socks5h:\/\/127\.0\.0\.1:10808/)).toBeInTheDocument();
		unmount();

		mockSettings({ scmProxyUrl: "", scmProxyActiveLayer: "env" });
		render(<ScmProxyRow />);
		expect(screen.getByText(/Active now: proxy environment variables/)).toBeInTheDocument();
		unmount();

		mockSettings({ scmProxyUrl: "", scmProxyActiveLayer: "direct" });
		render(<ScmProxyRow />);
		expect(screen.getByText(/Active now: direct connection/)).toBeInTheDocument();
	});

	it("hides the active-layer line when the daemon predates the field", () => {
		mockSettings({ scmProxyUrl: "" });
		render(<ScmProxyRow />);
		expect(screen.queryByText(/Active now:/)).not.toBeInTheDocument();
	});
});
