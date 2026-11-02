import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AgentAvatar } from "./AgentAvatar";
import agyLogo from "../assets/agents/agy.svg";
import fxDarkLogo from "../assets/agents/fx-dark.svg";
import fxLightLogo from "../assets/agents/fx-light.svg";
import memcodeLogo from "../assets/agents/memcode.png";

function themeSources(name: string) {
	const mark = screen.getByRole("img", { name });
	return Array.from(mark.querySelectorAll("img"), (img) => img.getAttribute("src"));
}

describe("AgentAvatar", () => {
	it("renders the bundled Memcode mark", () => {
		render(<AgentAvatar provider="memcode" />);
		expect(screen.getByRole("img", { name: "memcode" })).toHaveAttribute("src", memcodeLogo);
	});
	it("renders the official fx light and dark marks under one accessible name", () => {
		render(<AgentAvatar provider="fx" />);

		expect(themeSources("fx")).toEqual([fxLightLogo, fxDarkLogo]);
	});

	it("renders a single mark when the brand reads on both themes", () => {
		render(<AgentAvatar provider="agy" />);

		expect(screen.getByRole("img", { name: "agy" })).toHaveAttribute("src", agyLogo);
	});

	it("gives every harness a theme-safe brand mark", () => {
		const providers = ["omp", "qwen", "droid", "kimi", "goose", "pi", "amp"];
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
