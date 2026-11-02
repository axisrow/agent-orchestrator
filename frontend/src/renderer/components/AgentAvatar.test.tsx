import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AgentAvatar } from "./AgentAvatar";
import agyLogo from "../assets/agents/agy.svg";
import ompLogo from "../assets/agents/omp.svg";
import piDarkLogo from "../assets/agents/pi-dark.svg";
import piLightLogo from "../assets/agents/pi-light.svg";

function themeSources(name: string) {
	const mark = screen.getByRole("img", { name });
	return Array.from(mark.querySelectorAll("img"), (img) => img.getAttribute("src"));
}

describe("AgentAvatar", () => {
	it("renders the official pi light and dark marks under one accessible name", () => {
		render(<AgentAvatar provider="pi" />);

		expect(themeSources("pi")).toEqual([piLightLogo, piDarkLogo]);
	});

	it("renders a single mark when the brand reads on both themes", () => {
		render(<AgentAvatar provider="agy" />);

		expect(screen.getByRole("img", { name: "agy" })).toHaveAttribute("src", agyLogo);
	});

	it("renders the bundled OMP mark", () => {
		render(<AgentAvatar provider="omp" />);
		expect(screen.getByRole("img", { name: "omp" })).toHaveAttribute("src", ompLogo);
	});

	it("gives every harness a theme-safe brand mark", () => {
		const providers = ["codex", "claude-code", "opencode-v2", "omp", "pi", "agy"];
		render(
			<>
				{providers.map((provider) => (
					<AgentAvatar key={provider} provider={provider} />
				))}
			</>,
		);

		for (const provider of providers) {
			const mark = screen.getByRole("img", { name: provider });
			expect(mark.tagName === "IMG" || mark.querySelectorAll("img").length === 2).toBe(true);
		}
	});

	it("falls back to a lettered tile for the removed OpenCode 1 harness", () => {
		render(<AgentAvatar provider="opencode" />);

		expect(screen.getByRole("img", { name: "opencode" }).querySelector("img")).toBeNull();
	});
});
