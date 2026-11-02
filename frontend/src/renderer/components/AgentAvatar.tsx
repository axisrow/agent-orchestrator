import {
	AgentAvatar as ProductAgentAvatar,
	type AgentAvatarProps,
	type AgentLogoSources,
} from "@aoagents/product-ui";
import agyLogo from "../assets/agents/agy.svg";
import ampDarkLogo from "../assets/agents/amp-dark.svg";
import ampLightLogo from "../assets/agents/amp-light.svg";
import autohandDarkLogo from "../assets/agents/autohand-dark.svg";
import autohandLightLogo from "../assets/agents/autohand-light.svg";
import claudeLogo from "../assets/agents/claude.svg";
import claudeCodeLogo from "../assets/agents/claude-code.svg";
import codexLogo from "../assets/agents/codex.svg";
import droidDarkLogo from "../assets/agents/droid-dark.svg";
import droidLightLogo from "../assets/agents/droid-light.svg";
import fxDarkLogo from "../assets/agents/fx-dark.svg";
import fxLightLogo from "../assets/agents/fx-light.svg";
import gooseDarkLogo from "../assets/agents/goose-dark.svg";
import gooseLightLogo from "../assets/agents/goose-light.svg";
import kimiDarkLogo from "../assets/agents/kimi-dark.svg";
import kimiLightLogo from "../assets/agents/kimi-light.svg";
import memcodeLogo from "../assets/agents/memcode.png";
import ompLogo from "../assets/agents/omp.svg";
import opencodeDarkLogo from "../assets/agents/opencode-dark.svg";
import opencodeLightLogo from "../assets/agents/opencode-light.svg";
import piDarkLogo from "../assets/agents/pi-dark.svg";
import piLightLogo from "../assets/agents/pi-light.svg";
import qwenDarkLogo from "../assets/agents/qwen-dark.svg";
import qwenLightLogo from "../assets/agents/qwen-light.svg";

// Official brand marks keyed by the harness name AO stores on session.provider.
// A { light, dark } pair is used where the brand ships separate marks for each
// background; a single mark reads on both. Agents without an asset fall back to
// a lettered tile.
const LOGOS: AgentLogoSources = {
	codex: codexLogo,
	"claude-code": claudeCodeLogo,
	claude: claudeLogo,
	"opencode-v2": { light: opencodeLightLogo, dark: opencodeDarkLogo },
	droid: { light: droidLightLogo, dark: droidDarkLogo },
	fx: { light: fxLightLogo, dark: fxDarkLogo },
	qwen: { light: qwenLightLogo, dark: qwenDarkLogo },
	goose: { light: gooseLightLogo, dark: gooseDarkLogo },
	kimi: { light: kimiLightLogo, dark: kimiDarkLogo },
	memcode: memcodeLogo,
	omp: ompLogo,
	pi: { light: piLightLogo, dark: piDarkLogo },
	amp: { light: ampLightLogo, dark: ampDarkLogo },
	agy: agyLogo,
	autohand: { light: autohandLightLogo, dark: autohandDarkLogo },
};

/**
 * Agent mark for board/task cards: the harness's real brand logo rendered bare —
 * no tile, border, or background — so each brand's own shape shows (codex,
 * claude, … carry their own rounded backgrounds). Agents without an
 * asset fall back to a bare initial. Kept small so the title stays the hero.
 *
 * The provider is exposed as the accessible name (alt / aria-label), not just a
 * hover title, so surfaces that show the logo in place of visible agent text —
 * e.g. the archive cards — still name the agent for screen readers.
 */
export function AgentAvatar({ provider, className, decorative = false }: AgentAvatarProps) {
	return <ProductAgentAvatar className={className} decorative={decorative} logoSources={LOGOS} provider={provider} />;
}

export type { AgentAvatarProps };
