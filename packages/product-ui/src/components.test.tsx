import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AgentAvatar } from "./AgentAvatar";
import { GithubAvatar } from "./GithubAvatar";
import { scmUserAvatarUrl } from "./scm-avatar";
import { UserAvatar } from "./UserAvatar";
import type { ExternalLinkProps } from "./external-link";
import { PRCardStatusSummary, PRSummaryMeta, PRSummaryParts } from "./PRSummaryDisplay";
import type { PRCardPresentation, PRDisplayTone, PRSummaryPart } from "./pull-request-models";

function ExternalLink({ ariaLabel, children, stopPropagation, ...props }: ExternalLinkProps) {
	return (
		<a
			{...props}
			aria-label={ariaLabel}
			onClick={stopPropagation ? (event) => event.stopPropagation() : undefined}
		>
			{children}
		</a>
	);
}

describe("portable leaf components", () => {
	it("keeps user initials visible until the provider avatar loads and after errors", () => {
		const avatarUrl = "https://avatars.githubusercontent.com/u/123?v=4";
		const { container } = render(<UserAvatar imageUrl={avatarUrl} name="ada-lovelace" />);
		const image = container.querySelector("img");

		expect(container).toHaveTextContent("AL");
		expect(image).toHaveAttribute("src", avatarUrl);
		expect(image).toHaveClass("opacity-0");
		if (image) fireEvent.load(image);
		expect(image).toHaveClass("opacity-100");
		expect(container).not.toHaveTextContent("AL");
		if (image) fireEvent.error(image);
		expect(container.querySelector("img")).not.toBeInTheDocument();
		expect(container).toHaveTextContent("AL");
	});

	it("renders only initials when no provider avatar URL exists", () => {
		const { container } = render(<UserAvatar name="ada-lovelace" />);

		expect(container).toHaveTextContent("AL");
		expect(container.querySelector("img")).not.toBeInTheDocument();
	});

	it("derives provider avatar endpoints only when an observation has no URL", () => {
		expect(
			scmUserAvatarUrl("github", "https://github.com/acme/repo/pull/7", "ada-lovelace"),
		).toBe("https://avatars.githubusercontent.com/ada-lovelace?size=64");
		expect(
			scmUserAvatarUrl("github", "https://git.example.com/acme/repo/pull/7", "ada-lovelace"),
		).toBe("https://git.example.com/ada-lovelace.png?size=64");
		expect(scmUserAvatarUrl("unknown", "https://example.com/pull/7", "ada-lovelace")).toBeUndefined();
	});

	it("keeps the GitHub-specific compatibility component login-derived", () => {
		const { container } = render(<GithubAvatar login="ada-lovelace" />);

		expect(container.querySelector("img")).toHaveAttribute(
			"src",
			"https://avatars.githubusercontent.com/ada-lovelace?size=64",
		);
	});

	it("renders an injected agent logo without owning app assets", () => {
		render(<AgentAvatar logoSources={{ "claude-code": "/logos/claude.svg" }} provider="claude-code" />);

		expect(screen.getByRole("img", { name: "claude-code" })).toHaveAttribute(
			"src",
			"/logos/claude.svg",
		);
	});

	it("names a light/dark logo pair once and leaves both images decorative", () => {
		render(
			<AgentAvatar
				logoSources={{ "claude-code": { light: "/logos/claude-light.svg", dark: "/logos/claude-dark.svg" } }}
				provider="claude-code"
			/>,
		);

		const mark = screen.getByRole("img", { name: "claude-code" });
		const images = Array.from(mark.querySelectorAll("img"));
		expect(images.map((img) => img.getAttribute("src"))).toEqual(["/logos/claude-light.svg", "/logos/claude-dark.svg"]);
		expect(images.every((img) => img.getAttribute("alt") === "")).toBe(true);
	});

	it("renders fallback identity metadata for unknown agents", () => {
		render(<AgentAvatar provider="new-agent" />);

		expect(screen.getByRole("img", { name: "new-agent" })).toHaveTextContent("N");
	});

	it("renders neutral PR metadata with injected plural labels", () => {
		render(
			<PRSummaryMeta
				countNounLabel={(count, noun) => `${count} localized-${noun}`}
				externalLink={ExternalLink}
				pr={{
					provider: "github",
					author: "ada",
					authorAvatarUrl: "https://avatars.githubusercontent.com/u/123?v=4",
					sourceBranch: "feature",
					targetBranch: "main",
					changedFiles: 2,
					additions: 4,
					deletions: 1,
				}}
			/>,
		);

		expect(screen.getByText("feature → main")).toBeInTheDocument();
		expect(screen.getByText("2 localized-file")).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "ada" })).toHaveAttribute("href", "https://github.com/ada");
		expect(screen.getByRole("link", { name: "ada" }).querySelector("img")).toHaveAttribute(
			"src",
			"https://avatars.githubusercontent.com/u/123?v=4",
		);
		expect(screen.getByText("feature → main")).toHaveClass("break-words");
	});

	it("renders precomputed PR presentation without controller dependencies", () => {
		const presentation: PRCardPresentation = {
			primary: {
				key: "review",
				label: "Review required",
				tone: "review",
				links: [],
			},
			supporting: [
				{
					key: "ci",
					label: "Checks running",
					href: "https://example.com/checks",
					breathe: true,
					tone: "validating",
					links: [],
				},
			],
		};
		const { container } = render(
			<PRCardStatusSummary externalLink={ExternalLink} presentation={presentation} />,
		);

		expect(screen.getByText("Review required")).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "Checks running" })).toBeInTheDocument();
		expect(container.querySelector(".animate-spin")).toBeInTheDocument();
		expect(container.querySelector(".animate-status-pulse")).not.toBeInTheDocument();
		expect(container.querySelector(".text-status-validating")).toBeInTheDocument();
	});

	it("ticks an elapsed label next to a running status, never inside its link", () => {
		vi.useFakeTimers();
		// Build `since` under fake timers so the initial label is deterministic.
		const since = new Date(Date.now() - 5_000).toISOString();
		render(
			<PRCardStatusSummary
				externalLink={ExternalLink}
				presentation={{
					primary: { key: "ci", label: "Checks running", tone: "validating", breathe: true, since, href: "https://example.com/checks", links: [] },
					supporting: [],
				}}
			/>,
		);

		const elapsed = screen.getByText("· 5s");
		expect(elapsed).toHaveClass("text-passive");
		// The timer is a sibling of the link, so the accessible name stays bare.
		expect(screen.getByRole("link", { name: "Checks running" })).not.toContainElement(elapsed);

		// The seconds count up every second — a coarser tick reads as frozen.
		act(() => vi.advanceTimersByTime(1_000));
		expect(screen.getByText("· 6s")).toBeInTheDocument();

		act(() => vi.advanceTimersByTime(60_000));
		expect(screen.getByText("· 1m")).toBeInTheDocument();
		vi.useRealTimers();
	});

	it("renders no elapsed label without a since timestamp, a breathe flag, or a valid date", () => {
		for (const status of [
			{ breathe: true },
			{ breathe: true, since: "garbage" },
			{ breathe: false, since: new Date().toISOString() },
		]) {
			const { container } = render(
				<PRCardStatusSummary
					externalLink={ExternalLink}
					presentation={{
						primary: { key: "ci", label: "Checks running", tone: "validating", links: [], ...status },
						supporting: [],
					}}
				/>,
			);
			expect(container.textContent).not.toMatch(/· \d/);
		}
	});

	it("renders the three CI states distinctly: pass, fail, running with spinner", () => {
		const cases: Array<{ tone: PRDisplayTone; breathe?: boolean; glyph: string; toneClass: string }> = [
			{ tone: "success", glyph: ".size-dot-sm", toneClass: ".text-success" },
			{ tone: "error", glyph: ".size-dot-sm", toneClass: ".text-error" },
			{ tone: "validating", breathe: true, glyph: ".animate-spin", toneClass: ".text-status-validating" },
		];
		for (const c of cases) {
			const { container } = render(
				<PRCardStatusSummary
					externalLink={ExternalLink}
					presentation={{
						primary: { key: "ci", label: `Checks ${c.tone}`, tone: c.tone, breathe: c.breathe, links: [] },
						supporting: [],
					}}
				/>,
			);
			expect(container.querySelector(c.glyph), `glyph for ${c.tone}`).toBeInTheDocument();
			expect(container.querySelector(c.toneClass), `tone for ${c.tone}`).toBeInTheDocument();
			// Running CI must never render as failing or neutral.
			if (c.tone === "validating") {
				expect(container.querySelector(".text-error")).not.toBeInTheDocument();
				expect(container.querySelector(".text-muted-foreground")).not.toBeInTheDocument();
			}
		}
	});

	it("keeps review details compact and inline with the review detail", () => {
		const reviewDetailsAction = <button type="button">View review details ↗</button>;
		render(
			<PRCardStatusSummary
				externalLink={ExternalLink}
				presentation={{
					primary: { key: "ci", label: "Checks passing", tone: "success", links: [] },
					supporting: [],
					statusRows: [
						{ key: "ci", label: "Checks passing", tone: "success", links: [] },
						{ key: "review", label: "Review status", detail: "Required review not submitted", tone: "review", links: [] },
					],
					readiness: { label: "Not mergeable yet", detail: "A required review is pending.", tone: "error" },
				}}
				reviewDetailsAction={reviewDetailsAction}
			/>,
		);

		const detail = screen.getByText("Required review not submitted");
		expect(detail.parentElement).toHaveClass("flex", "items-baseline");
		expect(detail.parentElement).toContainElement(screen.getByRole("button", { name: "View review details ↗" }));
		expect(detail.closest(".grid")).toHaveClass("grid-cols-1");
	});

	it("computes overflow against the requested link limit", () => {
		const parts: PRSummaryPart[] = [
			{
				key: "ci",
				label: "CI",
				status: "Failing",
				links: [{ label: "unit" }, { label: "lint" }, { label: "types" }],
				linkTotal: 3,
				overflowNoun: "check",
				tone: "error",
			},
		];
		render(
			<PRSummaryParts
				externalLink={ExternalLink}
				interactiveLinks={false}
				maxLinks={2}
				parts={parts}
			/>,
		);

		expect(screen.getByText("unit")).toBeInTheDocument();
		expect(screen.getByText("lint")).toBeInTheDocument();
		expect(screen.queryByText("types")).not.toBeInTheDocument();
		expect(screen.getByText("+1 check")).toBeInTheDocument();
	});
});
